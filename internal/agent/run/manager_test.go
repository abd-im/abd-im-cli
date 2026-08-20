package run

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestManagerSerializesOneConversationAndResumesSession(t *testing.T) {
	provider := &recordingProvider{}
	store := &memorySessions{}
	manager, err := NewManager(Config{
		Provider: provider, Sessions: store, SessionNamespace: "codex",
		MaxQueue: 2, MaxConcurrentRuns: 2, Deadline: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Submit(request("run-1", "bot:conversation-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Submit(request("run-2", "bot:conversation-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result := <-first.Done; result.Status != StatusCompleted {
		t.Fatalf("first result = %#v", result)
	}
	if result := <-second.Done; result.Status != StatusCompleted {
		t.Fatalf("second result = %#v", result)
	}
	starts := provider.Starts()
	if len(starts) != 2 || starts[0].SessionRef != "" || starts[1].SessionRef != "session-run-1" {
		t.Fatalf("provider starts = %#v", starts)
	}
}

func TestManagerRunsDifferentConversationsConcurrently(t *testing.T) {
	provider := newBlockingProvider()
	manager, err := NewManager(Config{Provider: provider, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := manager.Submit(request("run-1", "bot:conversation-1"))
	second, _ := manager.Submit(request("run-2", "user:conversation-1"))
	for i := 0; i < 2; i++ {
		select {
		case <-provider.started:
		case <-time.After(time.Second):
			t.Fatal("provider turns did not start concurrently")
		}
	}
	close(provider.release)
	if (<-first.Done).Status != StatusCompleted || (<-second.Done).Status != StatusCompleted {
		t.Fatal("concurrent turns did not complete")
	}
}

func TestManagerDeadlineAndCancellation(t *testing.T) {
	provider := newBlockingProvider()
	manager, err := NewManager(Config{Provider: provider, MaxQueue: 1, MaxConcurrentRuns: 1, Deadline: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	deadline, _ := manager.Submit(request("deadline", "bot:one"))
	if result := <-deadline.Done; result.Status != StatusDeadline {
		t.Fatalf("deadline result = %#v", result)
	}
	canceled, _ := manager.Submit(request("cancel", "bot:two"))
	if !manager.Cancel("cancel") {
		t.Fatal("Cancel() did not find run")
	}
	if result := <-canceled.Done; result.Status != StatusCanceled {
		t.Fatalf("canceled result = %#v", result)
	}
}

func request(id, conversation string) Request {
	return Request{ID: id, ProfileID: "work", ConversationID: conversation, EventID: "event-" + id, Prompt: "reply"}
}

type recordingProvider struct {
	mu     sync.Mutex
	starts []contracts.StartRequest
}

func (p *recordingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts = append(p.starts, request)
	p.mu.Unlock()
	return staticSession{result: contracts.TurnResult{FinalText: "done", SessionRef: "session-" + request.RunID}}, nil
}

func (p *recordingProvider) Starts() []contracts.StartRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]contracts.StartRequest(nil), p.starts...)
}

type staticSession struct{ result contracts.TurnResult }

func (s staticSession) Turn(context.Context, contracts.TurnRequest) (contracts.TurnResult, error) {
	return s.result, nil
}
func (staticSession) Cancel(context.Context) error { return nil }
func (staticSession) Close(context.Context) error  { return nil }

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{started: make(chan struct{}, 4), release: make(chan struct{})}
}

func (p *blockingProvider) Start(context.Context, contracts.StartRequest) (contracts.Session, error) {
	return blockingSession{provider: p}, nil
}

type blockingSession struct{ provider *blockingProvider }

func (s blockingSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.provider.started <- struct{}{}
	select {
	case <-s.provider.release:
		return contracts.TurnResult{FinalText: "done"}, nil
	case <-ctx.Done():
		return contracts.TurnResult{}, ctx.Err()
	}
}
func (blockingSession) Cancel(context.Context) error { return nil }
func (blockingSession) Close(context.Context) error  { return nil }

type memorySessions struct {
	mu    sync.Mutex
	value string
}

func (s *memorySessions) LoadSessionRef(context.Context, string, string, string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.value != "", nil
}
func (s *memorySessions) SaveSessionRef(_ context.Context, _, _, _, value string) error {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
	return nil
}
func (s *memorySessions) DeleteSessionRef(context.Context, string, string, string) error {
	s.mu.Lock()
	s.value = ""
	s.mu.Unlock()
	return nil
}
