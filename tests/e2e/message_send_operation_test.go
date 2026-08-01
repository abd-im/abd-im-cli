package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestMessageSendAllowlistAndIdempotencyE2E(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	sender := &e2eTextSender{}
	tool, credential := newMessageSendTool(t, store, sender, "run-confirmed")
	if response := callMessageSend(t, tool, credential, "outside", messagecapability.Input{Text: "outside", RecipientID: "user-outside"}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("outside recipient response/calls = %+v/%d", response, sender.calls)
	}

	input := messagecapability.Input{Text: "hello", RecipientID: "user-allowed"}
	first := callMessageSend(t, tool, credential, "confirmed", input)
	if !first.OK || sender.calls != 1 || sender.recipientID != "user-allowed" {
		t.Fatalf("first message.send_text response/sender = %+v/%+v", first, sender)
	}
	var firstResult struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(first.Data, &firstResult); err != nil || firstResult.OperationID == "" || firstResult.Status != string(control.OperationConfirmed) {
		t.Fatalf("first message.send_text data = %s, %v", first.Data, err)
	}
	second := callMessageSend(t, tool, credential, "confirmed", input)
	if !second.OK || sender.calls != 1 || string(second.Data) != string(first.Data) {
		t.Fatalf("same key response/calls = %+v/%d", second, sender.calls)
	}
	if response := callMessageSend(t, tool, credential, "confirmed", messagecapability.Input{Text: "different", RecipientID: "user-allowed"}); response.OK || response.Error == nil || response.Error.Code != contracts.CodeIdempotencyConflict || sender.calls != 1 {
		t.Fatalf("conflicting key response/calls = %+v/%d", response, sender.calls)
	}
	if response := callMessageSend(t, tool, credential, "group", messagecapability.Input{Text: "group", GroupID: "group-allowed"}); !response.OK || sender.calls != 2 || sender.groupID != "group-allowed" {
		t.Fatalf("group response/sender = %+v/%+v", response, sender)
	}
}

func TestUnknownMessageSendSurvivesRecoveryWithoutRetryE2E(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "control.db")
	store, err := control.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	input := messagecapability.Input{Text: "hello", RecipientID: "user-allowed"}
	firstSender := &e2eTextSender{err: operation.ErrOutcomeUnknown}
	tool, credential := newMessageSendTool(t, store, firstSender, "run-unknown")
	if response := callMessageSend(t, tool, credential, "unknown-first", input); response.OK || response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || firstSender.calls != 1 {
		t.Fatalf("first unknown response/calls = %+v/%d", response, firstSender.calls)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := control.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	secondSender := &e2eTextSender{}
	recoveredTool, recoveredCredential := newMessageSendTool(t, reopened, secondSender, "run-recovered")
	if response := callMessageSend(t, recoveredTool, recoveredCredential, "unknown-new-key", input); response.OK || response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || secondSender.calls != 0 {
		t.Fatalf("recovered unknown response/calls = %+v/%d", response, secondSender.calls)
	}
}

func newMessageSendTool(t *testing.T, store *control.Store, sender messagecapability.Sender, runID string) (*proxy.Proxy, string) {
	t.Helper()
	manifest, err := capability.New([]capability.Entry{{Method: messagecapability.Method, Scope: messagecapability.Scope, Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := messagecapability.New(manifest, guard, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{messagecapability.Method},
		Scopes:    []string{messagecapability.Scope},
		TargetAllowlists: map[string][]string{
			messagecapability.Method: {grant.UserTarget("user-allowed"), grant.GroupTarget("group-allowed")},
		},
		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 10,
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

func callMessageSend(t *testing.T, tool *proxy.Proxy, credential, idempotencyKey string, input messagecapability.Input) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      "request-" + idempotencyKey,
		ProfileID:      "work",
		Method:         messagecapability.Method,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		t.Fatalf("message.send_text Call() error = %v", err)
	}
	return response
}

type e2eTextSender struct {
	calls       int
	recipientID string
	groupID     string
	err         error
}

func (s *e2eTextSender) SendText(_ context.Context, _ string, recipientID, groupID string) error {
	s.calls++
	s.recipientID = recipientID
	s.groupID = groupID
	return s.err
}

var _ messagecapability.Sender = (*e2eTextSender)(nil)
