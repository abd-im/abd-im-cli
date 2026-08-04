package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestMarkReadRequiresWindowBoundaries(t *testing.T) {

	resolver := &fakeResolver{boundaries: map[string]Boundary{
		"after":   {ConversationID: "conversation-1", MessageID: "after", ServerSeq: 10},
		"inside":  {ConversationID: "conversation-1", MessageID: "inside", ServerSeq: 11},
		"before":  {ConversationID: "conversation-1", MessageID: "before", ServerSeq: 12},
		"outside": {ConversationID: "conversation-1", MessageID: "outside", ServerSeq: 13},
	}}
	sender := &fakeSender{}
	handler, grants, credential := newHandler(t, resolver, sender, grant.MessageWindow{
		ConversationID: "conversation-1", AfterMessageID: "after", BeforeMessageID: "before",
	})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key, conversationID, messageID string) contracts.Response {
		raw, _ := json.Marshal(Input{ConversationID: conversationID, UpToMessageID: messageID})
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method,
			Params: raw, Grant: credential, IdempotencyKey: key,
		})
		return response
	}
	if response := call("other-conversation", "conversation-2", "inside"); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("other conversation = %+v, calls=%d", response, sender.calls)
	}
	if response := call("before-start", "conversation-1", "after"); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("after boundary = %+v, calls=%d", response, sender.calls)
	}
	if response := call("at-end", "conversation-1", "before"); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("before boundary = %+v, calls=%d", response, sender.calls)
	}
	if response := call("past-end", "conversation-1", "outside"); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("outside window = %+v, calls=%d", response, sender.calls)
	}
	if response := call("inside", "conversation-1", "inside"); !response.OK || sender.calls != 1 || sender.request != (MarkReadRequest{ConversationID: "conversation-1", HasReadSeq: 11}) {
		t.Fatalf("inside window = %+v, sender=%+v", response, sender)
	}
}

func TestMarkReadRequiresFiniteWindowAndTrustedBoundary(t *testing.T) {

	resolver := &fakeResolver{boundaries: map[string]Boundary{
		"after":  {ConversationID: "conversation-1", MessageID: "after", ServerSeq: 10},
		"inside": {ConversationID: "conversation-1", MessageID: "inside", ServerSeq: 11},
	}}
	sender := &fakeSender{}
	handler, grants, credential := newHandler(t, resolver, sender, grant.MessageWindow{ConversationID: "conversation-1", AfterMessageID: "after"})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	raw, _ := json.Marshal(Input{ConversationID: "conversation-1", UpToMessageID: "inside"})
	response, _ := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: "no-end", ProfileID: "work", Method: Method,
		Params: raw, Grant: credential, IdempotencyKey: "no-end",
	})
	if response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("unbounded window = %+v, calls=%d", response, sender.calls)
	}

	resolver.boundaries["inside"] = Boundary{ConversationID: "conversation-2", MessageID: "inside", ServerSeq: 11}
	handler, grants, credential = newHandler(t, resolver, sender, grant.MessageWindow{
		ConversationID: "conversation-1", AfterMessageID: "after", BeforeMessageID: "inside",
	})
	tool, _ = proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	raw, _ = json.Marshal(Input{ConversationID: "conversation-1", UpToMessageID: "after"})
	response, _ = tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: "bad-server", ProfileID: "work", Method: Method,
		Params: raw, Grant: credential, IdempotencyKey: "bad-server",
	})
	if response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("untrusted boundary = %+v, calls=%d", response, sender.calls)
	}
}

func TestMarkReadIdempotencyAndUnknownOutcomeFailClosed(t *testing.T) {

	resolver := &fakeResolver{boundaries: map[string]Boundary{
		"after":    {ConversationID: "conversation-1", MessageID: "after", ServerSeq: 10},
		"inside-1": {ConversationID: "conversation-1", MessageID: "inside-1", ServerSeq: 11},
		"inside-2": {ConversationID: "conversation-1", MessageID: "inside-2", ServerSeq: 12},
		"before":   {ConversationID: "conversation-1", MessageID: "before", ServerSeq: 13},
	}}
	sender := &fakeSender{}
	handler, grants, credential := newHandler(t, resolver, sender, grant.MessageWindow{
		ConversationID: "conversation-1", AfterMessageID: "after", BeforeMessageID: "before",
	})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key, messageID string) contracts.Response {
		raw, _ := json.Marshal(Input{ConversationID: "conversation-1", UpToMessageID: messageID})
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method,
			Params: raw, Grant: credential, IdempotencyKey: key,
		})
		return response
	}
	first := call("same", "inside-1")
	if !first.OK || sender.calls != 1 || resolver.calls != 3 {
		t.Fatalf("first mark read = %+v, sender=%d resolver=%d", first, sender.calls, resolver.calls)
	}
	second := call("same", "inside-1")
	if !second.OK || string(second.Data) != string(first.Data) || sender.calls != 1 || resolver.calls != 3 {
		t.Fatalf("same key = %+v, sender=%d resolver=%d", second, sender.calls, resolver.calls)
	}
	if response := call("same", "inside-2"); response.Error == nil || response.Error.Code != contracts.CodeIdempotencyConflict || sender.calls != 1 || resolver.calls != 3 {
		t.Fatalf("conflicting key = %+v, sender=%d resolver=%d", response, sender.calls, resolver.calls)
	}

	sender.err = operation.ErrOutcomeUnknown
	if response := call("unknown", "inside-2"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 2 {
		t.Fatalf("unknown mark read = %+v, calls=%d", response, sender.calls)
	}
	if response := call("new-key", "inside-2"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 2 {
		t.Fatalf("unknown rebuilt with new key = %+v, calls=%d", response, sender.calls)
	}
}

func newHandler(t *testing.T, resolver BoundaryResolver, sender Sender, window grant.MessageWindow) (*Handler, *grant.Store, string) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(guard, resolver, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{Method},

		MessageWindow: window,
		ExpiresAt:     time.Now().Add(time.Hour),
		RateBudget:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, grants, credential
}

type fakeResolver struct {
	boundaries map[string]Boundary
	calls      int
}

func (r *fakeResolver) ResolveBoundary(_ context.Context, conversationID, messageID string) (Boundary, error) {
	r.calls++
	item, ok := r.boundaries[messageID]
	if !ok || item.ConversationID != conversationID {
		return Boundary{}, errors.New("missing server boundary")
	}
	return item, nil
}

type fakeSender struct {
	calls   int
	request MarkReadRequest
	err     error
}

func (s *fakeSender) MarkRead(_ context.Context, request MarkReadRequest) error {
	s.calls++
	s.request = request
	return s.err
}

var _ BoundaryResolver = (*fakeResolver)(nil)
var _ Sender = (*fakeSender)(nil)
