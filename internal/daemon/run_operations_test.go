package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	operationsservice "github.com/abd-im/abd-im-cli/internal/service/operations"
)

func TestRunOperationOwnerMethodsUseTypedAndRedactedResponses(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.PutRun(context.Background(), control.Run{ID: "run-1", ProfileID: "work", ConversationID: "conversation-1", EventID: "event-1", Status: control.RunQueued}); err != nil {
		t.Fatal(err)
	}
	const secret = "operation-input-must-not-leak"
	if err := store.PutOperation(context.Background(), control.Operation{ID: "operation-1", ProfileID: "work", Scope: "message.send_text", IdempotencyKey: secret, InputDigest: secret, TargetSummary: "recipient:user-2", Status: control.OperationFailed, ErrorSummary: "remote action failed"}); err != nil {
		t.Fatal(err)
	}
	canceler := &ownerRunCanceler{}
	reader, err := operationsservice.New("work", store, canceler)
	if err != nil {
		t.Fatal(err)
	}
	methods, err := RunOperationOwnerMethods(reader)
	if err != nil || len(methods) != 4 {
		t.Fatalf("RunOperationOwnerMethods() = %d methods, %v", len(methods), err)
	}
	dispatcher, err := NewDispatcher("work", methods)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := dispatcher.Handle(context.Background(), ownerRequest(operationsservice.RunListMethod, `{"limit":1}`))
	if err != nil || !listed.OK || listed.Meta == nil || listed.Meta.ProfileID != "work" {
		t.Fatalf("run.list = %#v, %v", listed, err)
	}
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listed.Data, &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != "run-1" || page.Items[0].Status != string(control.RunQueued) {
		t.Fatalf("run.list data = %s, %v", listed.Data, err)
	}

	cancelled, err := dispatcher.Handle(context.Background(), ownerRequest(operationsservice.RunCancelMethod, `{"run_id":"run-1"}`))
	if err != nil || !cancelled.OK || canceler.runID != "run-1" {
		t.Fatalf("run.cancel = %#v, %v; canceler=%#v", cancelled, err, canceler)
	}

	diagnostic, err := dispatcher.Handle(context.Background(), ownerRequest(operationsservice.OperationGetMethod, `{"operation_id":"operation-1"}`))
	payload, marshalErr := json.Marshal(diagnostic)
	if err != nil || marshalErr != nil || !diagnostic.OK || strings.Contains(string(payload), secret) {
		t.Fatalf("operation.get = %s, handle/marshal = %v/%v", payload, err, marshalErr)
	}

	marked, err := dispatcher.Handle(context.Background(), ownerRequest(operationsservice.OperationMarkUnknownMethod, `{"operation_id":"operation-1"}`))
	if err != nil || !marked.OK {
		t.Fatalf("operation.mark_unknown = %#v, %v", marked, err)
	}

	invalid, err := dispatcher.Handle(context.Background(), ownerRequest(operationsservice.RunCancelMethod, `{}`))
	if err != nil || invalid.OK || invalid.Error == nil || invalid.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("invalid run.cancel = %#v, %v", invalid, err)
	}
}

type ownerRunCanceler struct{ runID string }

func (c *ownerRunCanceler) Cancel(runID string) bool {
	c.runID = runID
	return true
}
