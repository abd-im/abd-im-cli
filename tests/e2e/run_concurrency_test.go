package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/reply"
)

func TestConcurrentInboundRunsRemainEventBoundE2E(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ledger, err := events.NewLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	sender := newConcurrentStreamSender()
	replies, err := reply.New(store, sender)
	if err != nil {
		t.Fatal(err)
	}
	provider := newConcurrentInboundProvider()
	runs, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 1, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: "work", Ledger: ledger, Replies: replies, Runs: runs, Grants: grant.NewStore(),
		Policy: daemon.PolicyFunc(func(context.Context, daemon.InboundContext) (daemon.Decision, bool, error) {
			return daemon.Decision{Principal: "provider", RateBudget: 1}, true, nil
		}),
		GrantTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inbound.Shutdown(context.Background())

	first, err := inbound.Process(context.Background(), concurrentInboundEvent("event-a", "conversation-a", "message-a", "user-a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := inbound.Process(context.Background(), concurrentInboundEvent("event-b", "conversation-b", "message-b", "user-b"))
	if err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{provider.waitStarted(t): true, provider.waitStarted(t): true}
	if !started[first.RunID] || !started[second.RunID] {
		t.Fatalf("concurrent provider runs = %v, want %q and %q", started, first.RunID, second.RunID)
	}
	keys := provider.stateKeys()
	if keys[first.RunID] == "" || keys[second.RunID] == "" || keys[first.RunID] == keys[second.RunID] {
		t.Fatalf("provider state keys = %v", keys)
	}

	provider.release(first.RunID)
	provider.release(second.RunID)
	deliveries := map[string]reply.Delivery{}
	for len(deliveries) != 2 {
		delivery := receiveDelivery(t, sender.deliveries)
		deliveries[delivery.ConversationID] = delivery
	}
	assertConcurrentDelivery(t, deliveries["conversation-a"], first.EventID, "message-a", "user-a", first.RunID)
	assertConcurrentDelivery(t, deliveries["conversation-b"], second.EventID, "message-b", "user-b", second.RunID)
}

func concurrentInboundEvent(dedupKey, conversationID, messageID, senderID string) contracts.SDKEvent {
	data, _ := json.Marshal(map[string]any{
		"conversation_id": conversationID,
		"message_id":      messageID,
		"sender_id":       senderID,
		"session_type":    1,
	})
	return contracts.SDKEvent{
		ProfileID: "work", Type: string(contracts.EventMessageReceived), OccurredAt: time.Now().UTC(),
		DedupKey: dedupKey, Data: data, MessageText: "concurrent inbound",
	}
}

func assertConcurrentDelivery(t *testing.T, delivery reply.Delivery, eventID, messageID, recipientID, runID string) {
	t.Helper()
	if delivery.EventID != eventID || delivery.TriggerMessageID != messageID || delivery.RecipientID != recipientID || delivery.Text != "reply-"+runID {
		t.Fatalf("event-bound concurrent delivery = %#v", delivery)
	}
}

type concurrentInboundProvider struct {
	mu       sync.Mutex
	started  chan string
	releases map[string]chan struct{}
	keys     map[string]string
}

func newConcurrentInboundProvider() *concurrentInboundProvider {
	return &concurrentInboundProvider{
		started: make(chan string, 2), releases: make(map[string]chan struct{}), keys: make(map[string]string),
	}
}

func (p *concurrentInboundProvider) Start(_ context.Context, request contracts.StartRequest) (contracts.Session, error) {
	p.mu.Lock()
	p.releases[request.RunID] = make(chan struct{})
	p.keys[request.RunID] = request.StateKey
	p.mu.Unlock()
	return &concurrentInboundSession{provider: p, runID: request.RunID}, nil
}

func (p *concurrentInboundProvider) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case runID := <-p.started:
		return runID
	case <-time.After(time.Second):
		t.Fatal("concurrent provider run did not start")
		return ""
	}
}

func (p *concurrentInboundProvider) release(runID string) {
	p.mu.Lock()
	release := p.releases[runID]
	p.mu.Unlock()
	close(release)
}

func (p *concurrentInboundProvider) stateKeys() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make(map[string]string, len(p.keys))
	for runID, key := range p.keys {
		result[runID] = key
	}
	return result
}

type concurrentInboundSession struct {
	provider *concurrentInboundProvider
	runID    string
}

func (s *concurrentInboundSession) Turn(ctx context.Context, _ contracts.TurnRequest) (contracts.TurnResult, error) {
	s.provider.mu.Lock()
	release := s.provider.releases[s.runID]
	s.provider.mu.Unlock()
	s.provider.started <- s.runID
	select {
	case <-release:
		return contracts.TurnResult{FinalText: "reply-" + s.runID}, nil
	case <-ctx.Done():
		return contracts.TurnResult{}, ctx.Err()
	}
}

func (*concurrentInboundSession) Cancel(context.Context) error { return nil }
func (*concurrentInboundSession) Close(context.Context) error  { return nil }

type concurrentStreamState struct {
	delivery reply.StreamDelivery
	text     string
}

type concurrentStreamSender struct {
	mu         sync.Mutex
	streams    map[string]concurrentStreamState
	deliveries chan reply.Delivery
}

func newConcurrentStreamSender() *concurrentStreamSender {
	return &concurrentStreamSender{streams: make(map[string]concurrentStreamState), deliveries: make(chan reply.Delivery, 2)}
}

func (s *concurrentStreamSender) Reply(_ context.Context, delivery reply.Delivery) error {
	s.deliveries <- delivery
	return nil
}

func (s *concurrentStreamSender) StartStream(_ context.Context, delivery reply.StreamDelivery) (reply.StreamRef, error) {
	s.mu.Lock()
	s.streams[delivery.ClientMsgID] = concurrentStreamState{delivery: delivery, text: delivery.Content}
	s.mu.Unlock()
	return reply.StreamRef{ConversationID: delivery.ConversationID, ClientMsgID: delivery.ClientMsgID}, nil
}

func (s *concurrentStreamSender) AppendStream(_ context.Context, appendValue reply.StreamAppend) error {
	s.mu.Lock()
	state := s.streams[appendValue.ClientMsgID]
	for _, packet := range appendValue.Packets {
		state.text += packet
	}
	s.streams[appendValue.ClientMsgID] = state
	if !appendValue.End {
		s.mu.Unlock()
		return nil
	}
	delete(s.streams, appendValue.ClientMsgID)
	s.mu.Unlock()
	s.deliveries <- reply.Delivery{
		ProfileID: state.delivery.ProfileID, EventID: state.delivery.EventID,
		ConversationID: state.delivery.ConversationID, TriggerMessageID: state.delivery.TriggerMessageID,
		RecipientID: state.delivery.RecipientID, GroupID: state.delivery.GroupID,
		OperationID: state.delivery.ClientMsgID, Text: state.text,
	}
	return nil
}
