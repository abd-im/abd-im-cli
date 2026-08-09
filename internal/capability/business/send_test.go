package business

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestSendMessageUsesStableOperationID(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	handler, err := New(guard, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "business:connection-1",
		Methods: []string{SendMessageMethod}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(SendMessageInput{
		BusinessConnectionID: "connection-1", ConversationID: "conversation-1",
		TriggerMessageID: "message-1", Text: "reply",
	})
	call := func() contracts.Response {
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion: contracts.APIVersionV1, RequestID: "request-1", ProfileID: "work",
			Method: SendMessageMethod, Params: params, Grant: credential, IdempotencyKey: "reply-1",
		})
		return response
	}
	if response := call(); !response.OK || sender.calls != 1 || sender.operationID != "business-send-reply-1" {
		t.Fatalf("first response = %#v, sender = %#v", response, sender)
	}
	if response := call(); !response.OK || sender.calls != 1 {
		t.Fatalf("duplicate response = %#v, calls = %d", response, sender.calls)
	}
}

type fakeSender struct {
	calls       int
	operationID string
}

func (s *fakeSender) SendBusinessText(_ context.Context, _, _, _, _, operationID string) error {
	s.calls++
	s.operationID = operationID
	return nil
}
