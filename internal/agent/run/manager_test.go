package run

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestManagerSerializesConversationAndCreatesRunPrivateSessions(t *testing.T) {
	session := &recordingSession{}
	provider := &recordingProvider{session: session}
	manager, err := NewManager(Config{Provider: provider, MaxQueue: 2, MaxConcurrentRuns: 2, Deadline: time.Second})
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
	if provider.starts != 2 {
		t.Fatalf("provider starts = %d, want 2", provider.starts)
	}
	if len(provider.requests) != 2 || provider.requests[0].GrantCredential != "grant-run-1" || provider.requests[1].GrantCredential != "grant-run-2" {
		t.Fatalf("provider start credentials = %+v", provider.requests)
	}
	if got := provider.requests[0].AllowedMethods; len(got) != 1 || got[0] != "message.history" {
		t.Fatalf("provider allowed methods = %v", got)
	}
	if got, want := session.runIDs(), []string{"run-1", "run-2"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
	if got, want := session.grantCredentials(), []string{"grant-run-1", "grant-run-2"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("turn credentials = %v, want %v", got, want)
	}
}

func TestManagerPublishesStartedOnlyWhenQueuedRunExecutes(t *testing.T) {
	block := make(chan struct{})
	session := &recordingSession{block: block}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: session}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	started := make(chan string, 2)
	firstRequest := testRequest("run-1", "conversation-1", time.Now().Add(time.Hour))
	firstRequest.Started = func(context.Context) error { started <- "run-1"; return nil }
	first, err := manager.Submit(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "run-1" {
		t.Fatalf("first started callback = %q", got)
	}

	secondRequest := testRequest("run-2", "conversation-1", time.Now().Add(time.Hour))
	secondRequest.Started = func(context.Context) error { started <- "run-2"; return nil }
	second, err := manager.Submit(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		t.Fatalf("queued run started early: %q", got)
	case <-time.After(20 * time.Millisecond):
	}

	close(block)
	if result := <-first.Done; result.Status != StatusCompleted {
		t.Fatalf("first result = %#v", result)
	}
	if got := <-started; got != "run-2" {
		t.Fatalf("second started callback = %q", got)
	}
	if result := <-second.Done; result.Status != StatusCompleted {
		t.Fatalf("second result = %#v", result)
	}
}

func TestManagerReusesConversationSessionAndReplacesMissingSession(t *testing.T) {
	sessions := &memorySessionStore{refs: make(map[string]string)}
	provider := &sessionRefProvider{}
	manager, err := NewManager(Config{Provider: provider, Sessions: sessions, SessionNamespace: "codex", MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	first, err := manager.Submit(testRequest("run-1", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if result := <-first.Done; result.Status != StatusCompleted || result.Turn.SessionRef != "session-new" {
		t.Fatalf("first result = %#v", result)
	}
	second, err := manager.Submit(testRequest("run-2", "conversation-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if result := <-second.Done; result.Status != StatusCompleted || result.Turn.SessionRef != "session-new" {
		t.Fatalf("second result = %#v", result)
	}

	sessions.refs["work/conversation-2/codex"] = "session-missing"
	third, err := manager.Submit(testRequest("run-3", "conversation-2", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if result := <-third.Done; result.Status != StatusCompleted || result.Turn.SessionRef != "session-new" {
		t.Fatalf("fallback result = %#v", result)
	}

	got := provider.sessionRefs()
	want := []string{"", "session-new", "session-missing", ""}
	if len(got) != len(want) {
		t.Fatalf("provider session refs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("provider session refs = %v, want %v", got, want)
		}
	}
	if sessions.refs["work/conversation-2/codex"] != "session-new" {
		t.Fatalf("replacement session ref = %q", sessions.refs["work/conversation-2/codex"])
	}
	keys := provider.stateKeys()
	if len(keys) != 4 || keys[0] != keys[1] || keys[2] != keys[3] || keys[0] == keys[2] {
		t.Fatalf("provider state keys = %v", keys)
	}
	for _, key := range keys {
		if len(key) != 64 {
			t.Fatalf("provider state key length = %d, want 64", len(key))
		}
	}
}

func TestManagerRejectsOverflowAndCancelsGrantExpiry(t *testing.T) {
	block := make(chan struct{})
	session := &recordingSession{block: block}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: session}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
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
	expiringManager, err := NewManager(Config{Provider: &recordingProvider{session: expiring}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
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

func TestManagerKeepsConversationQueuesDistinct(t *testing.T) {
	block := make(chan struct{})
	session := &recordingSession{block: block}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: session}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	first, err := manager.Submit(testRequest("run-a1", "conversation-a", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if !session.waitForTurn(time.Second) {
		t.Fatal("first conversation did not start")
	}
	second, err := manager.Submit(testRequest("run-a2", "conversation-a", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Submit(testRequest("run-a3", "conversation-a", time.Now().Add(time.Hour))); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third run in conversation-a error = %v, want ErrQueueFull", err)
	}
	other, err := manager.Submit(testRequest("run-b1", "conversation-b", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("conversation-b was blocked by conversation-a queue: %v", err)
	}

	close(block)
	for name, handle := range map[string]*Handle{"first": first, "second": second, "other": other} {
		if result := <-handle.Done; result.Status != StatusCompleted {
			t.Fatalf("%s result = %#v", name, result)
		}
	}
}

func TestManagerBoundsCrossConversationConcurrency(t *testing.T) {
	provider := newBlockingProvider()
	manager, err := NewManager(Config{Provider: provider, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	first, err := manager.Submit(testRequest("run-a", "conversation-a", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Submit(testRequest("run-b", "conversation-b", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{provider.waitStarted(t): true, provider.waitStarted(t): true}
	if !started["run-a"] || !started["run-b"] {
		t.Fatalf("initial concurrent runs = %v", started)
	}

	waiting := make(chan string, 1)
	manager.onSlotWait = func(runID string) { waiting <- runID }
	third, err := manager.Submit(testRequest("run-c", "conversation-c", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if runID := waitString(t, waiting); runID != "run-c" {
		t.Fatalf("waiting run = %q", runID)
	}
	if provider.startCount("run-c") != 0 {
		t.Fatal("third provider started without a concurrency slot")
	}
	provider.release("run-a")
	if runID := provider.waitStarted(t); runID != "run-c" {
		t.Fatalf("run after released slot = %q", runID)
	}
	provider.release("run-b")
	provider.release("run-c")
	for name, handle := range map[string]*Handle{"first": first, "second": second, "third": third} {
		if result := waitRunResult(t, handle); result.Status != StatusCompleted {
			t.Fatalf("%s result = %#v", name, result)
		}
	}
	if provider.maxConcurrent() != 2 {
		t.Fatalf("max concurrent turns = %d, want 2", provider.maxConcurrent())
	}
}

func TestManagerCancelsAndExpiresRunsWaitingForConcurrencySlot(t *testing.T) {
	for _, test := range []struct {
		name   string
		expiry func() time.Time
		cancel bool
		status Status
	}{
		{name: "cancel", expiry: func() time.Time { return time.Now().Add(time.Hour) }, cancel: true, status: StatusCanceled},
		{name: "grant expiry", expiry: func() time.Time { return time.Now().Add(50 * time.Millisecond) }, status: StatusGrantExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := newBlockingProvider()
			manager, err := NewManager(Config{Provider: provider, MaxQueue: 1, MaxConcurrentRuns: 1, Deadline: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown(context.Background())
			active, err := manager.Submit(testRequest("run-active", "conversation-a", time.Now().Add(time.Hour)))
			if err != nil {
				t.Fatal(err)
			}
			if runID := provider.waitStarted(t); runID != "run-active" {
				t.Fatalf("active run = %q", runID)
			}
			waiting := make(chan string, 1)
			manager.onSlotWait = func(runID string) { waiting <- runID }
			blocked, err := manager.Submit(testRequest("run-waiting", "conversation-b", test.expiry()))
			if err != nil {
				t.Fatal(err)
			}
			if runID := waitString(t, waiting); runID != "run-waiting" {
				t.Fatalf("waiting run = %q", runID)
			}
			if test.cancel && !manager.Cancel("run-waiting") {
				t.Fatal("Cancel() = false")
			}
			result := waitRunResult(t, blocked)
			if result.Status != test.status {
				t.Fatalf("waiting result = %#v, want %q", result, test.status)
			}
			if provider.startCount("run-waiting") != 0 {
				t.Fatal("provider started a run while it was waiting for capacity")
			}
			provider.release("run-active")
			if result := waitRunResult(t, active); result.Status != StatusCompleted {
				t.Fatalf("active result = %#v", result)
			}
		})
	}
}

func TestManagerCancelAndShutdownTerminateRuns(t *testing.T) {
	canceledSession := &recordingSession{block: make(chan struct{})}
	manager, err := NewManager(Config{Provider: &recordingProvider{session: canceledSession}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
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
	close(canceledSession.block)
	if next, err := manager.Submit(testRequest("run-after-cancel", "conversation-1", time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("Submit(after cancel) error = %v", err)
	} else if result := <-next.Done; result.Status != StatusCompleted {
		t.Fatalf("result after canceled session = %#v", result)
	}
	if manager.provider.(*recordingProvider).starts != 2 {
		t.Fatalf("provider starts after canceled session = %d, want 2", manager.provider.(*recordingProvider).starts)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	shutdownSession := &recordingSession{block: make(chan struct{})}
	shutdownManager, err := NewManager(Config{Provider: &recordingProvider{session: shutdownSession}, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
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
	return Request{ID: id, ProfileID: "work", ConversationID: conversation, EventID: "event-" + id, GrantCredential: "grant-" + id, GrantExpiresAt: expiry, AllowedMethods: []string{"message.history"}, Proxy: &testkit.FakeProxy{Response: &contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: "proxy", OK: true, Data: json.RawMessage(`{}`), Meta: &contracts.Meta{ProfileID: "work"}}}}
}

type recordingProvider struct {
	mu       sync.Mutex
	starts   int
	requests []contracts.StartRequest
	session  *recordingSession
}

type sessionRefProvider struct {
	mu       sync.Mutex
	requests []contracts.StartRequest
}

func (p *sessionRefProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if request.SessionRef == "session-missing" {
		return nil, contracts.ErrSessionNotFound
	}
	ref := request.SessionRef
	if ref == "" {
		ref = "session-new"
	}
	return &sessionRefSession{ref: ref}, nil
}

func (p *sessionRefProvider) sessionRefs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	refs := make([]string, 0, len(p.requests))
	for _, request := range p.requests {
		refs = append(refs, request.SessionRef)
	}
	return refs
}

func (p *sessionRefProvider) stateKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.requests))
	for _, request := range p.requests {
		keys = append(keys, request.StateKey)
	}
	return keys
}

type sessionRefSession struct{ ref string }

func (s *sessionRefSession) Turn(_ context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	return contracts.TurnResult{FinalText: request.RunID, SessionRef: s.ref}, nil
}
func (*sessionRefSession) Cancel(context.Context) error { return nil }
func (*sessionRefSession) Close(context.Context) error  { return nil }

type memorySessionStore struct {
	refs map[string]string
}

func (s *memorySessionStore) LoadSessionRef(_ context.Context, profileID, conversationID, provider string) (string, bool, error) {
	ref, found := s.refs[profileID+"/"+conversationID+"/"+provider]
	return ref, found, nil
}
func (s *memorySessionStore) SaveSessionRef(_ context.Context, profileID, conversationID, provider, sessionRef string) error {
	s.refs[profileID+"/"+conversationID+"/"+provider] = sessionRef
	return nil
}
func (s *memorySessionStore) DeleteSessionRef(_ context.Context, profileID, conversationID, provider string) error {
	delete(s.refs, profileID+"/"+conversationID+"/"+provider)
	return nil
}

func (p *recordingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts++
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return p.session, nil
}

type blockingProvider struct {
	mu        sync.Mutex
	started   chan string
	releases  map[string]chan struct{}
	starts    map[string]int
	active    int
	maxActive int
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{started: make(chan string, 16), releases: make(map[string]chan struct{}), starts: make(map[string]int)}
}

func (p *blockingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts[request.RunID]++
	if p.releases[request.RunID] == nil {
		p.releases[request.RunID] = make(chan struct{})
	}
	p.mu.Unlock()
	return &blockingSession{provider: p, runID: request.RunID}, nil
}

func (p *blockingProvider) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case runID := <-p.started:
		return runID
	case <-time.After(time.Second):
		t.Fatal("provider turn did not start")
		return ""
	}
}

func (p *blockingProvider) release(runID string) {
	p.mu.Lock()
	channel := p.releases[runID]
	p.mu.Unlock()
	close(channel)
}

func (p *blockingProvider) startCount(runID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts[runID]
}

func (p *blockingProvider) maxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

type blockingSession struct {
	provider *blockingProvider
	runID    string
}

func (s *blockingSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.provider.mu.Lock()
	s.provider.active++
	if s.provider.active > s.provider.maxActive {
		s.provider.maxActive = s.provider.active
	}
	release := s.provider.releases[s.runID]
	s.provider.mu.Unlock()
	s.provider.started <- s.runID
	select {
	case <-release:
	case <-ctx.Done():
		s.provider.mu.Lock()
		s.provider.active--
		s.provider.mu.Unlock()
		return contracts.TurnResult{}, ctx.Err()
	}
	s.provider.mu.Lock()
	s.provider.active--
	s.provider.mu.Unlock()
	return contracts.TurnResult{FinalText: s.runID}, nil
}

func (*blockingSession) Cancel(context.Context) error { return nil }
func (*blockingSession) Close(context.Context) error  { return nil }

func waitString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for value")
		return ""
	}
}

func waitRunResult(t *testing.T, handle *Handle) Result {
	t.Helper()
	select {
	case result := <-handle.Done:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run result")
		return Result{}
	}
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
