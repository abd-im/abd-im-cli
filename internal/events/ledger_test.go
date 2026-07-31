package events

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
)

func TestLedgerDeduplicatesCallbackAndKeepsOnlyReferences(t *testing.T) {
	ledger, cleanup := newTestLedger(t)
	defer cleanup()
	callback := Callback{ProfileID: "work", DedupKey: "sdk-1", Type: string(contracts.EventMessageReceived), ConversationID: "conversation-1", MessageID: "message-1", OccurredAt: time.Now()}
	first, err := ledger.RecordCallback(context.Background(), callback)
	if err != nil {
		t.Fatalf("first RecordCallback() error = %v", err)
	}
	second, err := ledger.RecordCallback(context.Background(), callback)
	if err != nil {
		t.Fatalf("second RecordCallback() error = %v", err)
	}
	if !first.Created || second.Created || first.Event.EventID != second.Event.EventID {
		t.Fatalf("records = %#v, %#v; want one durable event", first, second)
	}
	batch, err := ledger.List(context.Background(), "work", "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(batch.Events) != 1 || batch.Events[0].Sequence != 1 || batch.Events[0].Type != string(contracts.EventMessageReceived) {
		t.Fatalf("List() = %#v", batch)
	}
	if got := string(batch.Events[0].Data); got != `{"conversation_id":"conversation-1","message_id":"message-1"}` {
		t.Fatalf("event data = %q, want references only", got)
	}
}

func TestReconcileDoesNotFabricateMessageReceivedAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := control.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ledger, err := NewLedger(store)
	if err != nil {
		t.Fatalf("NewLedger() error = %v", err)
	}
	if _, err := ledger.RecordCallback(context.Background(), Callback{ProfileID: "work", DedupKey: "sdk-before-restart", Type: string(contracts.EventMessageReceived)}); err != nil {
		t.Fatalf("RecordCallback() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = control.Open(path)
	if err != nil {
		t.Fatalf("reopen control store: %v", err)
	}
	defer store.Close()
	ledger, err = NewLedger(store)
	if err != nil {
		t.Fatalf("NewLedger(restarted) error = %v", err)
	}
	reconciled, err := ledger.Reconcile(context.Background(), "work")
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if reconciled.Event.Type != string(contracts.EventStateReconciled) {
		t.Fatalf("Reconcile event type = %q", reconciled.Event.Type)
	}
	batch, err := ledger.List(context.Background(), "work", "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(batch.Events) != 2 || batch.Events[0].Type != string(contracts.EventMessageReceived) || batch.Events[1].Type != string(contracts.EventStateReconciled) {
		t.Fatalf("restart events = %#v", batch.Events)
	}
}

func TestWatchUsesCursorAndCanRejectMalformedCursor(t *testing.T) {
	ledger, cleanup := newTestLedger(t)
	defer cleanup()
	if _, err := ledger.Watch(context.Background(), "work", "bad"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Watch(bad) error = %v, want ErrInvalidCursor", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch, err := ledger.Watch(ctx, "work", "")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if _, err := ledger.RecordCallback(context.Background(), Callback{ProfileID: "work", DedupKey: "sdk-1", Type: string(contracts.EventMessageReceived)}); err != nil {
		t.Fatalf("RecordCallback() error = %v", err)
	}
	select {
	case batch := <-watch:
		if len(batch.Events) != 1 || batch.NextCursor != "v1:1" {
			t.Fatalf("watch batch = %#v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not receive event")
	}
}

func newTestLedger(t *testing.T) (*Ledger, func()) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ledger, err := NewLedger(store)
	if err != nil {
		_ = store.Close()
		t.Fatalf("NewLedger() error = %v", err)
	}
	return ledger, func() { _ = store.Close() }
}
