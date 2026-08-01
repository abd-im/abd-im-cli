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
	if migrations != 5 {
		t.Fatalf("migration count = %d, want 5", migrations)
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
		TargetAllowlists:    map[string][]string{"message.history": {"conversation:conversation-1"}},
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
	if err := store.PutRun(ctx, Run{ID: "run-1", ProfileID: "work", ConversationID: "conversation-1", EventID: "event-1", Status: RunQueued, CreatedAt: now}); err != nil {
		t.Fatalf("PutRun() error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, "work", "run-1", RunRunning, ""); err != nil {
		t.Fatalf("UpdateRunStatus() error = %v", err)
	}
	storedRun, err := store.RunByID(ctx, "work", "run-1")
	if err != nil || storedRun.Status != RunRunning {
		t.Fatalf("RunByID() = %#v, %v", storedRun, err)
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
		"attachments":       true,
		"runs":              true,
	}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		if !allowedTables[name] {
			t.Fatalf("control schema has unexpected table %q", name)
		}
		for _, forbidden := range []string{"body", "content", "path"} {
			if strings.Contains(strings.ToLower(definition), forbidden) {
				t.Fatalf("control schema stores message data %q: %s", forbidden, definition)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema: %v", err)
	}
}

func TestAttachmentMetadataIsRunScopedAndQuotaBound(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	expiresAt := time.Now().Add(time.Hour)
	first := Attachment{ID: "attachment-1", ProfileID: "work", RunID: "run-1", GrantID: "grant-1", Kind: "image", SizeBytes: 7, ByteLimit: 10, ExpiresAt: expiresAt}
	if err := store.PutAttachment(ctx, first); err != nil {
		t.Fatalf("PutAttachment(first) error = %v", err)
	}
	stored, err := store.AttachmentByID(ctx, "work", first.ID)
	if err != nil {
		t.Fatalf("AttachmentByID() error = %v", err)
	}
	if stored.RunID != "run-1" || stored.GrantID != "grant-1" || stored.Kind != "image" || stored.SizeBytes != 7 || stored.ByteLimit != 10 || stored.CreatedAt.IsZero() {
		t.Fatalf("stored attachment = %+v", stored)
	}
	second := Attachment{ID: "attachment-2", ProfileID: "work", RunID: "run-1", GrantID: "grant-1", Kind: "file", SizeBytes: 4, ByteLimit: 10, ExpiresAt: expiresAt}
	if err := store.PutAttachment(ctx, second); !errors.Is(err, ErrAttachmentQuota) {
		t.Fatalf("PutAttachment(over quota) error = %v, want ErrAttachmentQuota", err)
	}
	changedLimit := Attachment{ID: "attachment-3", ProfileID: "work", RunID: "run-1", GrantID: "grant-1", Kind: "file", SizeBytes: 1, ByteLimit: 100, ExpiresAt: expiresAt}
	if err := store.PutAttachment(ctx, changedLimit); !errors.Is(err, ErrConflict) {
		t.Fatalf("PutAttachment(changed limit) error = %v, want ErrConflict", err)
	}
	if _, err := store.AttachmentByID(ctx, "other", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AttachmentByID(other profile) error = %v, want ErrNotFound", err)
	}
	if err := store.PutAttachment(ctx, Attachment{ID: "../secret", ProfileID: "work", RunID: "run-2", GrantID: "grant-2", Kind: "file", ByteLimit: 10, ExpiresAt: expiresAt}); err == nil {
		t.Fatal("PutAttachment() accepted a path-like ID")
	}
}

func TestGrantReadsLegacyTargetArray(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	if err := store.PutGrant(ctx, Grant{
		ID:               "grant-legacy",
		ProfileID:        "work",
		RunID:            "run-legacy",
		Principal:        "provider",
		Scopes:           []string{"message.read"},
		TargetAllowlists: map[string][]string{"message.history": {"conversation:conversation-1"}},
		ApprovalPolicy:   "none",
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE grants SET target_allowlist = ? WHERE id = ?`, `["conversation-1"]`, "grant-legacy"); err != nil {
		t.Fatal(err)
	}
	grant, err := store.Grant(ctx, "grant-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got := grant.TargetAllowlists["legacy"]; len(got) != 1 || got[0] != "conversation-1" {
		t.Fatalf("legacy targets = %#v", grant.TargetAllowlists)
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
