package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestQuoteRequiresSourceMessageWindowE2E(t *testing.T) {
	store := actionStore(t)

	guard, _ := operation.NewGuard(store)
	sender := &e2eQuoteSender{}
	handler, err := messagecapability.NewQuote(guard, e2eQuoteSource{{ID: "message-1", ConversationID: "si_user-1_user-2"}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := actionTool(t, messagecapability.QuoteMethod, grant.MessageWindow{ConversationID: "si_user-1_user-2"}, handler.ProxyMethod())
	input := messagecapability.QuoteInput{Text: "reply", RecipientID: "user-2", ConversationID: "si_user-1_user-2", MessageID: "message-1"}
	if response := actionCall(t, tool, credential, messagecapability.QuoteMethod, "quote-1", input); !response.OK || sender.calls != 1 {
		t.Fatalf("authorized quote = %+v, calls=%d", response, sender.calls)
	}
	input.MessageID = "message-2"
	if response := actionCall(t, tool, credential, messagecapability.QuoteMethod, "quote-2", input); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 1 {
		t.Fatalf("quoted message outside window = %+v, calls=%d", response, sender.calls)
	}
}

func TestAtSendsTypedMentionsE2E(t *testing.T) {
	store := actionStore(t)

	guard, _ := operation.NewGuard(store)
	sender := &e2eAtSender{}
	handler, err := messagecapability.NewAt(guard, sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := actionTool(t, messagecapability.AtMethod, grant.MessageWindow{}, handler.ProxyMethod())
	input := messagecapability.AtInput{Text: "attention", GroupID: "group-1", MentionUserIDs: []string{"user-2", "user-3"}}
	if response := actionCall(t, tool, credential, messagecapability.AtMethod, "at-allowed", input); !response.OK || sender.calls != 1 {
		t.Fatalf("valid mention = %+v, calls=%d", response, sender.calls)
	}
}

func TestMarkReadRequiresResolvedFiniteMessageWindowE2E(t *testing.T) {
	store := actionStore(t)

	guard, _ := operation.NewGuard(store)
	resolver := e2eBoundaryResolver{
		"after":  {ConversationID: "si_user-1_user-2", MessageID: "after", ServerSeq: 10},
		"inside": {ConversationID: "si_user-1_user-2", MessageID: "inside", ServerSeq: 11},
		"before": {ConversationID: "si_user-1_user-2", MessageID: "before", ServerSeq: 12},
	}
	sender := &e2eMarkReadSender{}
	handler, err := conversationcapability.New(guard, resolver, sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := actionTool(t, conversationcapability.Method, grant.MessageWindow{ConversationID: "si_user-1_user-2", AfterMessageID: "after", BeforeMessageID: "before"}, handler.ProxyMethod())
	input := conversationcapability.Input{ConversationID: "si_user-1_user-2", UpToMessageID: "inside"}
	if response := actionCall(t, tool, credential, conversationcapability.Method, "read-1", input); !response.OK || sender.calls != 1 || sender.input.HasReadSeq != 11 {
		t.Fatalf("authorized mark read = %+v, sender=%+v", response, sender)
	}
	input.UpToMessageID = "before"
	if response := actionCall(t, tool, credential, conversationcapability.Method, "read-2", input); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 1 {
		t.Fatalf("out of window mark read = %+v, sender=%+v", response, sender)
	}
}

func actionStore(t *testing.T) *control.Store {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func actionTool(t *testing.T, method string, window grant.MessageWindow, registered proxy.Method) (*proxy.Proxy, string) {
	t.Helper()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{method},

		MessageWindow: window,
		ExpiresAt:     time.Now().Add(time.Hour),
		RateBudget:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-1", "work", []proxy.Method{registered})
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func actionCall(t *testing.T, tool *proxy.Proxy, credential, method, key string, input any) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: method,
		Params: params, Grant: credential, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type e2eQuoteSource []messagecapability.QuoteReference

func (s e2eQuoteSource) History(context.Context, string) ([]messagecapability.QuoteReference, error) {
	return append([]messagecapability.QuoteReference(nil), s...), nil
}

type e2eQuoteSender struct{ calls int }

func (s *e2eQuoteSender) SendQuote(context.Context, messagecapability.QuoteInput) error {
	s.calls++
	return nil
}

type e2eAtSender struct{ calls int }

func (s *e2eAtSender) SendAt(context.Context, string, string, []string) error { s.calls++; return nil }

type e2eBoundaryResolver map[string]conversationcapability.Boundary

func (r e2eBoundaryResolver) ResolveBoundary(_ context.Context, conversationID, messageID string) (conversationcapability.Boundary, error) {
	value, ok := r[messageID]
	if !ok || value.ConversationID != conversationID {
		return conversationcapability.Boundary{}, errors.New("boundary not found")
	}
	return value, nil
}

type e2eMarkReadSender struct {
	calls int
	input conversationcapability.MarkReadRequest
}

func (s *e2eMarkReadSender) MarkRead(_ context.Context, input conversationcapability.MarkReadRequest) error {
	s.calls++
	s.input = input
	return nil
}

var _ messagecapability.QuoteSource = e2eQuoteSource{}
var _ messagecapability.QuoteSender = (*e2eQuoteSender)(nil)
var _ messagecapability.AtSender = (*e2eAtSender)(nil)
var _ conversationcapability.BoundaryResolver = e2eBoundaryResolver{}
var _ conversationcapability.Sender = (*e2eMarkReadSender)(nil)
