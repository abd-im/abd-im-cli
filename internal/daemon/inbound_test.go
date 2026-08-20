package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/events"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
)

func TestInboundDirectRepliesThroughBotSDK(t *testing.T) {
	inbound, provider, userSender, botSender, closeStore := newInboundHarness(t)
	defer closeStore()
	event := sdkEvent("direct-1", `{"conversation_id":"conversation-1","message_id":"message-1","sender_id":"peer","session_type":1}`, "hello")
	outcome, err := inbound.Process(context.Background(), event)
	if err != nil || outcome.Ignored || outcome.RunID == "" {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	delivery := receiveDelivery(t, botSender.sent)
	if delivery.text != "answer" || delivery.recipientID != "peer" {
		t.Fatalf("bot delivery = %#v", delivery)
	}
	select {
	case delivery := <-userSender.sent:
		t.Fatalf("direct reply used user SDK: %#v", delivery)
	default:
	}
	if prompt := provider.lastPrompt(); !strings.Contains(prompt, "Reply mode: direct") || !strings.Contains(prompt, "--as bot") || !strings.Contains(prompt, "hello") {
		t.Fatalf("direct prompt = %q", prompt)
	}
	duplicate, err := inbound.Process(context.Background(), event)
	if err != nil || !duplicate.Ignored || duplicate.Created {
		t.Fatalf("duplicate Process() = %#v, %v", duplicate, err)
	}
}

func TestInboundHostedLoadsUserContextAndRepliesThroughUserSDK(t *testing.T) {
	inbound, provider, userSender, botSender, closeStore := newInboundHarness(t)
	defer closeStore()
	event := sdkEvent("hosted-1", `{"conversation_id":"conversation-1","message_id":"message-2","session_type":1,"business_connection_id":"connection-1","owner_user_id":"owner","instruction":"keep it brief"}`, "server prompt must be ignored")
	outcome, err := inbound.Process(context.Background(), event)
	if err != nil || outcome.Ignored {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	delivery := receiveDelivery(t, userSender.sent)
	if delivery.text != "answer" || delivery.recipientID != "peer" {
		t.Fatalf("user delivery = %#v", delivery)
	}
	select {
	case delivery := <-botSender.sent:
		t.Fatalf("hosted reply used bot SDK: %#v", delivery)
	default:
	}
	prompt := provider.lastPrompt()
	for _, want := range []string{"Reply mode: hosted", "on behalf of owner", "--as user", "keep it brief", "peer: latest message"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("hosted prompt missing %q: %s", want, prompt)
		}
	}
}

func TestInboundRejectsHostedNotificationForAnotherOwner(t *testing.T) {
	inbound, _, _, _, closeStore := newInboundHarness(t)
	defer closeStore()
	event := sdkEvent("hosted-other", `{"conversation_id":"conversation-1","message_id":"message-2","session_type":1,"business_connection_id":"connection-1","owner_user_id":"other"}`, "")
	if _, err := inbound.Process(context.Background(), event); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Process() error = %v", err)
	}
}

func newInboundHarness(t *testing.T) (*Inbound, *promptProvider, *captureSender, *captureSender, func()) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	ledger, _ := events.NewLedger(store)
	provider := &promptProvider{}
	runs, err := run.NewManager(run.Config{Provider: provider, MaxQueue: 2, MaxConcurrentRuns: 2, Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	userSender, botSender := &captureSender{sent: make(chan delivery, 2)}, &captureSender{sent: make(chan delivery, 2)}
	inbound, err := New(Config{
		ProfileID: "work", UserID: "owner", BotID: "agent", Ledger: ledger, Runs: runs,
		UserMessages: fakeMessages{}, UserSender: userSender, BotSender: botSender,
	})
	if err != nil {
		t.Fatal(err)
	}
	return inbound, provider, userSender, botSender, func() { _ = inbound.Shutdown(context.Background()); _ = store.Close() }
}

func sdkEvent(key, data, text string) contracts.SDKEvent {
	return contracts.SDKEvent{ProfileID: "work", Type: string(contracts.EventMessageReceived), OccurredAt: time.Now(), DedupKey: key, Data: json.RawMessage(data), MessageText: text}
}

type fakeMessages struct{}

func (fakeMessages) Get(context.Context, string, string) (messageservice.Message, error) {
	return messageservice.Message{ID: "message-2", ConversationID: "conversation-1", SenderID: "peer", Text: "latest message"}, nil
}
func (fakeMessages) History(context.Context, messageservice.HistoryQuery) ([]messageservice.Message, error) {
	return []messageservice.Message{{ID: "message-1", ConversationID: "conversation-1", SenderID: "owner", Text: "previous"}, {ID: "message-2", ConversationID: "conversation-1", SenderID: "peer", Text: "latest message"}}, nil
}
func (fakeMessages) Search(context.Context, messageservice.HistoryQuery, string) ([]messageservice.Message, error) {
	return nil, nil
}

type promptProvider struct {
	mu      sync.Mutex
	prompts []string
}

func (p *promptProvider) Start(context.Context, contracts.StartRequest) (contracts.Session, error) {
	return promptSession{provider: p}, nil
}
func (p *promptProvider) lastPrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prompts[len(p.prompts)-1]
}

type promptSession struct{ provider *promptProvider }

func (s promptSession) Turn(_ context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	s.provider.mu.Lock()
	s.provider.prompts = append(s.provider.prompts, request.Prompt)
	s.provider.mu.Unlock()
	return contracts.TurnResult{FinalText: "answer"}, nil
}
func (promptSession) Cancel(context.Context) error { return nil }
func (promptSession) Close(context.Context) error  { return nil }

type delivery struct{ text, recipientID, groupID string }
type captureSender struct{ sent chan delivery }

func (s *captureSender) SendText(_ context.Context, text, recipientID, groupID string) error {
	s.sent <- delivery{text: text, recipientID: recipientID, groupID: groupID}
	return nil
}

func receiveDelivery(t *testing.T, deliveries <-chan delivery) delivery {
	t.Helper()
	select {
	case delivery := <-deliveries:
		return delivery
	case <-time.After(time.Second):
		t.Fatal("reply was not sent")
		return delivery{}
	}
}
