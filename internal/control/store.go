// Package control persists daemon-owned control-plane metadata.
package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound reports that a control-plane record does not exist.
	ErrNotFound = errors.New("control record not found")
	// ErrConflict reports a duplicate control-plane identity.
	ErrConflict = errors.New("control record conflict")
)

// Store owns the SQLite control database for one daemon.
type Store struct {
	db *sql.DB
}

// Open opens path and applies all pending schema migrations. It does not open an
// SDK, connect an account, or access any provider.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("control database path is required")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open control database: %w", err)
	}
	// A daemon is the database's sole owner. Keeping one connection also makes
	// SQLite in-memory databases behave consistently in tests.
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the control database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Migrate applies each control schema migration once. It is safe to call again.
func (s *Store) Migrate(ctx context.Context) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin control migration: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	for _, migration := range migrations {
		var applied bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", migration.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if applied {
			continue
		}
		for _, statement := range migration.statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration %d: %w", migration.version, err)
			}
		}
		if _, err = tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			migration.version, encodeTime(time.Now())); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit control migration: %w", err)
	}
	return nil
}

// PutProfile creates or updates a profile's control metadata.
func (s *Store) PutProfile(ctx context.Context, profile Profile) error {
	if strings.TrimSpace(profile.ID) == "" {
		return errors.New("profile ID is required")
	}

	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO profiles (id, created_at, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			updated_at = excluded.updated_at`,
		profile.ID, encodeTime(profile.CreatedAt), encodeTime(profile.UpdatedAt))
	if err != nil {
		return fmt.Errorf("put profile: %w", err)
	}
	return nil
}

// GetProfile returns a profile's control metadata.
func (s *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, created_at, updated_at
		FROM profiles WHERE id = ?`, id)
	profile, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("%w: profile %q", ErrNotFound, id)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	return profile, nil
}

// PutEvent records an inbound event identity and references, never message text.
func (s *Store) PutEvent(ctx context.Context, event Event) error {
	if err := event.validate(); err != nil {
		return err
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (
			id, profile_id, sequence, sdk_dedup_key, type,
			conversation_id, message_id, occurred_at, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.ProfileID, event.Sequence, event.SDKDedupKey, event.Type,
		event.ConversationID, event.MessageID, encodeTime(event.OccurredAt), encodeTime(event.RecordedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: event %q", ErrConflict, event.ID)
	}
	if err != nil {
		return fmt.Errorf("put event: %w", err)
	}
	return nil
}

// EventByDedupKey returns an event using the SDK callback deduplication key.
func (s *Store) EventByDedupKey(ctx context.Context, profileID, dedupKey string) (Event, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, sequence, sdk_dedup_key, type,
			conversation_id, message_id, occurred_at, recorded_at
		FROM events WHERE profile_id = ? AND sdk_dedup_key = ?`, profileID, dedupKey)
	event, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, fmt.Errorf("%w: event dedup key", ErrNotFound)
	}
	if err != nil {
		return Event{}, fmt.Errorf("get event by dedup key: %w", err)
	}
	return event, nil
}

// NextEventSequence returns the next profile-local event sequence. Profile
// locking serializes writers, while the unique profile/sequence index remains
// a final durability guard.
func (s *Store) NextEventSequence(ctx context.Context, profileID string) (uint64, error) {
	if strings.TrimSpace(profileID) == "" {
		return 0, errors.New("profile ID is required")
	}
	var sequence uint64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE profile_id = ?`, profileID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("next event sequence: %w", err)
	}
	return sequence, nil
}

// ListEvents returns persisted event references after a profile-local cursor.
// It intentionally has no message-body columns or joins into the SDK store.
func (s *Store) ListEvents(ctx context.Context, profileID string, after uint64, limit int) ([]Event, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	if limit <= 0 || limit > 100 {
		return nil, errors.New("event limit must be between 1 and 100")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile_id, sequence, sdk_dedup_key, type,
			conversation_id, message_id, occurred_at, recorded_at
		FROM events WHERE profile_id = ? AND sequence > ?
		ORDER BY sequence ASC LIMIT ?`, profileID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// PutOperation records a remote side effect's idempotency identity and outcome.
func (s *Store) PutOperation(ctx context.Context, operation Operation) error {
	if err := operation.validate(); err != nil {
		return err
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = time.Now()
	}
	if operation.UpdatedAt.IsZero() {
		operation.UpdatedAt = operation.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO operations (
			id, profile_id, scope, idempotency_key, input_digest, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID, operation.ProfileID, operation.Scope, operation.IdempotencyKey,
		operation.InputDigest, operation.Status, encodeTime(operation.CreatedAt), encodeTime(operation.UpdatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: operation %q", ErrConflict, operation.ID)
	}
	if err != nil {
		return fmt.Errorf("put operation: %w", err)
	}
	return nil
}

// OperationByIdempotencyKey returns the operation that owns a side-effect key.
func (s *Store) OperationByIdempotencyKey(ctx context.Context, profileID, scope, key string) (Operation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, scope, idempotency_key, input_digest, status, created_at, updated_at
		FROM operations
		WHERE profile_id = ? AND scope = ? AND idempotency_key = ?`, profileID, scope, key)
	operation, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, fmt.Errorf("%w: operation idempotency key", ErrNotFound)
	}
	if err != nil {
		return Operation{}, fmt.Errorf("get operation by idempotency key: %w", err)
	}
	return operation, nil
}

// UnknownOperationByInputDigest finds a prior uncertain side effect with the
// same canonical input. Callers must not retry it under a different key.
func (s *Store) UnknownOperationByInputDigest(ctx context.Context, profileID, scope, digest string) (Operation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, scope, idempotency_key, input_digest, status, created_at, updated_at
		FROM operations WHERE profile_id = ? AND scope = ? AND input_digest = ? AND status = 'unknown'
		ORDER BY created_at ASC LIMIT 1`, profileID, scope, digest)
	operation, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, fmt.Errorf("%w: unknown operation input", ErrNotFound)
	}
	if err != nil {
		return Operation{}, fmt.Errorf("get unknown operation by input: %w", err)
	}
	return operation, nil
}

// UpdateOperationStatus records a terminal or unknown operation outcome.
func (s *Store) UpdateOperationStatus(ctx context.Context, id string, status OperationStatus) error {
	switch status {
	case OperationConfirmed, OperationFailed, OperationUnknown:
	default:
		return errors.New("operation status must be confirmed, failed, or unknown")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE operations SET status = ?, updated_at = ? WHERE id = ?`, status, encodeTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update operation status: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check operation update: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: operation %q", ErrNotFound, id)
	}
	return nil
}

// PutReplySlot creates the immutable reply target for one inbound event.
func (s *Store) PutReplySlot(ctx context.Context, slot ReplySlot) error {
	if err := slot.validate(); err != nil {
		return err
	}
	if slot.CreatedAt.IsZero() {
		slot.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reply_slots (
			id, profile_id, event_id, conversation_id, trigger_message_id,
			recipient_id, group_id, run_id, operation_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		slot.ID, slot.ProfileID, slot.EventID, slot.ConversationID, slot.TriggerMessageID,
		slot.RecipientID, slot.GroupID, slot.RunID, slot.OperationID, encodeTime(slot.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: reply slot %q", ErrConflict, slot.ID)
	}
	if err != nil {
		return fmt.Errorf("put reply slot: %w", err)
	}
	return nil
}

// ReplySlotByEvent returns the sole reply target for an event.
func (s *Store) ReplySlotByEvent(ctx context.Context, profileID, eventID string) (ReplySlot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, event_id, conversation_id, trigger_message_id,
			recipient_id, group_id, run_id, operation_id, created_at
		FROM reply_slots WHERE profile_id = ? AND event_id = ?`, profileID, eventID)
	slot, err := scanReplySlot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ReplySlot{}, fmt.Errorf("%w: reply slot for event %q", ErrNotFound, eventID)
	}
	if err != nil {
		return ReplySlot{}, fmt.Errorf("get reply slot: %w", err)
	}
	return slot, nil
}

// PutGrant records the constraints issued to one provider run.
func (s *Store) PutGrant(ctx context.Context, grant Grant) error {
	if err := grant.validate(); err != nil {
		return err
	}
	scopes, err := json.Marshal(grant.Scopes)
	if err != nil {
		return fmt.Errorf("encode grant scopes: %w", err)
	}
	targets, err := json.Marshal(grant.TargetAllowlists)
	if err != nil {
		return fmt.Errorf("encode grant targets: %w", err)
	}
	window, err := json.Marshal(grant.MessageWindow)
	if err != nil {
		return fmt.Errorf("encode grant message window: %w", err)
	}
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO grants (
			id, profile_id, run_id, principal, scopes, target_allowlist, message_window,
			attachment_byte_limit, rate_limit, approval_policy, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		grant.ID, grant.ProfileID, grant.RunID, grant.Principal, string(scopes), string(targets), string(window),
		grant.AttachmentByteLimit, grant.RateLimit, grant.ApprovalPolicy, encodeTime(grant.ExpiresAt), encodeTime(grant.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: grant %q", ErrConflict, grant.ID)
	}
	if err != nil {
		return fmt.Errorf("put grant: %w", err)
	}
	return nil
}

// Grant returns a persisted provider grant.
func (s *Store) Grant(ctx context.Context, id string) (Grant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, run_id, principal, scopes, target_allowlist, message_window,
			attachment_byte_limit, rate_limit, approval_policy, expires_at, created_at
		FROM grants WHERE id = ?`, id)
	grant, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, fmt.Errorf("%w: grant %q", ErrNotFound, id)
	}
	if err != nil {
		return Grant{}, fmt.Errorf("get grant: %w", err)
	}
	return grant, nil
}

func encodeTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func decodeTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

type scanner interface {
	Scan(...any) error
}

func scanProfile(row scanner) (Profile, error) {
	var profile Profile
	var createdAt, updatedAt string
	if err := row.Scan(&profile.ID, &createdAt, &updatedAt); err != nil {
		return Profile{}, err
	}
	var err error
	if profile.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Profile{}, err
	}
	if profile.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func scanEvent(row scanner) (Event, error) {
	var event Event
	var occurredAt, recordedAt string
	if err := row.Scan(
		&event.ID, &event.ProfileID, &event.Sequence, &event.SDKDedupKey, &event.Type,
		&event.ConversationID, &event.MessageID, &occurredAt, &recordedAt,
	); err != nil {
		return Event{}, err
	}
	var err error
	if event.OccurredAt, err = decodeTime(occurredAt); err != nil {
		return Event{}, err
	}
	if event.RecordedAt, err = decodeTime(recordedAt); err != nil {
		return Event{}, err
	}
	return event, nil
}

func scanOperation(row scanner) (Operation, error) {
	var operation Operation
	var createdAt, updatedAt string
	if err := row.Scan(
		&operation.ID, &operation.ProfileID, &operation.Scope, &operation.IdempotencyKey,
		&operation.InputDigest, &operation.Status, &createdAt, &updatedAt,
	); err != nil {
		return Operation{}, err
	}
	var err error
	if operation.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Operation{}, err
	}
	if operation.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func scanReplySlot(row scanner) (ReplySlot, error) {
	var slot ReplySlot
	var createdAt string
	if err := row.Scan(
		&slot.ID, &slot.ProfileID, &slot.EventID, &slot.ConversationID, &slot.TriggerMessageID,
		&slot.RecipientID, &slot.GroupID, &slot.RunID, &slot.OperationID, &createdAt,
	); err != nil {
		return ReplySlot{}, err
	}
	var err error
	if slot.CreatedAt, err = decodeTime(createdAt); err != nil {
		return ReplySlot{}, err
	}
	return slot, nil
}

func scanGrant(row scanner) (Grant, error) {
	var grant Grant
	var scopes, targets, window, expiresAt, createdAt string
	if err := row.Scan(
		&grant.ID, &grant.ProfileID, &grant.RunID, &grant.Principal,
		&scopes, &targets, &window, &grant.AttachmentByteLimit, &grant.RateLimit,
		&grant.ApprovalPolicy, &expiresAt, &createdAt,
	); err != nil {
		return Grant{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return Grant{}, fmt.Errorf("decode grant scopes: %w", err)
	}
	if err := json.Unmarshal([]byte(targets), &grant.TargetAllowlists); err != nil {
		var legacyTargets []string
		if legacyErr := json.Unmarshal([]byte(targets), &legacyTargets); legacyErr != nil {
			return Grant{}, fmt.Errorf("decode grant targets: %w", err)
		}
		grant.TargetAllowlists = map[string][]string{"legacy": legacyTargets}
	}
	if err := json.Unmarshal([]byte(window), &grant.MessageWindow); err != nil {
		return Grant{}, fmt.Errorf("decode grant message window: %w", err)
	}
	var err error
	if grant.ExpiresAt, err = decodeTime(expiresAt); err != nil {
		return Grant{}, err
	}
	if grant.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Grant{}, err
	}
	return grant, nil
}
