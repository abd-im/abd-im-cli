// Package control persists the small amount of daemon-owned metadata.
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
	ErrNotFound = errors.New("control record not found")
	ErrConflict = errors.New("control record conflict")
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("control database path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open control database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

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
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
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
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", migration.version, encodeTime(time.Now())); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit control migration: %w", err)
	}
	return nil
}

func (s *Store) PutProfile(ctx context.Context, profile Profile) error {
	if strings.TrimSpace(profile.ID) == "" {
		return errors.New("profile ID is required")
	}
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO profiles (id, created_at, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at`, profile.ID, encodeTime(profile.CreatedAt), encodeTime(profile.UpdatedAt))
	if err != nil {
		return fmt.Errorf("put profile: %w", err)
	}
	return nil
}

func (s *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	var profile Profile
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, created_at, updated_at FROM profiles WHERE id = ?`, id).Scan(&profile.ID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("%w: profile %q", ErrNotFound, id)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	profile.CreatedAt, err = decodeTime(createdAt)
	if err == nil {
		profile.UpdatedAt, err = decodeTime(updatedAt)
	}
	return profile, err
}

func (s *Store) PutEvent(ctx context.Context, event Event) error {
	if err := event.validate(); err != nil {
		return err
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events
		(id, profile_id, sequence, sdk_dedup_key, type, conversation_id, message_id, occurred_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.ProfileID, event.Sequence, event.SDKDedupKey, event.Type,
		event.ConversationID, event.MessageID, encodeTime(event.OccurredAt), encodeTime(event.RecordedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: event %q", ErrConflict, event.ID)
	}
	if err != nil {
		return fmt.Errorf("put event: %w", err)
	}
	return nil
}

func (s *Store) EventByDedupKey(ctx context.Context, profileID, dedupKey string) (Event, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, profile_id, sequence, sdk_dedup_key, type, conversation_id, message_id, occurred_at, recorded_at
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

func (s *Store) NextEventSequence(ctx context.Context, profileID string) (uint64, error) {
	if strings.TrimSpace(profileID) == "" {
		return 0, errors.New("profile ID is required")
	}
	var sequence uint64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE profile_id = ?`, profileID).Scan(&sequence)
	return sequence, err
}

func (s *Store) ListEvents(ctx context.Context, profileID string, after uint64, limit int) ([]Event, error) {
	if strings.TrimSpace(profileID) == "" || limit <= 0 || limit > 100 {
		return nil, errors.New("profile ID and event limit between 1 and 100 are required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, profile_id, sequence, sdk_dedup_key, type, conversation_id, message_id, occurred_at, recorded_at
		FROM events WHERE profile_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, profileID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var result []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) LoadSessionRef(ctx context.Context, profileID, conversationID, provider string) (string, bool, error) {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(provider) == "" {
		return "", false, errors.New("provider session profile, conversation, and provider are required")
	}
	var ref string
	err := s.db.QueryRowContext(ctx, `SELECT session_ref FROM provider_sessions WHERE profile_id = ? AND conversation_id = ? AND provider = ?`, profileID, conversationID, provider).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load provider session: %w", err)
	}
	return ref, true, nil
}

func (s *Store) SaveSessionRef(ctx context.Context, profileID, conversationID, provider, sessionRef string) error {
	session := ProviderSession{ProfileID: profileID, ConversationID: conversationID, Provider: provider, SessionRef: sessionRef}
	if err := session.validate(); err != nil {
		return err
	}
	now := encodeTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_sessions (profile_id, conversation_id, provider, session_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(profile_id, conversation_id, provider) DO UPDATE SET session_ref = excluded.session_ref, updated_at = excluded.updated_at`,
		profileID, conversationID, provider, sessionRef, now, now)
	return err
}

func (s *Store) DeleteSessionRef(ctx context.Context, profileID, conversationID, provider string) error {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(provider) == "" {
		return errors.New("provider session profile, conversation, and provider are required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM provider_sessions WHERE profile_id = ? AND conversation_id = ? AND provider = ?`, profileID, conversationID, provider)
	return err
}

type scanner interface{ Scan(...any) error }

func scanEvent(row scanner) (Event, error) {
	var event Event
	var occurredAt, recordedAt string
	err := row.Scan(&event.ID, &event.ProfileID, &event.Sequence, &event.SDKDedupKey, &event.Type, &event.ConversationID, &event.MessageID, &occurredAt, &recordedAt)
	if err != nil {
		return Event{}, err
	}
	event.OccurredAt, err = decodeTime(occurredAt)
	if err == nil {
		event.RecordedAt, err = decodeTime(recordedAt)
	}
	return event, err
}

func encodeTime(value time.Time) string          { return value.UTC().Format(time.RFC3339Nano) }
func decodeTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint failed")
}
