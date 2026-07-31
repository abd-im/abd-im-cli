package reply

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/control"
)

func TestReplyUsesSingleEventBoundSlotAndNeverOverridesConversation(t *testing.T) {
	store, service, sender := newTestService(t)
	defer store.Close()
	binding := Binding{ProfileID: "work", EventID: "event-1", ConversationID: "conversation-original", TriggerMessageID: "message-trigger", RecipientID: "user-2", RunID: "run-1"}
	first, err := service.Reserve(context.Background(), binding)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	duplicate, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-1", ConversationID: "conversation-attacker", TriggerMessageID: "other", RecipientID: "attacker", RunID: "run-2"})
	if err != nil {
		t.Fatalf("duplicate Reserve() error = %v", err)
	}
	if duplicate.ID != first.ID || duplicate.ConversationID != "conversation-original" || duplicate.RunID != "run-1" {
		t.Fatalf("duplicate slot = %#v, want original immutable target", duplicate)
	}
	outcome, err := service.Deliver(context.Background(), "work", "event-1", "final response")
	if err != nil || outcome.Operation.Status != control.OperationConfirmed {
		t.Fatalf("Deliver() = %#v, %v", outcome, err)
	}
	if sender.calls != 1 || sender.delivery.ConversationID != "conversation-original" || sender.delivery.RecipientID != "user-2" || sender.delivery.Text != "final response" {
		t.Fatalf("sender delivery = %#v, calls = %d", sender.delivery, sender.calls)
	}
	if _, err := service.Deliver(context.Background(), "work", "event-1", "final response"); err != nil {
		t.Fatalf("idempotent Deliver() error = %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("idempotent Deliver() sent %d replies, want 1", sender.calls)
	}
	if _, err := service.Deliver(context.Background(), "work", "event-1", "different response"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting Deliver() error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestUnknownReplyOutcomeIsDurableAndNeverResent(t *testing.T) {
	store, service, sender := newTestService(t)
	defer store.Close()
	sender.err = ErrOutcomeUnknown
	if _, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-unknown", ConversationID: "conversation-1", TriggerMessageID: "message-1", RecipientID: "user-2", RunID: "run-1"}); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	first, err := service.Deliver(context.Background(), "work", "event-unknown", "final response")
	if !errors.Is(err, ErrOutcomeUnknown) || first.Operation.Status != control.OperationUnknown {
		t.Fatalf("first Deliver() = %#v, %v", first, err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.calls)
	}
	sender.err = nil
	second, err := service.Deliver(context.Background(), "work", "event-unknown", "final response")
	if err != nil || second.Operation.Status != control.OperationUnknown {
		t.Fatalf("second Deliver() = %#v, %v", second, err)
	}
	if sender.calls != 1 {
		t.Fatalf("unknown outcome was resent: calls = %d", sender.calls)
	}
}

type fakeSender struct {
	calls    int
	delivery Delivery
	err      error
}

func (s *fakeSender) Reply(_ context.Context, delivery Delivery) error {
	s.calls++
	s.delivery = delivery
	return s.err
}

func newTestService(t *testing.T) (*control.Store, *Service, *fakeSender) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sender := &fakeSender{}
	service, err := New(store, sender)
	if err != nil {
		_ = store.Close()
		t.Fatalf("New() error = %v", err)
	}
	return store, service, sender
}
