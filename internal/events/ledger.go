// Package events provides the durable ledger between SDK callbacks and runs.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
)

const cursorPrefix = "v1:"

var ErrInvalidCursor = errors.New("invalid event cursor")

// Callback is the minimal, copied identity received from an SDK listener.
// Message contents are intentionally absent.
type Callback struct {
	ProfileID      string
	DedupKey       string
	Type           string
	ConversationID string
	MessageID      string
	OccurredAt     time.Time
}

// Record is a persisted callback result. Created is false for a duplicate.
type Record struct {
	Event   contracts.Event
	Created bool
}

// Batch is one cursor-based event page or watch notification.
type Batch struct {
	Events     []contracts.Event
	NextCursor string
}

type watcher struct {
	profileID string
	after     uint64
	output    chan Batch
}

// Ledger serializes callback persistence for one daemon and broadcasts the
// resulting facts to local watchers. Profile locks prevent cross-process
// writers; the database uniqueness constraints remain the durable guard.
type Ledger struct {
	store *control.Store

	mu       sync.Mutex
	watchers map[*watcher]struct{}
}

func NewLedger(store *control.Store) (*Ledger, error) {
	if store == nil {
		return nil, errors.New("control store is required")
	}
	return &Ledger{store: store, watchers: make(map[*watcher]struct{})}, nil
}

// RecordCallback persists one callback exactly once by SDK dedup key.
func (l *Ledger) RecordCallback(ctx context.Context, callback Callback) (Record, error) {
	if err := validateCallback(callback); err != nil {
		return Record{}, err
	}
	if callback.OccurredAt.IsZero() {
		callback.OccurredAt = time.Now().UTC()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, err := l.store.EventByDedupKey(ctx, callback.ProfileID, callback.DedupKey); err == nil {
		return Record{Event: publicEvent(existing), Created: false}, nil
	} else if !errors.Is(err, control.ErrNotFound) {
		return Record{}, err
	}

	sequence, err := l.store.NextEventSequence(ctx, callback.ProfileID)
	if err != nil {
		return Record{}, err
	}
	event := control.Event{
		ID:             newEventID(),
		ProfileID:      callback.ProfileID,
		Sequence:       sequence,
		SDKDedupKey:    callback.DedupKey,
		Type:           callback.Type,
		ConversationID: callback.ConversationID,
		MessageID:      callback.MessageID,
		OccurredAt:     callback.OccurredAt,
	}
	if err := l.store.PutEvent(ctx, event); err != nil {
		if errors.Is(err, control.ErrConflict) {
			existing, lookupErr := l.store.EventByDedupKey(ctx, callback.ProfileID, callback.DedupKey)
			if lookupErr == nil {
				return Record{Event: publicEvent(existing), Created: false}, nil
			}
		}
		return Record{}, err
	}

	public := publicEvent(event)
	l.broadcastLocked(public)
	return Record{Event: public, Created: true}, nil
}

// Reconcile records restart or synchronization differences as state facts. It
// never creates a synthetic message.received event for time spent offline.
func (l *Ledger) Reconcile(ctx context.Context, profileID string) (Record, error) {
	return l.RecordCallback(ctx, Callback{
		ProfileID:  profileID,
		DedupKey:   "state.reconciled:" + newEventID(),
		Type:       string(contracts.EventStateReconciled),
		OccurredAt: time.Now().UTC(),
	})
}

// List returns a bounded page after a durable opaque cursor.
func (l *Ledger) List(ctx context.Context, profileID, cursor string, limit int) (Batch, error) {
	after, err := parseCursor(cursor)
	if err != nil {
		return Batch{}, err
	}
	events, err := l.store.ListEvents(ctx, profileID, after, limit)
	if err != nil {
		return Batch{}, err
	}
	batch := Batch{Events: make([]contracts.Event, 0, len(events)), NextCursor: cursorFor(after)}
	for _, event := range events {
		public := publicEvent(event)
		batch.Events = append(batch.Events, public)
		batch.NextCursor = cursorFor(public.Sequence)
	}
	return batch, nil
}

// Watch emits new records after cursor until context cancellation. Consumers
// retain NextCursor and can recover any missed notification through List.
func (l *Ledger) Watch(ctx context.Context, profileID, cursor string) (<-chan Batch, error) {
	after, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	watch := &watcher{profileID: profileID, after: after, output: make(chan Batch, 16)}
	l.mu.Lock()
	l.watchers[watch] = struct{}{}
	l.mu.Unlock()

	go func() {
		<-ctx.Done()
		l.mu.Lock()
		if _, exists := l.watchers[watch]; exists {
			delete(l.watchers, watch)
			close(watch.output)
		}
		l.mu.Unlock()
	}()
	return watch.output, nil
}

func (l *Ledger) broadcastLocked(event contracts.Event) {
	for watch := range l.watchers {
		if watch.profileID != event.ProfileID || event.Sequence <= watch.after {
			continue
		}
		batch := Batch{Events: []contracts.Event{event}, NextCursor: cursorFor(event.Sequence)}
		select {
		case watch.output <- batch:
			watch.after = event.Sequence
		default:
			// A slow watcher retains its cursor and must call List to catch up.
		}
	}
}

func validateCallback(callback Callback) error {
	if strings.TrimSpace(callback.ProfileID) == "" || strings.TrimSpace(callback.DedupKey) == "" || strings.TrimSpace(callback.Type) == "" {
		return errors.New("callback profile ID, dedup key, and type are required")
	}
	return nil
}

func publicEvent(event control.Event) contracts.Event {
	payload, _ := json.Marshal(struct {
		ConversationID string `json:"conversation_id,omitempty"`
		MessageID      string `json:"message_id,omitempty"`
	}{ConversationID: event.ConversationID, MessageID: event.MessageID})
	return contracts.Event{
		APIVersion: contracts.APIVersionV1,
		EventID:    event.ID,
		ProfileID:  event.ProfileID,
		Sequence:   event.Sequence,
		Type:       event.Type,
		OccurredAt: event.OccurredAt,
		DedupKey:   event.SDKDedupKey,
		Data:       payload,
	}
}

func newEventID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("event-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func parseCursor(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	if !strings.HasPrefix(cursor, cursorPrefix) {
		return 0, ErrInvalidCursor
	}
	sequence, err := strconv.ParseUint(strings.TrimPrefix(cursor, cursorPrefix), 10, 64)
	if err != nil || sequence == 0 {
		return 0, ErrInvalidCursor
	}
	return sequence, nil
}

func cursorFor(sequence uint64) string {
	if sequence == 0 {
		return ""
	}
	return cursorPrefix + strconv.FormatUint(sequence, 10)
}
