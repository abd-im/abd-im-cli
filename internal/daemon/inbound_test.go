package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/agent/proxy"
	"github.com/abd-im-cli/abdim-cli/internal/agent/run"
	"github.com/abd-im-cli/abdim-cli/internal/bridge"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/control"
	"github.com/abd-im-cli/abdim-cli/internal/events"
	"github.com/abd-im-cli/abdim-cli/internal/reply"
	"github.com/abd-im-cli/abdim-cli/internal/testkit"
)

func TestInboundCreatesOneRunAndOnlyRepliesToTriggerConversation(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	event := inboundEvent("sdk-message-1", "conversation-original", "message-trigger")

	first, err := harness.inbound.Process(context.Background(), event)
	if err != nil || !first.Created || first.Ignored || first.RunID == "" {
		t.Fatalf("first Process() = %#v, %v", first, err)
	}
	select {
	case delivery := <-harness.sender.deliveries:
		if delivery.ConversationID != "conversation-original" || delivery.TriggerMessageID != "message-trigger" || delivery.Text != "final response" {
			t.Fatalf("reply delivery = %#v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("inbound run did not deliver a reply")
	}

	duplicate, err := harness.inbound.Process(context.Background(), event)
	if err != nil || duplicate.Created || !duplicate.Ignored || duplicate.EventID != first.EventID {
		t.Fatalf("duplicate Process() = %#v, %v", duplicate, err)
	}
	if harness.policyCalls() != 1 || harness.provider.startCount() != 1 || harness.session.turnCount() != 1 || harness.sender.calls() != 1 {
		t.Fatalf("calls policy=%d provider=%d turns=%d replies=%d", harness.policyCalls(), harness.provider.startCount(), harness.session.turnCount(), harness.sender.calls())
	}
	if !harness.session.deniedThirdParty() {
		t.Fatal("provider was allowed to read a third-party conversation")
	}
	window := harness.window()
	if window.ConversationID != "conversation-original" || window.AfterMessageID != "message-trigger" || window.BeforeMessageID != "" {
		t.Fatalf("grant window = %#v", window)
	}
	if _, err := harness.store.ReplySlotByEvent(context.Background(), "work", first.EventID); err != nil {
		t.Fatalf("reply slot was not persisted before provider execution: %v", err)
	}
}

func TestInboundListenerRunsBehindReadyLoginManager(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	sdk := &testkit.FakeSDK{}
	manager, err := bridge.NewLoginMgr(func() contracts.SDK { return sdk }, filepath.Join(t.TempDir(), "profile.lock"), harness.inbound.Listener)
	if err != nil {
		t.Fatalf("bridge.NewLoginMgr() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil || manager.State() != bridge.StateReady {
		t.Fatalf("LoginMgr.Start() state=%q err=%v", manager.State(), err)
	}
	defer manager.Shutdown(context.Background())
	if err := sdk.Emit(context.Background(), inboundEvent("sdk-via-bridge", "conversation-original", "message-trigger")); err != nil {
		t.Fatalf("FakeSDK.Emit() error = %v", err)
	}
	select {
	case delivery := <-harness.sender.deliveries:
		if delivery.ConversationID != "conversation-original" {
			t.Fatalf("listener reply target = %#v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge listener did not produce a reply")
	}
}

func TestInboundCancellationRevokesRunBeforeReply(t *testing.T) {
	harness := newHarness(t, true)
	defer harness.close(t)
	outcome, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-message-cancel", "conversation-1", "message-1"))
	if err != nil || outcome.RunID == "" {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	if !harness.session.waitForTurn(time.Second) {
		t.Fatal("provider turn did not start")
	}
	if !harness.inbound.CancelEvent(outcome.EventID) {
		t.Fatal("CancelEvent() = false")
	}
	select {
	case delivery := <-harness.sender.deliveries:
		t.Fatalf("canceled run sent reply %#v", delivery)
	case <-time.After(50 * time.Millisecond):
	}
	if harness.sender.calls() != 0 {
		t.Fatalf("canceled run reply count = %d", harness.sender.calls())
	}
}

func TestInboundRejectsPolicyMethodsOutsideStaticRegistry(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	harness.setPolicy(Decision{Principal: "provider", Methods: []string{"daemon.shutdown"}, RateBudget: 1}, true)
	if _, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-message-policy", "conversation-1", "message-1")); err == nil {
		t.Fatal("Process() accepted a policy-selected controller method")
	}
	if harness.provider.startCount() != 0 || harness.sender.calls() != 0 {
		t.Fatalf("rejected policy started provider=%d replies=%d", harness.provider.startCount(), harness.sender.calls())
	}
}

type harness struct {
	store    *control.Store
	inbound  *Inbound
	sender   *recordingSender
	provider *recordingProvider
	session  *recordingSession

	mu       sync.Mutex
	decision Decision
	allowed  bool
	policies int
	windowed grant.MessageWindow
}

func newHarness(t *testing.T, blockTurn bool) *harness {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("control.Open() error = %v", err)
	}
	ledger, err := events.NewLedger(store)
	if err != nil {
		_ = store.Close()
		t.Fatalf("events.NewLedger() error = %v", err)
	}
	sender := &recordingSender{deliveries: make(chan reply.Delivery, 2)}
	replies, err := reply.New(store, sender)
	if err != nil {
		_ = store.Close()
		t.Fatalf("reply.New() error = %v", err)
	}
	session := &recordingSession{block: blockTurn}
	provider := &recordingProvider{session: session}
	runs, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 2, Deadline: time.Second})
	if err != nil {
		_ = store.Close()
		t.Fatalf("run.NewManager() error = %v", err)
	}
	h := &harness{
		store:    store,
		sender:   sender,
		provider: provider,
		session:  session,
		decision: Decision{Principal: "provider", Methods: []string{"message.history"}, RateBudget: 2},
		allowed:  true,
	}
	reader := proxy.Method{
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
		Handle: func(_ context.Context, _ contracts.Request, access grant.Grant) (json.RawMessage, error) {
			h.mu.Lock()
			h.windowed = access.MessageWindow
			h.mu.Unlock()
			return json.RawMessage(`{"items":[]}`), nil
		},
	}
	inbound, err := New(Config{
		ProfileID: "work",
		Ledger:    ledger,
		Replies:   replies,
		Runs:      runs,
		Grants:    grant.NewStore(),
		Methods:   []proxy.Method{reader},
		Policy: PolicyFunc(func(context.Context, contracts.Event) (Decision, bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.policies++
			return h.decision, h.allowed, nil
		}),
		GrantTTL: time.Minute,
	})
	if err != nil {
		_ = runs.Shutdown(context.Background())
		_ = store.Close()
		t.Fatalf("daemon.New() error = %v", err)
	}
	h.inbound = inbound
	return h
}

func (h *harness) close(t *testing.T) {
	t.Helper()
	if err := h.inbound.Shutdown(context.Background()); err != nil {
		t.Fatalf("Inbound.Shutdown() error = %v", err)
	}
	if err := h.store.Close(); err != nil {
		t.Fatalf("control.Store.Close() error = %v", err)
	}
}

func (h *harness) policyCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.policies
}

func (h *harness) window() grant.MessageWindow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.windowed
}

func (h *harness) setPolicy(decision Decision, allowed bool) {
	h.mu.Lock()
	h.decision = decision
	h.allowed = allowed
	h.mu.Unlock()
}

func inboundEvent(dedup, conversationID, messageID string) contracts.Event {
	data, _ := json.Marshal(map[string]string{"conversation_id": conversationID, "message_id": messageID})
	return contracts.Event{
		APIVersion: contracts.APIVersionV1,
		EventID:    "sdk-event-" + dedup,
		ProfileID:  "work",
		Sequence:   1,
		Type:       string(contracts.EventMessageReceived),
		OccurredAt: time.Now().UTC(),
		DedupKey:   dedup,
		Data:       data,
	}
}

type recordingSender struct {
	mu         sync.Mutex
	count      int
	deliveries chan reply.Delivery
}

func (s *recordingSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	s.deliveries <- delivery
	return nil
}

func (s *recordingSender) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

type recordingProvider struct {
	mu      sync.Mutex
	starts  int
	session *recordingSession
}

func (p *recordingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts++
	p.mu.Unlock()
	p.session.setProxy(request.Proxy)
	return p.session, nil
}

func (p *recordingProvider) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts
}

type recordingSession struct {
	mu      sync.Mutex
	proxy   contracts.ToolProxy
	turns   int
	denied  bool
	block   bool
	started chan struct{}
}

func (s *recordingSession) setProxy(value contracts.ToolProxy) {
	s.mu.Lock()
	s.proxy = value
	s.mu.Unlock()
}

func (s *recordingSession) Turn(ctx context.Context, turn contracts.TurnRequest) (contracts.TurnResult, error) {
	s.mu.Lock()
	s.turns++
	proxyValue := s.proxy
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
	if block {
		<-ctx.Done()
		return contracts.TurnResult{}, ctx.Err()
	}
	if response, err := s.call(ctx, proxyValue, turn, "conversation-third-party"); err != nil || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
		return contracts.TurnResult{}, errors.New("third-party conversation was not denied")
	} else {
		s.mu.Lock()
		s.denied = true
		s.mu.Unlock()
	}
	response, err := s.call(ctx, proxyValue, turn, "conversation-original")
	if err != nil || !response.OK {
		return contracts.TurnResult{}, errors.New("trigger conversation was not readable")
	}
	return contracts.TurnResult{FinalText: "final response"}, nil
}

func (s *recordingSession) call(ctx context.Context, value contracts.ToolProxy, turn contracts.TurnRequest, conversationID string) (contracts.Response, error) {
	params, _ := json.Marshal(map[string]string{"conversation_id": conversationID})
	return value.Call(ctx, contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "provider-read-" + conversationID, ProfileID: "work", Method: "message.history", Params: params, Grant: turn.GrantCredential})
}

func (s *recordingSession) Cancel(context.Context) error { return nil }
func (s *recordingSession) Close(context.Context) error  { return nil }

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

func (s *recordingSession) turnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns
}

func (s *recordingSession) deniedThirdParty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.denied
}
