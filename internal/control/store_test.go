package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsEventsAndProviderSessions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.PutProfile(ctx, Profile{ID: "work"}); err != nil {
		t.Fatal(err)
	}
	event := Event{ID: "event-1", ProfileID: "work", Sequence: 1, SDKDedupKey: "dedup-1", Type: "message.received", ConversationID: "conversation-1", MessageID: "message-1", OccurredAt: time.Now()}
	if err := store.PutEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEvent(ctx, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate PutEvent() error = %v", err)
	}
	if got, err := store.EventByDedupKey(ctx, "work", "dedup-1"); err != nil || got.ID != event.ID {
		t.Fatalf("EventByDedupKey() = %#v, %v", got, err)
	}
	if err := store.SaveSessionRef(ctx, "work", "user:conversation-1", "codex", "session-1"); err != nil {
		t.Fatal(err)
	}
	if ref, found, err := store.LoadSessionRef(ctx, "work", "user:conversation-1", "codex"); err != nil || !found || ref != "session-1" {
		t.Fatalf("LoadSessionRef() = %q, %t, %v", ref, found, err)
	}
}
