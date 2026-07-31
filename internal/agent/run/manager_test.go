package run

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/testkit"
)

func TestManagerSerializesConversationAndReusesSingleSession(t *testing.T) {
	session := &recordingSession{}
	provider := &recordingProvider{session: session}
	manager, err := NewManager(Config{Provider: provider, MaxQueue: 2, Deadline: time.Second})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Submit(testRequest("run-1", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	second, err := manager.Submit(testRequest("run-2", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	if result := <-first.Done; result.Status != StatusCompleted {
		t.Fatalf("first result = %#v", result)
	}
	if result := <-second.Done; result.Status != StatusCompleted {
		t.Fatalf("second result = %#v", result)
	}
	if provider.starts != 1 {
		t.Fatalf("provider starts = %d, want 1", provider.starts)
	}
	if len(provider.requests) != 1 || provider.requests[0].GrantCredential != "grant-run-1" {
		t.Fatalf("provider start credentials = %+v", provider.requests)
	}
	if got, want := session.runIDs(), []string{"run-1", "run-2"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
	if got, want := session.grantCredentials(), []string{"grant-run-1", "grant-run-2"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("turn credentials = %v, want %v", got, want)
	}
}

func TestManagerRejectsOverflowAndCancelsGrantExpiry(t *testing.T) {
	block := make(chan struct{})
	session := &recordingSession{block: block}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: session}, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Submit(testRequest("run-1", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	if !session.waitForTurn(time.Second) {
		t.Fatal("first turn did not start")
	}
	second, err := manager.Submit(testRequest("run-2", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	overflow, err := manager.Submit(testRequest("run-3", "conversation-1", time.Now().Add(time.Hour)))
	if err != ErrQueueFull {
		t.Fatalf("Submit(overflow) error = %v, want ErrQueueFull", err)
	}
	if result := <-overflow.Done; result.Status != StatusOverflow {
		t.Fatalf("overflow result = %#v", result)
	}
	close(block)
	<-first.Done
	<-second.Done

	expiring := &recordingSession{block: make(chan struct{})}
	expiringManager, err := NewManager(Config{Provider: &recordingProvider{session: expiring}, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		t.Fatalf("NewManager(expiring) error = %v", err)
	}
	defer expiringManager.Shutdown(context.Background())
	handle, err := expiringManager.Submit(testRequest("run-expired", "conversation-2", time.Now().Add(10*time.Millisecond)))
	if err != nil {
		t.Fatalf("Submit(expiring) error = %v", err)
	}
	if result := <-handle.Done; result.Status != StatusGrantExpired {
		t.Fatalf("expired result = %#v", result)
	}
}

func TestManagerCancelAndShutdownTerminateRuns(t *testing.T) {
	canceledSession := &recordingSession{block: make(chan struct{})}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: canceledSession}, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	handle, err := manager.Submit(testRequest("run-cancel", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if !canceledSession.waitForTurn(time.Second) {
		t.Fatal("turn did not start")
	}
	if !manager.Cancel("run-cancel") {
		t.Fatal("Cancel() = false, want true")
	}
	if result := <-handle.Done; result.Status != StatusCanceled {
		t.Fatalf("canceled result = %#v", result)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	shutdownSession := &recordingSession{block: make(chan struct{})}
	shutdownManager, err := NewManager(Config{Provider: &recordingProvider{session: shutdownSession}, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		t.Fatalf("NewManager(shutdown) error = %v", err)
	}
	shutdownHandle, err := shutdownManager.Submit(testRequest("run-shutdown", "conversation-2", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Submit(shutdown) error = %v", err)
	}
	if !shutdownSession.waitForTurn(time.Second) {
		t.Fatal("shutdown turn did not start")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- shutdownManager.Shutdown(context.Background()) }()
	if result := <-shutdownHandle.Done; result.Status != StatusInterrupted {
		t.Fatalf("shutdown result = %#v", result)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if shutdownSession.closeCount() != 1 {
		t.Fatalf("session Close() count = %d, want 1", shutdownSession.closeCount())
	}
}

func testRequest(id, conversation string, expiry time.Time) Request {
	return Request{ID: id, ProfileID: "work", ConversationID: conversation, EventID: "event-" + id, GrantCredential: "grant-" + id, GrantExpiresAt: expiry, Proxy: &testkit.FakeProxy{Response: &contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: "proxy", OK: true, Data: json.RawMessage(`{}`), Meta: &contracts.Meta{ProfileID: "work"}}}}
}

type recordingProvider struct {
	mu       sync.Mutex
	starts   int
	requests []contracts.StartRequest
	session  *recordingSession
}

func (p *recordingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts++
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return p.session, nil
}

type recordingSession struct {
	mu      sync.Mutex
	turns   []contracts.TurnRequest
	block   chan struct{}
	started chan struct{}
	closes  int
}

func (s *recordingSession) Turn(ctx context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	s.mu.Lock()
	s.turns = append(s.turns, request)
	if s.started == nil {
		s.started = make(chan struct{})
	}
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	block := s.block
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return contracts.TurnResult{}, ctx.Err()
		}
	}
	return contracts.TurnResult{FinalText: request.RunID}, nil
}

func (s *recordingSession) Cancel(context.Context) error { return nil }
func (s *recordingSession) Close(context.Context) error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *recordingSession) runIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	runIDs := make([]string, 0, len(s.turns))
	for _, turn := range s.turns {
		runIDs = append(runIDs, turn.RunID)
	}
	return runIDs
}

func (s *recordingSession) grantCredentials() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	credentials := make([]string, 0, len(s.turns))
	for _, turn := range s.turns {
		credentials = append(credentials, turn.GrantCredential)
	}
	return credentials
}

func (s *recordingSession) waitForTurn(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		started := s.started
		s.mu.Unlock()
		if started != nil {
			select {
			case <-started:
				return true
			default:
			}
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func (s *recordingSession) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}
