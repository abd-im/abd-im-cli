package e2e

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/abd-im/abd-im-cli/internal/reply"
)

func TestPolicyChangeCancelsEventBoundRunAndRevokesProxyE2E(t *testing.T) {
	harness := newCancellationInbound(t, time.Minute)
	defer harness.close(t)
	outcome, err := harness.inbound.Process(context.Background(), cancellationEvent("policy-change"))
	if err != nil || outcome.RunID == "" {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	start := harness.provider.start(t)
	harness.provider.session.waitForTurn(t)
	if response := callCancellationTool(t, start.Proxy, start.GrantCredential); !response.OK {
		t.Fatalf("active run tool call = %+v", response)
	}
	if !harness.inbound.CancelEvent(outcome.EventID) {
		t.Fatal("CancelEvent() = false")
	}
	harness.provider.session.waitForCancel(t)
	response := waitForCanceledProxy(t, start.Proxy, start.GrantCredential)
	if response.Error == nil || response.Error.Code != contracts.CodeGrantInvalid {
		t.Fatalf("revoked run tool call = %+v", response)
	}
	assertNoCancellationReply(t, harness.sender.deliveries)
}

func TestGrantExpiryCancelsRunAndClosesProxyE2E(t *testing.T) {
	provider := newCancellationProvider()
	tools := grant.NewStore()
	_, credential, err := tools.Issue(grant.Policy{
		RunID:           "run-expired",
		ProfileID:       "work",
		Principal:       "provider",
		Methods:         []string{"message.history"},
		Scopes:          []string{"message.read"},
		TargetAllowlist: []string{"conversation-1"},
		ExpiresAt:       time.Now().Add(30 * time.Millisecond),
		RateBudget:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(tools, "run-expired", "work", []proxy.Method{cancellationMethod()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	handle, err := manager.Submit(run.Request{
		ID:              "run-expired",
		ProfileID:       "work",
		ConversationID:  "conversation-1",
		EventID:         "event-expired",
		GrantCredential: credential,
		GrantExpiresAt:  time.Now().Add(30 * time.Millisecond),
		AllowedMethods:  []string{"message.history"},
		Proxy:           tool,
		Prompt:          "inbound",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.session.waitForTurn(t)
	result := <-handle.Done
	if result.Status != run.StatusGrantExpired || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("expired run result = %#v", result)
	}
	if response := callCancellationTool(t, tool, credential); response.Error == nil || response.Error.Code != contracts.CodeGrantInvalid {
		t.Fatalf("expired run tool call = %+v", response)
	}
}

type cancellationInbound struct {
	store    *control.Store
	inbound  *daemon.Inbound
	provider *cancellationProvider
	sender   *cancellationSender
}

func newCancellationInbound(t *testing.T, grantTTL time.Duration) *cancellationInbound {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := events.NewLedger(store)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	sender := &cancellationSender{deliveries: make(chan reply.Delivery, 1)}
	replies, err := reply.New(store, sender)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	provider := newCancellationProvider()
	manager, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 1, Deadline: time.Second})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: "work",
		Ledger:    ledger,
		Replies:   replies,
		Runs:      manager,
		Grants:    grant.NewStore(),
		Methods:   []proxy.Method{cancellationMethod()},
		Policy: daemon.PolicyFunc(func(context.Context, contracts.Event) (daemon.Decision, bool, error) {
			return daemon.Decision{Principal: "provider", Methods: []string{"message.history"}, RateBudget: 5}, true, nil
		}),
		GrantTTL: grantTTL,
	})
	if err != nil {
		_ = manager.Shutdown(context.Background())
		_ = store.Close()
		t.Fatal(err)
	}
	return &cancellationInbound{store: store, inbound: inbound, provider: provider, sender: sender}
}

func (h *cancellationInbound) close(t *testing.T) {
	t.Helper()
	if err := h.inbound.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
}

func cancellationMethod() proxy.Method {
	return proxy.Method{
		Name:  "message.history",
		Scope: "message.read",
		Targets: func(raw json.RawMessage) ([]string, error) {
			var input struct {
				ConversationID string `json:"conversation_id"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			return []string{input.ConversationID}, nil
		},
		Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
			return json.RawMessage(`{"items":[]}`), nil
		},
	}
}

func cancellationEvent(dedupKey string) contracts.SDKEvent {
	data, _ := json.Marshal(map[string]any{
		"conversation_id": "conversation-1",
		"message_id":      "message-trigger",
		"sender_id":       "user-2",
		"session_type":    1,
	})
	return contracts.SDKEvent{ProfileID: "work", Type: string(contracts.EventMessageReceived), OccurredAt: time.Now().UTC(), DedupKey: dedupKey, Data: data, MessageText: "inbound"}
}

func callCancellationTool(t *testing.T, tool contracts.ToolProxy, credential string) contracts.Response {
	t.Helper()
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "tool-call",
		ProfileID:  "work",
		Method:     "message.history",
		Params:     json.RawMessage(`{"conversation_id":"conversation-1","limit":1}`),
		Grant:      credential,
	})
	if err != nil {
		t.Fatalf("tool Call() error = %v", err)
	}
	return response
}

func waitForCanceledProxy(t *testing.T, tool contracts.ToolProxy, credential string) contracts.Response {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response := callCancellationTool(t, tool, credential)
		if response.Error != nil && response.Error.Code == contracts.CodeGrantInvalid {
			return response
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run-private proxy remained usable after cancellation")
	return contracts.Response{}
}

func assertNoCancellationReply(t *testing.T, deliveries <-chan reply.Delivery) {
	t.Helper()
	select {
	case delivery := <-deliveries:
		t.Fatalf("canceled run replied: %#v", delivery)
	case <-time.After(50 * time.Millisecond):
	}
}

type cancellationSender struct{ deliveries chan reply.Delivery }

func (s *cancellationSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.deliveries <- delivery
	return nil
}

type cancellationProvider struct {
	starts  chan contracts.StartRequest
	session *cancellationSession
}

func newCancellationProvider() *cancellationProvider {
	return &cancellationProvider{starts: make(chan contracts.StartRequest, 1), session: newCancellationSession()}
}

func (p *cancellationProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.starts <- request
	return p.session, nil
}

func (p *cancellationProvider) start(t *testing.T) contracts.StartRequest {
	t.Helper()
	select {
	case request := <-p.starts:
		return request
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
		return contracts.StartRequest{}
	}
}

type cancellationSession struct {
	started  chan struct{}
	canceled chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func newCancellationSession() *cancellationSession {
	return &cancellationSession{started: make(chan struct{}), canceled: make(chan struct{}), closed: make(chan struct{})}
}

func (s *cancellationSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return contracts.TurnResult{}, ctx.Err()
}

func (s *cancellationSession) Cancel(context.Context) error {
	select {
	case <-s.canceled:
	default:
		close(s.canceled)
	}
	return nil
}

func (s *cancellationSession) Close(context.Context) error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func (s *cancellationSession) waitForTurn(t *testing.T) {
	t.Helper()
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("provider turn did not start")
	}
}

func (s *cancellationSession) waitForCancel(t *testing.T) {
	t.Helper()
	select {
	case <-s.canceled:
	case <-time.After(time.Second):
		t.Fatal("provider session was not canceled")
	}
}

var _ contracts.Provider = (*cancellationProvider)(nil)
var _ contracts.Session = (*cancellationSession)(nil)
