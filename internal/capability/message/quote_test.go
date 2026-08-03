package message

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestSendQuoteRequiresManifestTypedTargetAndMessageWindow(t *testing.T) {
	manifest, err := capability.New([]capability.Entry{{Method: QuoteMethod, Scope: QuoteScope, Status: capability.Gated}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeQuoteSource{references: []QuoteReference{
		{ID: "message-0", ConversationID: "conversation-1"},
		{ID: "message-1", ConversationID: "conversation-1"},
		{ID: "message-2", ConversationID: "conversation-1"},
		{ID: "message-3", ConversationID: "conversation-1"},
	}}
	sender := &fakeQuoteSender{}
	handler, err := NewQuote(manifest, guard, source, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{QuoteMethod},
		Scopes:    []string{QuoteScope},
		TargetAllowlists: map[string][]string{
			QuoteMethod: {
				grant.UserTarget("user-1"),
				grant.GroupTarget("group-1"),
				grant.ConversationTarget("conversation-1"),
				grant.MessageTarget("message-1"),
				grant.MessageTarget("message-2"),
			},
		},
		MessageWindow: grant.MessageWindow{
			ConversationID:  "conversation-1",
			AfterMessageID:  "message-0",
			BeforeMessageID: "message-3",
		},
		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	call := func(key string, input QuoteInput) contracts.Response {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, err := tool.Call(context.Background(), contracts.Request{
			APIVersion:     contracts.APIVersionV1,
			RequestID:      key,
			ProfileID:      "work",
			Method:         QuoteMethod,
			Params:         raw,
			Grant:          credential,
			IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	allowed := QuoteInput{Text: "quote", RecipientID: "user-1", ConversationID: "conversation-1", MessageID: "message-1"}
	if response := call("gated", allowed); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("gated quote = %+v, calls=%d", response, sender.calls)
	}

	manifest, err = capability.New([]capability.Entry{{Method: QuoteMethod, Scope: QuoteScope, Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err = NewQuote(manifest, guard, source, sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, err = proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	if response := call("outside-target", QuoteInput{Text: "quote", RecipientID: "user-2", ConversationID: "conversation-1", MessageID: "message-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("outside target quote = %+v, calls=%d", response, sender.calls)
	}
	if response := call("before-window", QuoteInput{Text: "quote", RecipientID: "user-1", ConversationID: "conversation-1", MessageID: "message-0"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("before window quote = %+v, calls=%d", response, sender.calls)
	}
	if response := call("wrong-conversation", QuoteInput{Text: "quote", GroupID: "group-1", ConversationID: "conversation-2", MessageID: "message-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("wrong conversation quote = %+v, calls=%d", response, sender.calls)
	}
	if response := call("allowed-user", allowed); !response.OK || sender.calls != 1 || sender.input != allowed {
		t.Fatalf("allowed user quote = %+v, sender=%+v", response, sender)
	}
	group := QuoteInput{Text: "group quote", GroupID: "group-1", ConversationID: "conversation-1", MessageID: "message-2"}
	if response := call("allowed-group", group); !response.OK || sender.calls != 2 || sender.input != group {
		t.Fatalf("allowed group quote = %+v, sender=%+v", response, sender)
	}
	_, fullCredential, err := grants.Issue(grant.Policy{
		RunID: "run-full", ProfileID: "work", Principal: "owner", FullAccess: true,
		Methods: []string{QuoteMethod}, Scopes: []string{QuoteScope},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	fullTool, err := proxy.New(grants, "run-full", "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	tool, credential = fullTool, fullCredential
	full := QuoteInput{Text: "full quote", RecipientID: "user-2", ConversationID: "conversation-1", MessageID: "message-0"}
	if response := call("full-access", full); !response.OK || sender.calls != 3 || sender.input != full {
		t.Fatalf("full access quote = %+v, sender=%+v", response, sender)
	}
}

func TestSendQuoteIdempotencyAndUnknownOutcomeFailClosed(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := capability.New([]capability.Entry{{Method: QuoteMethod, Scope: QuoteScope, Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeQuoteSource{references: []QuoteReference{{ID: "message-1", ConversationID: "conversation-1"}}}
	sender := &fakeQuoteSender{}
	handler, err := NewQuote(manifest, guard, source, sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := newQuoteTool(t, handler, "run-1")
	input := QuoteInput{Text: "quote", RecipientID: "user-1", ConversationID: "conversation-1", MessageID: "message-1"}
	first := callQuote(t, tool, credential, "same-key", input)
	if !first.OK || sender.calls != 1 {
		t.Fatalf("first quote = %+v, calls=%d", first, sender.calls)
	}
	source.err = errors.New("source is temporarily unavailable")
	if second := callQuote(t, tool, credential, "same-key", input); !second.OK || sender.calls != 1 || string(second.Data) != string(first.Data) {
		t.Fatalf("repeat quote = %+v, calls=%d", second, sender.calls)
	}
	source.err = nil
	if conflict := callQuote(t, tool, credential, "same-key", QuoteInput{Text: "changed", RecipientID: "user-1", ConversationID: "conversation-1", MessageID: "message-1"}); conflict.Error == nil || conflict.Error.Code != contracts.CodeIdempotencyConflict || sender.calls != 1 {
		t.Fatalf("conflicting quote = %+v, calls=%d", conflict, sender.calls)
	}

	unknownSender := &fakeQuoteSender{err: operation.ErrOutcomeUnknown}
	unknownHandler, err := NewQuote(manifest, guard, source, unknownSender)
	if err != nil {
		t.Fatal(err)
	}
	unknownTool, unknownCredential := newQuoteTool(t, unknownHandler, "run-2")
	if response := callQuote(t, unknownTool, unknownCredential, "unknown-first", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || unknownSender.calls != 1 {
		t.Fatalf("first unknown quote = %+v, calls=%d", response, unknownSender.calls)
	}
	if response := callQuote(t, unknownTool, unknownCredential, "unknown-new-key", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || unknownSender.calls != 1 {
		t.Fatalf("unknown quote rebuilt = %+v, calls=%d", response, unknownSender.calls)
	}
}

func TestSendQuoteFailsClosedWhenSourceReturnsWrongConversation(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := capability.New([]capability.Entry{{Method: QuoteMethod, Scope: QuoteScope, Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewQuote(manifest, guard, &fakeQuoteSource{references: []QuoteReference{{ID: "message-1", ConversationID: "conversation-other"}}}, &fakeQuoteSender{})
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := newQuoteTool(t, handler, "run-1")
	response := callQuote(t, tool, credential, "invalid-source", QuoteInput{Text: "quote", RecipientID: "user-1", ConversationID: "conversation-1", MessageID: "message-1"})
	if response.Error == nil || response.Error.Code != contracts.CodeSDKError {
		t.Fatalf("invalid source quote = %+v", response)
	}
}

func newQuoteTool(t *testing.T, handler *QuoteHandler, runID string) (*proxy.Proxy, string) {
	t.Helper()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{QuoteMethod},
		Scopes:    []string{QuoteScope},
		TargetAllowlists: map[string][]string{
			QuoteMethod: {
				grant.UserTarget("user-1"),
				grant.ConversationTarget("conversation-1"),
				grant.MessageTarget("message-1"),
			},
		},
		MessageWindow: grant.MessageWindow{ConversationID: "conversation-1"},
		ExpiresAt:     time.Now().Add(time.Hour),
		RateBudget:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, runID, "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func callQuote(t *testing.T, tool *proxy.Proxy, credential, key string, input QuoteInput) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      key,
		ProfileID:      "work",
		Method:         QuoteMethod,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type fakeQuoteSource struct {
	references []QuoteReference
	err        error
}

func (s *fakeQuoteSource) History(_ context.Context, _ string) ([]QuoteReference, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]QuoteReference(nil), s.references...), nil
}

type fakeQuoteSender struct {
	calls int
	input QuoteInput
	err   error
}

func (s *fakeQuoteSender) SendQuote(_ context.Context, input QuoteInput) error {
	s.calls++
	s.input = input
	return s.err
}

var _ QuoteSource = (*fakeQuoteSource)(nil)
var _ QuoteSender = (*fakeQuoteSender)(nil)
