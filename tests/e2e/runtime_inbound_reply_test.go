package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"github.com/abd-im/abd-im-cli/internal/reply"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestRuntimeInboundReplyE2E(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "runtime", "daemon.lock")
	primary := newRuntimeHarness(t, filepath.Join(root, "primary.db"), lockPath, filepath.Join(root, "runtime", "daemon.sock"))
	defer primary.close(t)
	primary.start(t)

	secondary := newRuntimeHarness(t, filepath.Join(root, "secondary.db"), lockPath, filepath.Join(root, "secondary", "daemon.sock"))
	defer secondary.close(t)
	if err := secondary.runtime.Start(context.Background()); !errors.Is(err, profile.ErrLocked) {
		t.Fatalf("second runtime Start() error = %v, want profile lock error", err)
	}
	if secondary.runtime.State() != bridge.StateStopped || len(secondary.sdk.Steps()) != 0 {
		t.Fatalf("second runtime state/SDK steps = %q/%v", secondary.runtime.State(), secondary.sdk.Steps())
	}
	if _, err := os.Lstat(secondary.socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second runtime created a socket: %v", err)
	}

	response, err := ipc.Call(context.Background(), primary.socketPath, contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "e2e-profile",
		ProfileID:  "work",
		Method:     "profile.get",
		Params:     json.RawMessage(`{}`),
	})
	if err != nil || !response.OK || string(response.Data) != `{"id":"work"}` || primary.runtime.State() != bridge.StateReady {
		t.Fatalf("ready socket response/state = %#v, %v/%q", response, err, primary.runtime.State())
	}

	event := e2eInboundEvent("sdk-message-1")
	if err := primary.sdk.Emit(context.Background(), event); err != nil {
		t.Fatalf("first SDK Emit() error = %v", err)
	}
	if err := primary.sdk.Emit(context.Background(), event); err != nil {
		t.Fatalf("duplicate SDK Emit() error = %v", err)
	}
	delivery := receiveDelivery(t, primary.sender.deliveries)
	if delivery.ProfileID != "work" || delivery.ConversationID != "conversation-original" || delivery.TriggerMessageID != "message-trigger" || delivery.RecipientID != "user-2" || delivery.GroupID != "" || delivery.Text != "e2e final response" {
		t.Fatalf("event-bound delivery = %#v", delivery)
	}
	assertNoAdditionalDelivery(t, primary.sender.deliveries)
	if starts := primary.provider.Starts(); len(starts) != 1 || !reflect.DeepEqual(starts[0].AllowedMethods, []string{"message.history"}) {
		t.Fatalf("provider starts = %#v", starts)
	}
	if _, err := primary.store.ReplySlotByEvent(context.Background(), "work", delivery.EventID); err != nil {
		t.Fatalf("reply slot = %v", err)
	}
	beforeRestart, err := primary.ledger.List(context.Background(), "work", "", 10)
	if err != nil || len(beforeRestart.Events) != 1 || beforeRestart.Events[0].Type != string(contracts.EventMessageReceived) {
		t.Fatalf("pre-restart ledger = %#v, %v", beforeRestart, err)
	}
	if string(beforeRestart.Events[0].Data) != `{"conversation_id":"conversation-original","message_id":"message-trigger"}` {
		t.Fatalf("persisted event data = %s", beforeRestart.Events[0].Data)
	}

	primary.stop(t)
	if got, want := primary.sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK lifecycle = %v, want %v", got, want)
	}
	if err := primary.store.Close(); err != nil {
		t.Fatalf("close control store: %v", err)
	}
	primary.store = nil

	store, err := control.Open(filepath.Join(root, "primary.db"))
	if err != nil {
		t.Fatalf("reopen control store: %v", err)
	}
	defer store.Close()
	ledger, err := events.NewLedger(store)
	if err != nil {
		t.Fatalf("reopen ledger: %v", err)
	}
	reconciled, err := ledger.Reconcile(context.Background(), "work")
	if err != nil || reconciled.Event.Type != string(contracts.EventStateReconciled) {
		t.Fatalf("Reconcile() = %#v, %v", reconciled, err)
	}
	afterRestart, err := ledger.List(context.Background(), "work", "", 10)
	if err != nil || len(afterRestart.Events) != 2 || afterRestart.Events[0].Type != string(contracts.EventMessageReceived) || afterRestart.Events[1].Type != string(contracts.EventStateReconciled) {
		t.Fatalf("post-restart ledger = %#v, %v", afterRestart, err)
	}
}

type runtimeHarness struct {
	store      *control.Store
	ledger     *events.Ledger
	inbound    *daemon.Inbound
	runtime    *daemon.Runtime
	sdk        *testkit.FakeSDK
	provider   *testkit.FakeProvider
	sender     *e2eSender
	socketPath string

	cancel context.CancelFunc
	done   chan error
	closed bool
}

func newRuntimeHarness(t *testing.T, databasePath, lockPath, socketPath string) *runtimeHarness {
	t.Helper()
	store, err := control.Open(databasePath)
	if err != nil {
		t.Fatalf("open control store: %v", err)
	}
	ledger, err := events.NewLedger(store)
	if err != nil {
		_ = store.Close()
		t.Fatalf("new ledger: %v", err)
	}
	sender := &e2eSender{deliveries: make(chan reply.Delivery, 2)}
	replies, err := reply.New(store, sender)
	if err != nil {
		_ = store.Close()
		t.Fatalf("new reply service: %v", err)
	}
	session := &testkit.FakeSession{TurnResults: []contracts.TurnResult{{FinalText: "e2e final response"}}}
	provider := &testkit.FakeProvider{Session: session}
	runs, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 2, Deadline: time.Second})
	if err != nil {
		_ = store.Close()
		t.Fatalf("new run manager: %v", err)
	}
	method := proxy.Method{
		Name: "message.history",

		Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
			return json.RawMessage(`{"items":[]}`), nil
		},
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: "work",
		Ledger:    ledger,
		Replies:   replies,
		Runs:      runs,
		Grants:    grant.NewStore(),
		Methods:   []proxy.Method{method},
		Policy: daemon.PolicyFunc(func(context.Context, daemon.InboundContext) (daemon.Decision, bool, error) {
			return daemon.Decision{Principal: "provider", Methods: []string{method.Name}, RateBudget: 1}, true, nil
		}),
		GrantTTL: time.Minute,
	})
	if err != nil {
		_ = runs.Shutdown(context.Background())
		_ = store.Close()
		t.Fatalf("new inbound: %v", err)
	}
	dispatcher, err := daemon.NewDispatcher("work", []daemon.OwnerMethod{{
		Name: "profile.get",
		Handle: func(context.Context, json.RawMessage) (daemon.OwnerResult, error) {
			return daemon.OwnerResult{Data: map[string]string{"id": "work"}, Meta: contracts.Meta{ProfileID: "work"}}, nil
		},
	}})
	if err != nil {
		_ = inbound.Shutdown(context.Background())
		_ = store.Close()
		t.Fatalf("new dispatcher: %v", err)
	}
	sdk := &testkit.FakeSDK{}
	runtime, err := daemon.NewRuntime(daemon.RuntimeConfig{
		SDKFactory: func() contracts.SDK { return sdk },
		LockFile:   lockPath,
		SocketPath: socketPath,
		Inbound:    inbound,
		Handler:    dispatcher.Handle,
	})
	if err != nil {
		_ = inbound.Shutdown(context.Background())
		_ = store.Close()
		t.Fatalf("new runtime: %v", err)
	}
	return &runtimeHarness{store: store, ledger: ledger, inbound: inbound, runtime: runtime, sdk: sdk, provider: provider, sender: sender, socketPath: socketPath}
}

func (h *runtimeHarness) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- h.runtime.Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(h.socketPath); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime socket was not created")
}

func (h *runtimeHarness) stop(t *testing.T) {
	t.Helper()
	if h.cancel == nil {
		return
	}
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("runtime Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop")
	}
	h.cancel = nil
}

func (h *runtimeHarness) close(t *testing.T) {
	t.Helper()
	if h.closed {
		return
	}
	h.stop(t)
	if h.store != nil {
		if err := h.store.Close(); err != nil {
			t.Fatalf("close control store: %v", err)
		}
		h.store = nil
	}
	h.closed = true
}

type e2eSender struct {
	deliveries chan reply.Delivery
	stream     reply.StreamDelivery
	text       string
}

func (s *e2eSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.deliveries <- delivery
	return nil
}

func (s *e2eSender) StartStream(_ context.Context, delivery reply.StreamDelivery) (reply.StreamRef, error) {
	s.stream = delivery
	s.text = delivery.Content
	return reply.StreamRef{ConversationID: delivery.ConversationID, ClientMsgID: delivery.ClientMsgID}, nil
}

func (s *e2eSender) AppendStream(_ context.Context, appendValue reply.StreamAppend) error {
	for _, packet := range appendValue.Packets {
		s.text += packet
	}
	if appendValue.End {
		s.deliveries <- reply.Delivery{
			ProfileID: s.stream.ProfileID, EventID: s.stream.EventID,
			ConversationID: s.stream.ConversationID, TriggerMessageID: s.stream.TriggerMessageID,
			RecipientID: s.stream.RecipientID, GroupID: s.stream.GroupID,
			OperationID: s.stream.ClientMsgID, Text: s.text,
		}
	}
	return nil
}

func e2eInboundEvent(dedupKey string) contracts.SDKEvent {
	data, _ := json.Marshal(map[string]any{
		"conversation_id": "conversation-original",
		"message_id":      "message-trigger",
		"sender_id":       "user-2",
		"session_type":    1,
	})
	return contracts.SDKEvent{
		ProfileID:   "work",
		Type:        string(contracts.EventMessageReceived),
		OccurredAt:  time.Now().UTC(),
		DedupKey:    dedupKey,
		Data:        data,
		MessageText: "e2e inbound text",
	}
}

func receiveDelivery(t *testing.T, deliveries <-chan reply.Delivery) reply.Delivery {
	t.Helper()
	select {
	case delivery := <-deliveries:
		return delivery
	case <-time.After(time.Second):
		t.Fatal("inbound event did not produce a reply")
		return reply.Delivery{}
	}
}

func assertNoAdditionalDelivery(t *testing.T, deliveries <-chan reply.Delivery) {
	t.Helper()
	select {
	case delivery := <-deliveries:
		t.Fatalf("duplicate event produced reply %#v", delivery)
	case <-time.After(50 * time.Millisecond):
	}
}
