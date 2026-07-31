package control

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenMigratesIdempotently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.PutProfile(ctx, Profile{ID: "work"}); err != nil {
		t.Fatalf("PutProfile() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	profile, err := store.GetProfile(ctx, "work")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if profile.ID != "work" {
		t.Fatalf("ID = %q, want work", profile.ID)
	}

	var migrations int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&migrations); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrations != 3 {
		t.Fatalf("migration count = %d, want 3", migrations)
	}
}

func TestStorePersistsOnlyControlMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	if err := store.PutProfile(ctx, Profile{ID: "work", CreatedAt: now}); err != nil {
		t.Fatalf("PutProfile() error = %v", err)
	}
	if err := store.PutEvent(ctx, Event{
		ID:             "event-1",
		ProfileID:      "work",
		Sequence:       1,
		SDKDedupKey:    "sdk-callback-1",
		Type:           "message.received",
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		OccurredAt:     now,
	}); err != nil {
		t.Fatalf("PutEvent() error = %v", err)
	}
	if err := store.PutOperation(ctx, Operation{
		ID:             "operation-1",
		ProfileID:      "work",
		Scope:          "group.create",
		IdempotencyKey: "request-1",
		InputDigest:    "sha256:example",
		Status:         OperationUnknown,
		CreatedAt:      now,
	}); err != nil {
		t.Fatalf("PutOperation() error = %v", err)
	}
	expiresAt := now.Add(time.Hour)
	if err := store.PutGrant(ctx, Grant{
		ID:                  "grant-1",
		ProfileID:           "work",
		RunID:               "run-1",
		Principal:           "provider",
		Scopes:              []string{"message.history"},
		TargetAllowlist:     []string{"conversation-1"},
		MessageWindow:       MessageWindow{ConversationID: "conversation-1", AfterMessageID: "message-0", BeforeMessageID: "message-2"},
		AttachmentByteLimit: 1024,
		RateLimit:           10,
		ApprovalPolicy:      "none",
		ExpiresAt:           expiresAt,
	}); err != nil {
		t.Fatalf("PutGrant() error = %v", err)
	}

	event, err := store.EventByDedupKey(ctx, "work", "sdk-callback-1")
	if err != nil {
		t.Fatalf("EventByDedupKey() error = %v", err)
	}
	if event.MessageID != "message-1" || event.ConversationID != "conversation-1" {
		t.Fatalf("event = %+v, want message and conversation references", event)
	}

	operation, err := store.OperationByIdempotencyKey(ctx, "work", "group.create", "request-1")
	if err != nil {
		t.Fatalf("OperationByIdempotencyKey() error = %v", err)
	}
	if operation.Status != OperationUnknown {
		t.Fatalf("operation status = %q, want %q", operation.Status, OperationUnknown)
	}

	grant, err := store.Grant(ctx, "grant-1")
	if err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	if len(grant.Scopes) != 1 || grant.Scopes[0] != "message.history" {
		t.Fatalf("grant scopes = %#v", grant.Scopes)
	}
	if grant.MessageWindow.ConversationID != "conversation-1" {
		t.Fatalf("grant message window = %+v", grant.MessageWindow)
	}

	rows, err := store.db.QueryContext(ctx, "SELECT name, sql FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	defer rows.Close()
	allowedTables := map[string]bool{
		"schema_migrations": true,
		"profiles":          true,
		"events":            true,
		"operations":        true,
		"grants":            true,
		"reply_slots":       true,
	}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		if !allowedTables[name] {
			t.Fatalf("control schema has unexpected table %q", name)
		}
		for _, forbidden := range []string{"body", "content"} {
			if strings.Contains(strings.ToLower(definition), forbidden) {
				t.Fatalf("control schema stores message data %q: %s", forbidden, definition)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema: %v", err)
	}
}

func TestStoreValidatesModelsAndReportsMissingRecords(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.PutProfile(ctx, Profile{}); err == nil {
		t.Fatal("PutProfile() error = nil, want validation error")
	}
	if err := store.PutOperation(ctx, Operation{ID: "operation-1", ProfileID: "work", Scope: "group.create", IdempotencyKey: "key", InputDigest: "digest", Status: "pending"}); err == nil {
		t.Fatal("PutOperation() error = nil, want invalid status error")
	}
	if _, err := store.GetProfile(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProfile(missing) error = %v, want ErrNotFound", err)
	}

	if err := store.PutEvent(ctx, Event{ID: "event-1", ProfileID: "work", Sequence: 1, SDKDedupKey: "key", Type: "message.received"}); err != nil {
		t.Fatalf("first PutEvent() error = %v", err)
	}
	if err := store.PutEvent(ctx, Event{ID: "event-2", ProfileID: "work", Sequence: 2, SDKDedupKey: "key", Type: "message.received"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate PutEvent() error = %v, want ErrConflict", err)
	}
}
