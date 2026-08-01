package operations

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
)

func TestTrackerPersistsRunLifecycleAndServiceCancels(t *testing.T) {
	store := openStore(t)
	tracker, err := NewTracker(store)
	if err != nil {
		t.Fatal(err)
	}
	provider := &blockingProvider{session: newBlockingSession()}
	manager, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 2, Deadline: time.Minute, Observer: tracker})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	reader, err := New("work", store, manager)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &trackingProxy{}
	handle, err := manager.Submit(run.Request{
		ID:              "run-1",
		ProfileID:       "work",
		ConversationID:  "conversation-1",
		EventID:         "event-1",
		GrantCredential: "grant-1",
		GrantExpiresAt:  time.Now().Add(time.Hour),
		AllowedMethods:  []string{"message.history"},
		Proxy:           proxy,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	provider.session.waitStarted(t)

	listed, err := reader.List(context.Background(), ListInput{Limit: 1})
	if err != nil || len(listed.Data.Items) != 1 || listed.Data.Items[0].Status != string(control.RunRunning) {
		t.Fatalf("List() = %#v, %v", listed, err)
	}
	if listed.Meta.Capability.Method != RunListMethod {
		t.Fatalf("run.list metadata = %#v", listed.Meta)
	}
	cancelled, err := reader.Cancel(context.Background(), CancelInput{RunID: "run-1"})
	if err != nil || cancelled.Data.Status != string(control.RunCancelled) || cancelled.Data.Reason != "owner cancelled" {
		t.Fatalf("Cancel() = %#v, %v", cancelled, err)
	}
	if cancelled.Meta.Capability.Method != RunCancelMethod {
		t.Fatalf("run.cancel metadata = %#v", cancelled.Meta)
	}
	provider.session.waitCanceled(t)
	if result := <-handle.Done; result.Status != run.StatusCanceled {
		t.Fatalf("run result = %#v", result)
	}
	if proxy.closeCount() == 0 {
		t.Fatal("owner cancellation did not close the run-private proxy")
	}
	stored, err := store.RunByID(context.Background(), "work", "run-1")
	if err != nil || stored.Status != control.RunCancelled || stored.Reason != "owner cancelled" {
		t.Fatalf("stored run = %#v, %v", stored, err)
	}
}

func TestServicePaginatesRunsAndRedactsOperationDiagnostics(t *testing.T) {
	store := openStore(t)
	for _, id := range []string{"run-1", "run-2"} {
		if err := store.PutRun(context.Background(), control.Run{ID: id, ProfileID: "work", ConversationID: "conversation-" + id, EventID: "event-" + id, Status: control.RunCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	const marker = "operation-input-must-not-leak"
	if err := store.PutOperation(context.Background(), control.Operation{
		ID:             "operation-1",
		ProfileID:      "work",
		Scope:          "message.send_text",
		IdempotencyKey: marker,
		InputDigest:    marker,
		TargetSummary:  "recipient:user-2",
		Status:         control.OperationFailed,
		ErrorSummary:   "remote action failed",
	}); err != nil {
		t.Fatal(err)
	}
	reader, err := New("work", store, fakeCanceler{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.List(context.Background(), ListInput{Limit: 1})
	if err != nil || len(first.Data.Items) != 1 || first.Data.NextCursor == "" {
		t.Fatalf("first List() = %#v, %v", first, err)
	}
	second, err := reader.List(context.Background(), ListInput{Limit: 1, Cursor: first.Data.NextCursor})
	if err != nil || len(second.Data.Items) != 1 || second.Data.NextCursor != "" || second.Data.Items[0].ID == first.Data.Items[0].ID {
		t.Fatalf("second List() = %#v, %v", second, err)
	}
	diagnostic, err := reader.Operation(context.Background(), OperationInput{OperationID: "operation-1"})
	if err != nil || diagnostic.Data.TargetSummary != "recipient:user-2" || diagnostic.Data.ErrorSummary != "remote action failed" {
		t.Fatalf("Operation() = %#v, %v", diagnostic, err)
	}
	if diagnostic.Meta.Capability.Method != OperationGetMethod {
		t.Fatalf("operation.get metadata = %#v", diagnostic.Meta)
	}
	payload, err := json.Marshal(diagnostic.Data)
	if err != nil || string(payload) == "" || strings.Contains(string(payload), marker) {
		t.Fatalf("operation diagnostic leaked input = %s, %v", payload, err)
	}
	marked, err := reader.MarkOperationUnknown(context.Background(), OperationInput{OperationID: "operation-1"})
	if err != nil || marked.Data.Status != string(control.OperationUnknown) || marked.Data.ErrorSummary != "" {
		t.Fatalf("MarkOperationUnknown() = %#v, %v", marked, err)
	}
	if marked.Meta.Capability.Method != OperationMarkUnknownMethod {
		t.Fatalf("operation.mark_unknown metadata = %#v", marked.Meta)
	}
	if _, err := reader.Cancel(context.Background(), CancelInput{RunID: "missing"}); !errors.Is(err, control.ErrNotFound) {
		t.Fatalf("Cancel(missing) error = %v, want ErrNotFound", err)
	}
}

func TestTrackerRecoveryOnlyInterruptsActiveRuns(t *testing.T) {
	store := openStore(t)
	for _, item := range []control.Run{
		{ID: "queued", ProfileID: "work", ConversationID: "conversation-1", EventID: "event-1", Status: control.RunQueued},
		{ID: "running", ProfileID: "work", ConversationID: "conversation-2", EventID: "event-2", Status: control.RunRunning},
		{ID: "completed", ProfileID: "work", ConversationID: "conversation-3", EventID: "event-3", Status: control.RunCompleted},
	} {
		if err := store.PutRun(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	tracker, err := NewTracker(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Recover(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]control.RunStatus{"queued": control.RunInterrupted, "running": control.RunInterrupted, "completed": control.RunCompleted} {
		item, err := store.RunByID(context.Background(), "work", id)
		if err != nil || item.Status != want {
			t.Fatalf("RunByID(%q) = %#v, %v; want %q", id, item, err, want)
		}
	}
}

func openStore(t *testing.T) *control.Store {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type fakeCanceler struct{}

func (fakeCanceler) Cancel(string) bool { return false }

type trackingProxy struct {
	mu     sync.Mutex
	closes int
}

func (p *trackingProxy) Call(context.Context, contracts.Request) (contracts.Response, error) {
	return contracts.Response{}, nil
}

func (p *trackingProxy) Close(context.Context) error {
	p.mu.Lock()
	p.closes++
	p.mu.Unlock()
	return nil
}

func (p *trackingProxy) closeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

type blockingProvider struct{ session *blockingSession }

func (p *blockingProvider) Start(context.Context, contracts.StartRequest) (contracts.Session, error) {
	return p.session, nil
}

type blockingSession struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newBlockingSession() *blockingSession {
	return &blockingSession{started: make(chan struct{}), canceled: make(chan struct{})}
}

func (s *blockingSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return contracts.TurnResult{}, ctx.Err()
}

func (s *blockingSession) Cancel(context.Context) error {
	select {
	case <-s.canceled:
	default:
		close(s.canceled)
	}
	return nil
}

func (*blockingSession) Close(context.Context) error { return nil }

func (s *blockingSession) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("provider turn did not start")
	}
}

func (s *blockingSession) waitCanceled(t *testing.T) {
	t.Helper()
	select {
	case <-s.canceled:
	case <-time.After(time.Second):
		t.Fatal("provider session was not canceled")
	}
}

var _ contracts.Provider = (*blockingProvider)(nil)
var _ contracts.Session = (*blockingSession)(nil)
var _ contracts.ToolProxy = (*trackingProxy)(nil)
