package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/reply"
	operationsservice "github.com/abd-im/abd-im-cli/internal/service/operations"
)

func TestOwnerRunCancellationUsesLocalRPCAndClosesProviderBoundaryE2E(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tracker, err := operationsservice.NewTracker(store)
	if err != nil {
		t.Fatal(err)
	}
	provider := newRunOperationsProvider()
	manager, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 1, Deadline: time.Minute, Observer: tracker})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	ledger, err := events.NewLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	sender := &runOperationsSender{deliveries: make(chan reply.Delivery, 1)}
	replies, err := reply.New(store, sender)
	if err != nil {
		t.Fatal(err)
	}
	method := proxy.Method{
		Name:    "message.history",
		Scope:   "message.read",
		Allowed: func() bool { return true },
		Targets: func(json.RawMessage) ([]string, error) {
			return []string{grant.ConversationTarget("conversation-1")}, nil
		},
		Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
			return json.RawMessage(`{"items":[]}`), nil
		},
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: "work",
		Ledger:    ledger,
		Replies:   replies,
		Runs:      manager,
		Grants:    grant.NewStore(),
		Methods:   []proxy.Method{method},
		Policy: daemon.PolicyFunc(func(context.Context, daemon.InboundContext) (daemon.Decision, bool, error) {
			return daemon.Decision{Principal: "provider", Methods: []string{method.Name}, RateBudget: 2}, true, nil
		}),
		GrantTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inbound.Shutdown(context.Background())
	reader, err := operationsservice.New("work", store, manager)
	if err != nil {
		t.Fatal(err)
	}
	ownerMethods, err := daemon.RunOperationOwnerMethods(reader)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := daemon.NewDispatcher("work", ownerMethods)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "daemon.sock")
	server, err := ipc.Listen(socket, dispatcher.Handle)
	if err != nil {
		t.Fatal(err)
	}
	serveContext, stopServe := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveContext) }()
	defer func() {
		stopServe()
		_ = server.Close()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("owner RPC server = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("owner RPC server did not stop")
		}
	}()

	outcome, err := inbound.Process(context.Background(), runOperationsEvent())
	if err != nil || outcome.RunID == "" {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	start := provider.waitStart(t)
	listed, err := ipc.Call(context.Background(), socket, runOperationsOwnerRequest("run-list", operationsservice.RunListMethod, `{"limit":1}`))
	if err != nil || !listed.OK || !containsRun(listed.Data, outcome.RunID, "running") {
		t.Fatalf("run.list = %#v, %v", listed, err)
	}

	denied, err := start.Proxy.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "provider-run-ops",
		ProfileID:  "work",
		Method:     operationsservice.RunListMethod,
		Params:     json.RawMessage(`{"limit":1}`),
		Grant:      start.GrantCredential,
	})
	if err != nil || denied.OK || denied.Error == nil || denied.Error.Code != contracts.CodePolicyDenied {
		t.Fatalf("provider run.list = %#v, %v", denied, err)
	}

	cancelled, err := ipc.Call(context.Background(), socket, runOperationsOwnerRequest("run-cancel", operationsservice.RunCancelMethod, `{"run_id":"`+outcome.RunID+`"}`))
	if err != nil || !cancelled.OK || !containsRun(cancelled.Data, outcome.RunID, "cancelled") {
		t.Fatalf("run.cancel = %#v, %v", cancelled, err)
	}
	provider.session.waitCanceled(t)
	assertRunStatus(t, store, outcome.RunID, control.RunCancelled)

	revoked, err := start.Proxy.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "provider-after-cancel",
		ProfileID:  "work",
		Method:     method.Name,
		Params:     json.RawMessage(`{"conversation_id":"conversation-1"}`),
		Grant:      start.GrantCredential,
	})
	if err != nil || revoked.OK || revoked.Error == nil || revoked.Error.Code != contracts.CodeGrantInvalid {
		t.Fatalf("provider call after owner cancellation = %#v, %v", revoked, err)
	}
	select {
	case delivery := <-sender.deliveries:
		t.Fatalf("owner-cancelled run replied: %#v", delivery)
	case <-time.After(50 * time.Millisecond):
	}

	const inputMarker = "crash-recovery-input-must-not-leak"
	if err := store.PutRun(context.Background(), control.Run{ID: "stranded-run", ProfileID: "work", ConversationID: "conversation-old", EventID: "event-old", Status: control.RunRunning}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutOperation(context.Background(), control.Operation{ID: "unknown-operation", ProfileID: "work", Scope: "message.send_text", IdempotencyKey: inputMarker, InputDigest: inputMarker, TargetSummary: "recipient:user-2", Status: control.OperationUnknown}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Recover(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	recovered, err := ipc.Call(context.Background(), socket, runOperationsOwnerRequest("run-list-after-recovery", operationsservice.RunListMethod, `{"limit":10}`))
	if err != nil || !recovered.OK || !containsRun(recovered.Data, "stranded-run", "interrupted") {
		t.Fatalf("recovered run.list = %#v, %v", recovered, err)
	}
	diagnostic, err := ipc.Call(context.Background(), socket, runOperationsOwnerRequest("unknown-operation", operationsservice.OperationGetMethod, `{"operation_id":"unknown-operation"}`))
	payload, marshalErr := json.Marshal(diagnostic)
	if err != nil || marshalErr != nil || !diagnostic.OK || len(diagnostic.Data) == 0 || bytes.Contains(payload, []byte(inputMarker)) {
		t.Fatalf("recovered operation diagnostic = %s, errors = %v/%v", payload, err, marshalErr)
	}
	storedOperation, err := store.OperationByID(context.Background(), "work", "unknown-operation")
	if err != nil || storedOperation.Status != control.OperationUnknown {
		t.Fatalf("unknown operation was changed/retried: %#v, %v", storedOperation, err)
	}
}

func runOperationsEvent() contracts.SDKEvent {
	payload, _ := json.Marshal(map[string]any{
		"conversation_id": "conversation-1",
		"message_id":      "message-1",
		"sender_id":       "user-2",
		"session_type":    1,
	})
	return contracts.SDKEvent{
		ProfileID:   "work",
		Type:        string(contracts.EventMessageReceived),
		OccurredAt:  time.Now(),
		DedupKey:    "run-operations-e2e",
		Data:        payload,
		MessageText: "test run operations",
	}
}

func runOperationsOwnerRequest(id, method, params string) contracts.Request {
	return contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: id, ProfileID: "work", Method: method, Params: json.RawMessage(params)}
}

func containsRun(raw json.RawMessage, id, status string) bool {
	var payload struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	if payload.ID == id && payload.Status == status {
		return true
	}
	for _, item := range payload.Items {
		if item.ID == id && item.Status == status {
			return true
		}
	}
	return false
}

func assertRunStatus(t *testing.T, store *control.Store, id string, status control.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		item, err := store.RunByID(context.Background(), "work", id)
		if err == nil && item.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	item, err := store.RunByID(context.Background(), "work", id)
	t.Fatalf("run status = %#v, %v; want %q", item, err, status)
}

type runOperationsSender struct{ deliveries chan reply.Delivery }

func (s *runOperationsSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.deliveries <- delivery
	return nil
}

type runOperationsProvider struct {
	starts  chan contracts.StartRequest
	session *runOperationsSession
}

func newRunOperationsProvider() *runOperationsProvider {
	return &runOperationsProvider{starts: make(chan contracts.StartRequest, 1), session: newRunOperationsSession()}
}

func (p *runOperationsProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.starts <- request
	return p.session, nil
}

func (p *runOperationsProvider) waitStart(t *testing.T) contracts.StartRequest {
	t.Helper()
	select {
	case request := <-p.starts:
		return request
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
		return contracts.StartRequest{}
	}
}

type runOperationsSession struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newRunOperationsSession() *runOperationsSession {
	return &runOperationsSession{started: make(chan struct{}), canceled: make(chan struct{})}
}

func (s *runOperationsSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return contracts.TurnResult{}, ctx.Err()
}

func (s *runOperationsSession) Cancel(context.Context) error {
	select {
	case <-s.canceled:
	default:
		close(s.canceled)
	}
	return nil
}

func (*runOperationsSession) Close(context.Context) error { return nil }

func (s *runOperationsSession) waitCanceled(t *testing.T) {
	t.Helper()
	select {
	case <-s.canceled:
	case <-time.After(time.Second):
		t.Fatal("provider session was not canceled")
	}
}

var _ contracts.Provider = (*runOperationsProvider)(nil)
var _ contracts.Session = (*runOperationsSession)(nil)
