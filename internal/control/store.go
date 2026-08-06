// Package control persists daemon-owned control-plane metadata.
package control

import (
	"context"
	"database/sql"
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
	// ErrAttachmentQuota reports that a run has exhausted its attachment quota.
	ErrAttachmentQuota = errors.New("attachment quota exceeded")
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
			id, profile_id, scope, idempotency_key, input_digest, target_summary, status, error_summary, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID, operation.ProfileID, operation.Scope, operation.IdempotencyKey,
		operation.InputDigest, operation.TargetSummary, operation.Status, operation.ErrorSummary, encodeTime(operation.CreatedAt), encodeTime(operation.UpdatedAt))
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
		SELECT id, profile_id, scope, idempotency_key, input_digest, target_summary, status, error_summary, created_at, updated_at
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
		SELECT id, profile_id, scope, idempotency_key, input_digest, target_summary, status, error_summary, created_at, updated_at
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

// OperationByID returns one operation without exposing its idempotency key or
// input digest through higher-level owner diagnostics.
func (s *Store) OperationByID(ctx context.Context, profileID, id string) (Operation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, scope, idempotency_key, input_digest, target_summary, status, error_summary, created_at, updated_at
		FROM operations WHERE profile_id = ? AND id = ?`, profileID, id)
	operation, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, fmt.Errorf("%w: operation %q", ErrNotFound, id)
	}
	if err != nil {
		return Operation{}, fmt.Errorf("get operation by ID: %w", err)
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
		UPDATE operations
		SET status = ?, error_summary = CASE WHEN ? = 'failed' THEN error_summary ELSE '' END, updated_at = ?
		WHERE id = ?`, status, status, encodeTime(time.Now()), id)
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

// UpdateOperationFailure records the public category of a failed effect. The
// caller must pass a pre-redacted bounded summary, never an SDK error.
func (s *Store) UpdateOperationFailure(ctx context.Context, id, summary string) error {
	if len(summary) > 256 {
		return errors.New("operation error summary must not exceed 256 bytes")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE operations SET status = ?, error_summary = ?, updated_at = ? WHERE id = ?`,
		OperationFailed, summary, encodeTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("record operation failure: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check operation failure update: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: operation %q", ErrNotFound, id)
	}
	return nil
}

// PutRun records an accepted provider turn before it starts.
func (s *Store) PutRun(ctx context.Context, run Run) error {
	if err := run.validate(); err != nil {
		return err
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (id, profile_id, conversation_id, event_id, status, reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ProfileID, run.ConversationID, run.EventID, run.Status, run.Reason,
		encodeTime(run.CreatedAt), encodeTime(run.UpdatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: run %q", ErrConflict, run.ID)
	}
	if err != nil {
		return fmt.Errorf("put run: %w", err)
	}
	return nil
}

// LoadSessionRef returns the provider session mapped to one IM conversation.
func (s *Store) LoadSessionRef(ctx context.Context, profileID, conversationID, provider string) (string, bool, error) {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(provider) == "" {
		return "", false, errors.New("provider session profile, conversation, and provider are required")
	}
	var sessionRef string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_ref FROM provider_sessions
		WHERE profile_id = ? AND conversation_id = ? AND provider = ?`, profileID, conversationID, provider).Scan(&sessionRef)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load provider session: %w", err)
	}
	return sessionRef, true, nil
}

// SaveSessionRef creates or replaces the provider session for a conversation.
func (s *Store) SaveSessionRef(ctx context.Context, profileID, conversationID, provider, sessionRef string) error {
	session := ProviderSession{ProfileID: profileID, ConversationID: conversationID, Provider: provider, SessionRef: sessionRef}
	if err := session.validate(); err != nil {
		return err
	}
	now := encodeTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO provider_sessions (profile_id, conversation_id, provider, session_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, conversation_id, provider) DO UPDATE SET
			session_ref = excluded.session_ref,
			updated_at = excluded.updated_at`, profileID, conversationID, provider, sessionRef, now, now)
	if err != nil {
		return fmt.Errorf("save provider session: %w", err)
	}
	return nil
}

// DeleteSessionRef removes a stale provider session mapping.
func (s *Store) DeleteSessionRef(ctx context.Context, profileID, conversationID, provider string) error {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(provider) == "" {
		return errors.New("provider session profile, conversation, and provider are required")
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM provider_sessions WHERE profile_id = ? AND conversation_id = ? AND provider = ?`, profileID, conversationID, provider); err != nil {
		return fmt.Errorf("delete provider session: %w", err)
	}
	return nil
}

// RunByID returns one profile-scoped run record.
func (s *Store) RunByID(ctx context.Context, profileID, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, conversation_id, event_id, status, reason, created_at, updated_at
		FROM runs WHERE profile_id = ? AND id = ?`, profileID, id)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: run %q", ErrNotFound, id)
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return run, nil
}

// ListRuns returns a stable, bounded page sorted by creation time then ID.
func (s *Store) ListRuns(ctx context.Context, profileID string, offset, limit int) ([]Run, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	if offset < 0 || limit <= 0 || limit > 101 {
		return nil, errors.New("run offset must be non-negative and limit must be between 1 and 101")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile_id, conversation_id, event_id, status, reason, created_at, updated_at
		FROM runs WHERE profile_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, profileID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	result := make([]Run, 0, limit)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		result = append(result, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return result, nil
}

// UpdateRunStatus changes one known run's operational state. Reasons are
// fixed public labels rather than provider or SDK error strings.
func (s *Store) UpdateRunStatus(ctx context.Context, profileID, id string, status RunStatus, reason string) error {
	if len(reason) > 256 {
		return errors.New("run reason must not exceed 256 bytes")
	}
	switch status {
	case RunQueued, RunRunning, RunCompleted, RunInterrupted, RunCancelled:
	default:
		return errors.New("invalid run status")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, reason = ?, updated_at = ? WHERE profile_id = ? AND id = ?`,
		status, reason, encodeTime(time.Now()), profileID, id)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check run status update: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: run %q", ErrNotFound, id)
	}
	return nil
}

// InterruptActiveRuns marks work left active by an earlier daemon process as
// interrupted. It never replays provider output or remote effects.
func (s *Store) InterruptActiveRuns(ctx context.Context, profileID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, reason = ?, updated_at = ?
		WHERE profile_id = ? AND status IN (?, ?)`,
		RunInterrupted, "daemon restarted", encodeTime(time.Now()), profileID, RunQueued, RunRunning)
	if err != nil {
		return fmt.Errorf("interrupt active runs: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check interrupted runs: %w", err)
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

// PutAttachment reserves quota and records attachment metadata. It never
// stores the attachment's contents, original name, or filesystem path.
func (s *Store) PutAttachment(ctx context.Context, attachment Attachment) (err error) {
	if err := attachment.validate(); err != nil {
		return err
	}
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin attachment reservation: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var used int64
	if err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM attachments WHERE profile_id = ? AND run_id = ? AND grant_id = ?`, attachment.ProfileID, attachment.RunID, attachment.GrantID).Scan(&used); err != nil {
		return fmt.Errorf("read attachment quota: %w", err)
	}
	var recordedLimit sql.NullInt64
	if err = tx.QueryRowContext(ctx, `
		SELECT byte_limit FROM attachments
		WHERE profile_id = ? AND run_id = ? AND grant_id = ?
		LIMIT 1`, attachment.ProfileID, attachment.RunID, attachment.GrantID).Scan(&recordedLimit); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read attachment byte limit: %w", err)
	}
	if recordedLimit.Valid && recordedLimit.Int64 != attachment.ByteLimit {
		return fmt.Errorf("%w: attachment byte limit differs for grant %q", ErrConflict, attachment.GrantID)
	}
	if used > attachment.ByteLimit || attachment.SizeBytes > attachment.ByteLimit-used {
		return ErrAttachmentQuota
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO attachments (id, profile_id, run_id, grant_id, kind, size_bytes, byte_limit, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attachment.ID, attachment.ProfileID, attachment.RunID, attachment.GrantID, attachment.Kind, attachment.SizeBytes, attachment.ByteLimit,
		encodeTime(attachment.ExpiresAt), encodeTime(attachment.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: attachment %q", ErrConflict, attachment.ID)
	}
	if err != nil {
		return fmt.Errorf("put attachment: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit attachment reservation: %w", err)
	}
	return nil
}

// AttachmentByID returns attachment metadata scoped to one profile.
func (s *Store) AttachmentByID(ctx context.Context, profileID, id string) (Attachment, error) {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(id) == "" {
		return Attachment{}, errors.New("attachment profile ID and ID are required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, run_id, grant_id, kind, size_bytes, byte_limit, expires_at, created_at
		FROM attachments WHERE profile_id = ? AND id = ?`, profileID, id)
	attachment, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, fmt.Errorf("%w: attachment %q", ErrNotFound, id)
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	return attachment, nil
}

// DeleteAttachment removes metadata after a filesystem write failure.
func (s *Store) DeleteAttachment(ctx context.Context, profileID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE profile_id = ? AND id = ?`, profileID, id)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check attachment delete: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: attachment %q", ErrNotFound, id)
	}
	return nil
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
		&operation.InputDigest, &operation.TargetSummary, &operation.Status, &operation.ErrorSummary, &createdAt, &updatedAt,
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

func scanRun(row scanner) (Run, error) {
	var run Run
	var createdAt, updatedAt string
	if err := row.Scan(
		&run.ID, &run.ProfileID, &run.ConversationID, &run.EventID, &run.Status, &run.Reason, &createdAt, &updatedAt,
	); err != nil {
		return Run{}, err
	}
	var err error
	if run.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Run{}, err
	}
	if run.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return Run{}, err
	}
	return run, nil
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

func scanAttachment(row scanner) (Attachment, error) {
	var attachment Attachment
	var expiresAt, createdAt string
	if err := row.Scan(
		&attachment.ID, &attachment.ProfileID, &attachment.RunID, &attachment.GrantID, &attachment.Kind, &attachment.SizeBytes, &attachment.ByteLimit,
		&expiresAt, &createdAt,
	); err != nil {
		return Attachment{}, err
	}
	var err error
	if attachment.ExpiresAt, err = decodeTime(expiresAt); err != nil {
		return Attachment{}, err
	}
	if attachment.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Attachment{}, err
	}
	return attachment, nil
}
