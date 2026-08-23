// Package run schedules conversation-serialized provider turns with bounded
// cross-conversation concurrency and owns their cancellation lifecycle.
package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

var (
	ErrQueueFull = errors.New("conversation run queue is full")
	ErrStopped   = errors.New("run manager is stopped")
)

// Status is a terminal provider-run outcome.
type Status string

const (
	StatusCompleted   Status = "completed"
	StatusCanceled    Status = "canceled"
	StatusDeadline    Status = "deadline_exceeded"
	StatusOverflow    Status = "overflow"
	StatusInterrupted Status = "interrupted"
	StatusFailed      Status = "failed"
)

// Request binds one provider turn to its triggering IM event.
type Request struct {
	ID             string
	ProfileID      string
	ConversationID string
	EventID        string
	Prompt         string
	Events         contracts.RunEventSink
	Started        func(context.Context) error
}

// Result is delivered exactly once for every accepted or rejected run.
type Result struct {
	RunID  string
	Status Status
	Turn   contracts.TurnResult
	Err    error
}

// Handle lets callers await or cancel one run.
type Handle struct {
	RunID string
	Done  <-chan Result
}

// Config fixes the provider adapter and queue, concurrency, and turn limits.
type Config struct {
	Provider          contracts.Provider
	Sessions          SessionStore
	SessionNamespace  string
	MaxQueue          int
	MaxConcurrentRuns int
	Deadline          time.Duration
	Observer          Observer
	OnError           func(error)
}

// SessionStore persists the opaque provider state assigned to each IM conversation.
type SessionStore interface {
	LoadSessionRef(context.Context, string, string, string) (string, bool, error)
	SaveSessionRef(context.Context, string, string, string, string) error
	DeleteSessionRef(context.Context, string, string, string) error
}

// Observer receives lifecycle facts after a run is accepted. Implementations
// must retain only daemon-owned operational metadata and never prompts or
// provider output.
type Observer interface {
	Queued(Request) error
	Started(runID string) error
	Finished(Result) error
}

type job struct {
	request Request
	context context.Context
	cancel  context.CancelFunc
	done    chan Result

	mu       sync.Mutex
	canceled Status
	finished bool
}

type conversation struct {
	pending []*job
	running bool
}

// Manager creates one provider process session per run while resuming the
// conversation's provider state.
type Manager struct {
	provider         contracts.Provider
	sessions         SessionStore
	sessionNamespace string
	maxQueue         int
	slots            chan struct{}
	deadline         time.Duration
	observer         Observer
	onError          func(error)

	mu            sync.Mutex
	conversations map[string]*conversation
	jobs          map[string]*job
	stopped       bool
	workers       sync.WaitGroup

	// onSlotWait is a package-private synchronization point for deterministic
	// cancellation tests. Production managers leave it nil.
	onSlotWait func(string)
}

func NewManager(config Config) (*Manager, error) {
	if config.Provider == nil {
		return nil, errors.New("provider is required")
	}
	if config.MaxQueue <= 0 || config.MaxConcurrentRuns <= 0 || config.Deadline <= 0 {
		return nil, errors.New("positive queue size, concurrent run limit, and turn deadline are required")
	}
	if config.Sessions != nil && strings.TrimSpace(config.SessionNamespace) == "" {
		return nil, errors.New("session namespace is required with a session store")
	}
	return &Manager{
		provider:         config.Provider,
		sessions:         config.Sessions,
		sessionNamespace: config.SessionNamespace,
		maxQueue:         config.MaxQueue,
		slots:            make(chan struct{}, config.MaxConcurrentRuns),
		deadline:         config.Deadline,
		observer:         config.Observer,
		onError:          config.OnError,
		conversations:    make(map[string]*conversation),
		jobs:             make(map[string]*job),
	}, nil
}

// Submit queues a run behind prior turns from the same conversation.
func (m *Manager) Submit(request Request) (*Handle, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	item := &job{request: request, context: ctx, cancel: cancel, done: make(chan Result, 1)}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		cancel()
		return nil, ErrStopped
	}
	if _, exists := m.jobs[request.ID]; exists {
		m.mu.Unlock()
		cancel()
		return nil, errors.New("run ID already exists")
	}
	queue := m.conversations[request.ConversationID]
	if queue == nil {
		queue = &conversation{}
		m.conversations[request.ConversationID] = queue
	}
	if len(queue.pending) >= m.maxQueue {
		m.mu.Unlock()
		cancel()
		item.finish(Result{RunID: request.ID, Status: StatusOverflow, Err: ErrQueueFull})
		return &Handle{RunID: request.ID, Done: item.done}, ErrQueueFull
	}
	queue.pending = append(queue.pending, item)
	m.jobs[request.ID] = item
	if m.observer != nil {
		if err := m.observer.Queued(request); err != nil {
			queue.pending = queue.pending[:len(queue.pending)-1]
			delete(m.jobs, request.ID)
			if len(queue.pending) == 0 && !queue.running {
				delete(m.conversations, request.ConversationID)
			}
			m.mu.Unlock()
			cancel()
			return nil, fmt.Errorf("record accepted run: %w", err)
		}
	}
	if !queue.running {
		queue.running = true
		m.workers.Add(1)
		go m.runConversation(request.ConversationID, queue)
	}
	m.mu.Unlock()
	return &Handle{RunID: request.ID, Done: item.done}, nil
}

// Cancel terminates a queued or active turn after a withdrawal, policy change,
// owner cancellation, or other per-run invalidation.
func (m *Manager) Cancel(runID string) bool {
	m.mu.Lock()
	item, exists := m.jobs[runID]
	if !exists {
		m.mu.Unlock()
		return false
	}
	item.markCanceled(StatusCanceled)
	m.mu.Unlock()
	item.cancel()
	return true
}

// Shutdown interrupts all uncompleted runs.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	items := make([]*job, 0, len(m.jobs))
	for _, item := range m.jobs {
		items = append(items, item)
	}
	m.mu.Unlock()
	for _, item := range items {
		item.markCanceled(StatusInterrupted)
		item.cancel()
	}
	m.workers.Wait()

	return nil
}

func (m *Manager) runConversation(conversationID string, queue *conversation) {
	defer m.workers.Done()
	for {
		m.mu.Lock()
		if len(queue.pending) == 0 {
			queue.running = false
			m.mu.Unlock()
			return
		}
		item := queue.pending[0]
		queue.pending = queue.pending[1:]
		m.mu.Unlock()
		m.execute(item)
	}
}

func (m *Manager) execute(item *job) {
	if status, canceled := item.cancellation(); canceled {
		m.complete(item, Result{RunID: item.request.ID, Status: status})
		m.remove(item.request.ID)
		return
	}

	if err := m.acquireSlot(item.context, item.request.ID); err != nil {
		status := StatusCanceled
		if canceledStatus, canceled := item.cancellation(); canceled {
			status = canceledStatus
		}
		m.complete(item, Result{RunID: item.request.ID, Status: status, Err: err})
		m.remove(item.request.ID)
		return
	}
	defer func() { <-m.slots }()
	if status, canceled := item.cancellation(); canceled {
		m.complete(item, Result{RunID: item.request.ID, Status: status})
		m.remove(item.request.ID)
		return
	}
	if m.observer != nil {
		if err := m.observer.Started(item.request.ID); err != nil {
			m.complete(item, Result{RunID: item.request.ID, Status: StatusInterrupted, Err: fmt.Errorf("record run start: %w", err)})
			m.remove(item.request.ID)
			return
		}
	}
	if item.request.Started != nil {
		if err := item.request.Started(context.Background()); err != nil {
			m.complete(item, Result{RunID: item.request.ID, Status: StatusInterrupted, Err: fmt.Errorf("publish run start: %w", err)})
			m.remove(item.request.ID)
			return
		}
	}

	turnContext, turnCancel := context.WithTimeout(item.context, m.deadline)
	defer turnCancel()
	startRequest := contracts.StartRequest{
		ProfileID: item.request.ProfileID,
		RunID:     item.request.ID,
		StateKey:  providerStateKey(item.request.ProfileID, item.request.ConversationID),
	}
	if m.sessions != nil {
		sessionRef, _, loadErr := m.sessions.LoadSessionRef(turnContext, item.request.ProfileID, item.request.ConversationID, m.sessionNamespace)
		if loadErr != nil {
			m.complete(item, Result{RunID: item.request.ID, Status: StatusFailed, Err: loadErr})
			m.remove(item.request.ID)
			return
		}
		startRequest.SessionRef = sessionRef
	}
	session, err := m.provider.Start(turnContext, startRequest)
	if errors.Is(err, contracts.ErrSessionNotFound) && startRequest.SessionRef != "" && m.sessions != nil {
		if deleteErr := m.sessions.DeleteSessionRef(turnContext, item.request.ProfileID, item.request.ConversationID, m.sessionNamespace); deleteErr != nil {
			err = deleteErr
		} else {
			startRequest.SessionRef = ""
			session, err = m.provider.Start(turnContext, startRequest)
		}
	}
	if err != nil {
		m.complete(item, Result{RunID: item.request.ID, Status: StatusFailed, Err: err})
		m.remove(item.request.ID)
		return
	}
	if session == nil {
		m.complete(item, Result{RunID: item.request.ID, Status: StatusFailed, Err: errors.New("provider returned nil session")})
		m.remove(item.request.ID)
		return
	}
	defer session.Close(context.Background())

	finished := make(chan struct{})
	go func() {
		select {
		case <-turnContext.Done():
			_ = session.Cancel(context.Background())
		case <-finished:
		}
	}()
	result, err := session.Turn(turnContext, contracts.TurnRequest{
		RunID: item.request.ID, EventID: item.request.EventID,
		Prompt: item.request.Prompt,
		Events: item.request.Events,
	})
	close(finished)
	if status, canceled := item.cancellation(); canceled {
		m.complete(item, Result{RunID: item.request.ID, Status: status, Err: turnContext.Err()})
	} else if turnContext.Err() != nil {
		m.complete(item, Result{RunID: item.request.ID, Status: StatusDeadline, Err: turnContext.Err()})
	} else if err != nil {
		m.complete(item, Result{RunID: item.request.ID, Status: StatusFailed, Err: err})
	} else if m.sessions != nil && strings.TrimSpace(result.SessionRef) != "" {
		if err := m.sessions.SaveSessionRef(turnContext, item.request.ProfileID, item.request.ConversationID, m.sessionNamespace, result.SessionRef); err != nil {
			m.complete(item, Result{RunID: item.request.ID, Status: StatusFailed, Err: err})
		} else {
			m.complete(item, Result{RunID: item.request.ID, Status: StatusCompleted, Turn: result})
		}
	} else {
		m.complete(item, Result{RunID: item.request.ID, Status: StatusCompleted, Turn: result})
	}
	m.remove(item.request.ID)
}

func (m *Manager) acquireSlot(ctx context.Context, runID string) error {
	select {
	case m.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.slots
			return err
		}
		return nil
	default:
	}
	if m.onSlotWait != nil {
		m.onSlotWait(runID)
	}
	select {
	case m.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.slots
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func providerStateKey(profileID, conversationID string) string {
	input := fmt.Sprintf("abdim-provider-state-v1:%d:%s:%d:%s", len(profileID), profileID, len(conversationID), conversationID)
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}

func (m *Manager) complete(item *job, result Result) {
	if !item.finish(result) {
		return
	}
	if m.observer != nil {
		if err := m.observer.Finished(result); err != nil {
			m.report(fmt.Errorf("record run finish: %w", err))
		}
	}
}

func (m *Manager) remove(runID string) {
	m.mu.Lock()
	delete(m.jobs, runID)
	m.mu.Unlock()
}

func (item *job) markCanceled(status Status) {
	item.mu.Lock()
	if item.canceled == "" {
		item.canceled = status
	}
	item.mu.Unlock()
}

func (item *job) cancellation() (Status, bool) {
	item.mu.Lock()
	defer item.mu.Unlock()
	return item.canceled, item.canceled != ""
}

func (item *job) finish(result Result) bool {
	item.mu.Lock()
	if item.finished {
		item.mu.Unlock()
		return false
	}
	item.finished = true
	item.mu.Unlock()
	item.cancel()
	item.done <- result
	close(item.done)
	return true
}

func (m *Manager) report(err error) {
	if err != nil && m.onError != nil {
		m.onError(err)
	}
}

func validateRequest(request Request) error {
	if request.ID == "" || request.ProfileID == "" || request.ConversationID == "" || request.EventID == "" {
		return errors.New("run ID, profile ID, conversation ID, and event ID are required")
	}
	return nil
}
