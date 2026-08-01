package operation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/control"
)

func TestGuardStoresOnlyTargetAndRedactedFailureDiagnostics(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	guard, err := NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "token-and-message-body-must-not-persist"
	outcome, err := guard.Execute(context.Background(), Request{
		ID:             "operation-1",
		ProfileID:      "work",
		Scope:          "message.send_text",
		IdempotencyKey: "key-1",
		Input: struct {
			RecipientID string `json:"recipient_id"`
			Text        string `json:"text"`
		}{RecipientID: "user-2", Text: secret},
	}, func(context.Context) error { return errors.New(secret) })
	if err == nil || outcome.Operation.Status != control.OperationFailed {
		t.Fatalf("Execute() = %#v, %v", outcome, err)
	}
	stored, err := store.OperationByID(context.Background(), "work", "operation-1")
	if err != nil || stored.TargetSummary != "recipient:user-2" || stored.ErrorSummary != "remote action failed" {
		t.Fatalf("stored operation = %#v, %v", stored, err)
	}
	if strings.Contains(stored.TargetSummary, secret) || strings.Contains(stored.ErrorSummary, secret) {
		t.Fatalf("operation diagnostic leaked secret: %#v", stored)
	}
}
