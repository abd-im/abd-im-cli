// Package run serializes provider turns and owns their cancellation lifecycle.
package run

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

var (
	ErrQueueFull = errors.New("conversation run queue is full")
	ErrStopped   = errors.New("run manager is stopped")
)

// Status is a terminal provider-run outcome.
type Status string

const (
	StatusCompleted    Status = "completed"
	StatusCanceled     Status = "canceled"
	StatusDeadline     Status = "deadline_exceeded"
	StatusGrantExpired Status = "grant_expired"
	StatusOverflow     Status = "overflow"
	StatusInterrupted  Status = "interrupted"
	StatusFailed       Status = "failed"
)

// Request binds one provider turn to its triggering event and private proxy.
type Request struct {
	ID             string
	ProfileID      string
	ConversationID string
	EventID        string
	GrantExpiresAt time.Time
	Proxy          contracts.ToolProxy
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

// Config fixes the single provider adapter and queue/turn limits for a daemon.
type Config struct {
	Provider contracts.Provider
	MaxQueue int
	Deadline time.Duration
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

// Manager uses one provider session during one daemon lifetime. A stable proxy
// facade is passed at Start and is rebound only while the current turn runs.
type Manager struct {
	provider contracts.Provider
	maxQueue int
	deadline time.Duration

	mu            sync.Mutex
	conversations map[string]*conversation
	jobs          map[string]*job
	stopped       bool
	workers       sync.WaitGroup

	turnMu   sync.Mutex
	session  contracts.Session
	switcher *switchingProxy
}

func NewManager(config Config) (*Manager, error) {
	if config.Provider == nil {
		return nil, errors.New("provider is required")
	}
	if config.MaxQueue <= 0 || config.Deadline <= 0 {
		return nil, errors.New("positive queue size and turn deadline are required")
	}
	return &Manager{
		provider:      config.Provider,
		maxQueue:      config.MaxQueue,
		deadline:      config.Deadline,
		conversations: make(map[string]*conversation),
		jobs:          make(map[string]*job),
		switcher:      &switchingProxy{},
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
		_ = request.Proxy.Close(context.Background())
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

// Shutdown interrupts all uncompleted runs and closes the reusable session.
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

	m.turnMu.Lock()
	session := m.session
	m.session = nil
	m.turnMu.Unlock()
	if session != nil {
		return session.Close(ctx)
	}
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
		item.finish(Result{RunID: item.request.ID, Status: status})
		m.remove(item.request.ID)
		return
	}

	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	if status, canceled := item.cancellation(); canceled {
		item.finish(Result{RunID: item.request.ID, Status: status})
		m.remove(item.request.ID)
		return
	}

	m.switcher.bind(item.request.Proxy)
	defer m.switcher.clear()
	turnContext, turnCancel := withTurnDeadline(item.context, m.deadline, item.request.GrantExpiresAt)
	defer turnCancel()
	session, err := m.ensureSession(turnContext, item)
	if err != nil {
		item.finish(Result{RunID: item.request.ID, Status: StatusFailed, Err: err})
		m.remove(item.request.ID)
		return
	}

	finished := make(chan struct{})
	go func() {
		select {
		case <-turnContext.Done():
			_ = session.Cancel(context.Background())
		case <-finished:
		}
	}()
	result, err := session.Turn(turnContext, contracts.TurnRequest{RunID: item.request.ID, EventID: item.request.EventID})
	close(finished)
	if status, canceled := item.cancellation(); canceled {
		item.finish(Result{RunID: item.request.ID, Status: status, Err: turnContext.Err()})
	} else if turnContext.Err() != nil {
		item.finish(Result{RunID: item.request.ID, Status: deadlineStatus(item.request.GrantExpiresAt), Err: turnContext.Err()})
	} else if err != nil {
		item.finish(Result{RunID: item.request.ID, Status: StatusFailed, Err: err})
	} else {
		item.finish(Result{RunID: item.request.ID, Status: StatusCompleted, Turn: result})
	}
	m.remove(item.request.ID)
}

func (m *Manager) ensureSession(ctx context.Context, item *job) (contracts.Session, error) {
	if m.session != nil {
		return m.session, nil
	}
	session, err := m.provider.Start(ctx, contracts.StartRequest{ProfileID: item.request.ProfileID, RunID: item.request.ID, Proxy: m.switcher})
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("provider returned nil session")
	}
	m.session = session
	return session, nil
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

func (item *job) finish(result Result) {
	item.mu.Lock()
	if item.finished {
		item.mu.Unlock()
		return
	}
	item.finished = true
	item.mu.Unlock()
	item.cancel()
	_ = item.request.Proxy.Close(context.Background())
	item.done <- result
	close(item.done)
}

func validateRequest(request Request) error {
	if request.ID == "" || request.ProfileID == "" || request.ConversationID == "" || request.EventID == "" || request.Proxy == nil {
		return errors.New("run ID, profile ID, conversation ID, event ID, and proxy are required")
	}
	if request.GrantExpiresAt.IsZero() {
		return errors.New("run grant expiry is required")
	}
	return nil
}

func withTurnDeadline(parent context.Context, deadline time.Duration, grantExpiry time.Time) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(deadline)
	if grantExpiry.Before(limit) {
		limit = grantExpiry
	}
	return context.WithDeadline(parent, limit)
}

func deadlineStatus(grantExpiry time.Time) Status {
	if !time.Now().Before(grantExpiry) {
		return StatusGrantExpired
	}
	return StatusDeadline
}

type switchingProxy struct {
	mu      sync.RWMutex
	current contracts.ToolProxy
}

func (p *switchingProxy) bind(current contracts.ToolProxy) {
	p.mu.Lock()
	p.current = current
	p.mu.Unlock()
}

func (p *switchingProxy) clear() {
	p.mu.Lock()
	p.current = nil
	p.mu.Unlock()
}

func (p *switchingProxy) Call(ctx context.Context, request contracts.Request) (contracts.Response, error) {
	p.mu.RLock()
	current := p.current
	p.mu.RUnlock()
	if current == nil {
		return contracts.Response{}, errors.New("no active run proxy")
	}
	return current.Call(ctx, request)
}

// Close is intentionally a no-op for the reusable provider session. The
// manager alone closes each bound run proxy as that run completes.
func (p *switchingProxy) Close(context.Context) error { return nil }

var _ contracts.ToolProxy = (*switchingProxy)(nil)
