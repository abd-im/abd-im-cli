package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/reply"
	"github.com/abd-im/abd-im-cli/internal/testkit"
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
		if delivery.ConversationID != "conversation-original" || delivery.TriggerMessageID != "message-trigger" || delivery.RecipientID != "user-2" || delivery.Text != "final response" {
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
	if inbound := harness.decidedEvent(); inbound.Event.EventID != first.EventID || inbound.Event.Sequence == 0 || inbound.SenderID != "user-2" || inbound.ConversationID != "conversation-original" || inbound.SessionType != 1 {
		t.Fatalf("policy inbound context = %#v, want persisted event %q", inbound, first.EventID)
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
	if !strings.Contains(harness.session.prompt(), "message body marker") {
		t.Fatalf("provider prompt = %q, want inbound message text", harness.session.prompt())
	}
	page, err := harness.ledger.List(context.Background(), "work", "", 10)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("ledger.List() = %#v, %v", page, err)
	}
	payload, _ := json.Marshal(page.Events)
	if strings.Contains(string(payload), "message body marker") {
		t.Fatalf("ledger persisted inbound message text: %s", payload)
	}
}

func TestInboundReplyOnlyRunExposesNoTypedTools(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	harness.setPolicy(Decision{Principal: "openim:user-2", RateBudget: 1}, true)
	harness.session.setReplyOnly()

	outcome, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-reply-only", "conversation-original", "message-trigger"))
	if err != nil || outcome.RunID == "" || outcome.Ignored {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	select {
	case delivery := <-harness.sender.deliveries:
		if delivery.ConversationID != "conversation-original" || delivery.RecipientID != "user-2" {
			t.Fatalf("reply-only delivery = %#v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("reply-only run did not produce a reply")
	}
	if !harness.session.deniedThirdParty() {
		t.Fatal("reply-only run exposed a typed tool")
	}
	if methods := harness.provider.allowedMethods(); len(methods) != 0 {
		t.Fatalf("reply-only provider methods = %v, want empty", methods)
	}
	if strings.Contains(harness.session.prompt(), "Use only the abdim CLI") {
		t.Fatalf("reply-only prompt advertised CLI access: %q", harness.session.prompt())
	}
}

func TestInboundStreamsProviderOutputIntoOneEventBoundMessage(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	harness.session.setReplyOnly()
	harness.session.setOutputUpdates("hel", "hello", "hello world")
	if _, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-stream", "conversation-original", "message-trigger")); err != nil {
		t.Fatal(err)
	}
	select {
	case delivery := <-harness.sender.deliveries:
		if delivery.Text != "hello world" || delivery.ConversationID != "conversation-original" {
			t.Fatalf("stream delivery = %#v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not finish")
	}
	harness.sender.mu.Lock()
	starts, appends, ended := harness.sender.count, harness.sender.streamAppendCount, harness.sender.streamEnded
	harness.sender.mu.Unlock()
	if starts != 1 || appends != 2 || !ended {
		t.Fatalf("stream starts=%d appends=%d ended=%t", starts, appends, ended)
	}
}

func TestInboundCanBoundHistoryBeforeTrigger(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	harness.setPolicy(Decision{Principal: "openim:user-2", Methods: []string{"message.history"}, HistoryBeforeTrigger: true, RateBudget: 2}, true)

	if _, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-prior-history", "conversation-original", "message-trigger")); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	select {
	case <-harness.sender.deliveries:
	case <-time.After(time.Second):
		t.Fatal("inbound run did not complete")
	}
	window := harness.window()
	if window.ConversationID != "conversation-original" || window.AfterMessageID != "" || window.BeforeMessageID != "message-trigger" {
		t.Fatalf("grant window = %#v", window)
	}
}

func TestInboundShutdownWaitsForReplyPersistence(t *testing.T) {
	harness := newHarness(t, false)
	release := make(chan struct{})
	harness.sender.release = release
	released := false
	defer func() {
		if !released {
			close(release)
		}
		harness.close(t)
	}()

	if _, err := harness.inbound.Process(context.Background(), inboundEvent("sdk-shutdown-reply", "conversation-original", "message-trigger")); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	select {
	case <-harness.sender.deliveries:
	case <-time.After(time.Second):
		t.Fatal("inbound run did not reach reply sender")
	}
	done := make(chan error, 1)
	go func() { done <- harness.inbound.Shutdown(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown() returned before reply persistence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
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

func TestProviderVisibleMethodsFreezesFixedRegistry(t *testing.T) {
	methods := []proxy.Method{
		{Name: "group.get"},
		{Name: "message.history"},
		{Name: "group.create"},
		{Name: "group.delete"},
	}
	visible := providerVisibleMethods(methods)
	if len(visible) != 4 || visible[0] != "group.get" || visible[1] != "message.history" || visible[2] != "group.create" || visible[3] != "group.delete" {
		t.Fatalf("providerVisibleMethods() = %v", visible)
	}
}

type harness struct {
	store    *control.Store
	ledger   *events.Ledger
	inbound  *Inbound
	sender   *recordingSender
	provider *recordingProvider
	session  *recordingSession

	mu       sync.Mutex
	decision Decision
	allowed  bool
	policies int
	decided  InboundContext
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
		ledger:   ledger,
		sender:   sender,
		provider: provider,
		session:  session,
		decision: Decision{Principal: "provider", Methods: []string{"message.history"}, RateBudget: 2},
		allowed:  true,
	}
	reader := proxy.Method{
		Name: "message.history",

		Handle: func(_ context.Context, request contracts.Request, access grant.Grant) (json.RawMessage, error) {
			h.mu.Lock()
			h.windowed = access.MessageWindow
			h.mu.Unlock()
			var input struct {
				ConversationID string `json:"conversation_id"`
			}
			if json.Unmarshal(request.Params, &input) != nil || input.ConversationID != access.MessageWindow.ConversationID {
				return nil, proxy.Failure(contracts.CodePolicyDenied, "conversation is outside the run window")
			}
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
		Policy: PolicyFunc(func(_ context.Context, inbound InboundContext) (Decision, bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.policies++
			h.decided = inbound
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

func (h *harness) decidedEvent() InboundContext {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.decided
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

func inboundEvent(dedup, conversationID, messageID string) contracts.SDKEvent {
	data, _ := json.Marshal(map[string]any{"conversation_id": conversationID, "message_id": messageID, "sender_id": "user-2", "session_type": 1})
	return contracts.SDKEvent{
		ProfileID:   "work",
		Type:        string(contracts.EventMessageReceived),
		OccurredAt:  time.Now().UTC(),
		DedupKey:    dedup,
		Data:        data,
		MessageText: "message body marker",
	}
}

type recordingSender struct {
	mu                sync.Mutex
	count             int
	deliveries        chan reply.Delivery
	release           <-chan struct{}
	stream            reply.StreamDelivery
	streamText        string
	streamAppendCount int
	streamEnded       bool
}

func (s *recordingSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	s.deliveries <- delivery
	if s.release != nil {
		<-s.release
	}
	return nil
}

func (s *recordingSender) StartStream(_ context.Context, delivery reply.StreamDelivery) (reply.StreamRef, error) {
	s.mu.Lock()
	s.count++
	s.stream = delivery
	s.streamText = delivery.Content
	s.mu.Unlock()
	return reply.StreamRef{ConversationID: delivery.ConversationID, ClientMsgID: delivery.ClientMsgID}, nil
}

func (s *recordingSender) AppendStream(_ context.Context, appendValue reply.StreamAppend) error {
	s.mu.Lock()
	for _, packet := range appendValue.Packets {
		s.streamText += packet
	}
	if len(appendValue.Packets) > 0 {
		s.streamAppendCount++
	}
	if !appendValue.End {
		s.mu.Unlock()
		return nil
	}
	delivery := reply.Delivery{
		ProfileID: s.stream.ProfileID, EventID: s.stream.EventID,
		ConversationID: s.stream.ConversationID, RecipientID: s.stream.RecipientID,
		TriggerMessageID: s.stream.TriggerMessageID, GroupID: s.stream.GroupID,
		OperationID: s.stream.ClientMsgID, Text: s.streamText,
	}
	s.streamEnded = true
	release := s.release
	s.mu.Unlock()
	s.deliveries <- delivery
	if release != nil {
		<-release
	}
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
	methods []string
	session *recordingSession
}

func (p *recordingProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.starts++
	p.methods = append([]string(nil), request.AllowedMethods...)
	p.mu.Unlock()
	p.session.setProxy(request.Proxy)
	return p.session, nil
}

func (p *recordingProvider) allowedMethods() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.methods...)
}

func (p *recordingProvider) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts
}

type recordingSession struct {
	mu            sync.Mutex
	proxy         contracts.ToolProxy
	turns         int
	lastPrompt    string
	denied        bool
	replyOnly     bool
	outputUpdates []string
	block         bool
	started       chan struct{}
}

func (s *recordingSession) setProxy(value contracts.ToolProxy) {
	s.mu.Lock()
	s.proxy = value
	s.mu.Unlock()
}

func (s *recordingSession) Turn(ctx context.Context, turn contracts.TurnRequest) (contracts.TurnResult, error) {
	s.mu.Lock()
	s.turns++
	s.lastPrompt = turn.Prompt
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
	replyOnly := s.replyOnly
	outputUpdates := append([]string(nil), s.outputUpdates...)
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
	if replyOnly {
		for _, update := range outputUpdates {
			if turn.Output == nil {
				return contracts.TurnResult{}, errors.New("provider output sink is unavailable")
			}
			if err := turn.Output(ctx, contracts.TurnOutput{Text: update}); err != nil {
				return contracts.TurnResult{}, err
			}
		}
		final := "final response"
		if len(outputUpdates) > 0 {
			final = outputUpdates[len(outputUpdates)-1]
		}
		return contracts.TurnResult{FinalText: final}, nil
	}
	response, err := s.call(ctx, proxyValue, turn, "conversation-original")
	if err != nil || !response.OK {
		return contracts.TurnResult{}, errors.New("trigger conversation was not readable")
	}
	return contracts.TurnResult{FinalText: "final response"}, nil
}

func (s *recordingSession) setReplyOnly() {
	s.mu.Lock()
	s.replyOnly = true
	s.mu.Unlock()
}

func (s *recordingSession) setOutputUpdates(updates ...string) {
	s.mu.Lock()
	s.outputUpdates = append([]string(nil), updates...)
	s.mu.Unlock()
}

func (s *recordingSession) prompt() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPrompt
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
