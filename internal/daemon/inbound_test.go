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
	abdimbridge "github.com/abd-im/abd-im-cli/internal/bridge/abdim"
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
	stream := receiveTextStream(t, botSender.texts)
	receiveTextFinish(t, stream.finished)
	stream.mu.Lock()
	if stream.initial != "ans" || len(stream.packets) != 1 || stream.packets[0] != "wer" || stream.recipientID != "peer" {
		t.Fatalf("bot stream = %#v", stream)
	}
	stream.mu.Unlock()
	select {
	case stream := <-userSender.texts:
		t.Fatalf("direct reply used user SDK: %#v", stream)
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

func TestInboundListenerMarksDirectConversationAsRead(t *testing.T) {
	inbound, _, _, botSender, closeStore := newInboundHarness(t)
	defer closeStore()
	inbound.Listener(context.Background(), sdkEvent("direct-read", `{"conversation_id":"conversation-1","message_id":"message-1","sender_id":"peer","session_type":1}`, "hello"))
	select {
	case conversationID := <-botSender.read:
		if conversationID != "conversation-1" {
			t.Fatalf("read conversation = %q", conversationID)
		}
	case <-time.After(time.Second):
		t.Fatal("inbound listener did not mark direct conversation as read")
	}
}

func TestInboundRepliesToAgentWorkspaceOnly(t *testing.T) {
	inbound, _, _, botSender, closeStore := newInboundHarness(t)
	defer closeStore()
	inbound.workspaceClassifier = conversationClassifierFunc(func(_ context.Context, groupID string) (contracts.ConversationKind, error) {
		if groupID == "workspace" {
			return contracts.ConversationKindAgentWorkspace, nil
		}
		return contracts.ConversationKindChat, nil
	})

	workspace := sdkEvent("workspace", `{"conversation_id":"sg_workspace","message_id":"message-1","sender_id":"owner","group_id":"workspace","session_type":3,"content_type":101}`, "hello")
	outcome, err := inbound.Process(context.Background(), workspace)
	if err != nil || outcome.Ignored {
		t.Fatalf("workspace Process() = %#v, %v", outcome, err)
	}
	stream := receiveRunStream(t, botSender.runs)
	finish := receiveRunFinish(t, stream.finished)
	if finish.Outcome != "completed" || finish.Reason != "end_turn" {
		t.Fatalf("workspace finish = %#v", finish)
	}
	stream.mu.Lock()
	events := append([]contracts.RunEvent(nil), stream.events...)
	stream.mu.Unlock()
	if len(events) != 6 {
		t.Fatalf("workspace events = %#v", events)
	}
	answer, ok := events[3].(contracts.ItemDeltaEvent)
	answerTail, tailOK := events[4].(contracts.ItemDeltaEvent)
	if !ok || !tailOK || answer.Content.(contracts.TextBlock).Text+answerTail.Content.(contracts.TextBlock).Text != "answer" {
		t.Fatalf("workspace answer = %#v", events[3])
	}
	select {
	case delivery := <-botSender.sent:
		t.Fatalf("workspace also sent plain text: %#v", delivery)
	default:
	}

	ordinary := sdkEvent("ordinary", `{"conversation_id":"sg_ordinary","message_id":"message-2","sender_id":"owner","group_id":"ordinary","session_type":3,"content_type":101}`, "hello")
	outcome, err = inbound.Process(context.Background(), ordinary)
	if err != nil || !outcome.Ignored {
		t.Fatalf("ordinary group Process() = %#v, %v", outcome, err)
	}
}

type conversationClassifierFunc func(context.Context, string) (contracts.ConversationKind, error)

func (f conversationClassifierFunc) ConversationKind(ctx context.Context, groupID string) (contracts.ConversationKind, error) {
	return f(ctx, groupID)
}

func TestInboundHostedLoadsUserContextAndRepliesThroughUserSDK(t *testing.T) {
	inbound, provider, userSender, botSender, closeStore := newInboundHarness(t)
	defer closeStore()
	event := sdkEvent("hosted-1", `{"conversation_id":"conversation-1","message_id":"message-2","session_type":1,"business_connection_id":"connection-1","owner_user_id":"owner","instruction":"keep it brief"}`, "server prompt must be ignored")
	outcome, err := inbound.Process(context.Background(), event)
	if err != nil || outcome.Ignored {
		t.Fatalf("Process() = %#v, %v", outcome, err)
	}
	stream := receiveTextStream(t, userSender.texts)
	receiveTextFinish(t, stream.finished)
	stream.mu.Lock()
	if stream.initial != "ans" || len(stream.packets) != 1 || stream.packets[0] != "wer" || stream.recipientID != "peer" {
		t.Fatalf("user stream = %#v", stream)
	}
	stream.mu.Unlock()
	select {
	case stream := <-botSender.texts:
		t.Fatalf("hosted reply used bot SDK: %#v", stream)
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

func TestReplyTextStreamFiltersCommentaryAndFallsBackToFinalText(t *testing.T) {
	sender := &captureSender{texts: make(chan *captureTextStream, 1)}
	stream := newReplyTextStream(sender, replyTarget{recipientID: "peer"})
	itemID := "commentary-1"
	if err := stream.Event(context.Background(), contracts.NewItemStartedEvent(time.Now().UnixMilli(), contracts.MessageItem{
		ID: itemID, Type: "message", Role: "assistant", Phase: "commentary", Content: []contracts.ContentBlock{},
	})); err != nil {
		t.Fatal(err)
	}
	if err := stream.Event(context.Background(), contracts.NewItemDeltaEvent(time.Now().UnixMilli(), itemID, contracts.TextBlock{Type: "text", Text: "working"})); err != nil {
		t.Fatal(err)
	}
	select {
	case output := <-sender.texts:
		t.Fatalf("commentary created a text stream: %#v", output)
	default:
	}
	if err := stream.Finish(context.Background(), "answer"); err != nil {
		t.Fatal(err)
	}
	output := receiveTextStream(t, sender.texts)
	receiveTextFinish(t, output.finished)
	if output.initial != "answer" || len(output.packets) != 0 {
		t.Fatalf("fallback stream = %#v", output)
	}
}

func TestReplyTextStreamClosesPartialOutputOnFailure(t *testing.T) {
	sender := &captureSender{texts: make(chan *captureTextStream, 1)}
	stream := newReplyTextStream(sender, replyTarget{recipientID: "peer"})
	itemID := "answer-1"
	if err := stream.Event(context.Background(), contracts.NewItemStartedEvent(time.Now().UnixMilli(), contracts.MessageItem{
		ID: itemID, Type: "message", Role: "assistant", Phase: "final", Content: []contracts.ContentBlock{},
	})); err != nil {
		t.Fatal(err)
	}
	if err := stream.Event(context.Background(), contracts.NewItemDeltaEvent(time.Now().UnixMilli(), itemID, contracts.TextBlock{Type: "text", Text: "partial"})); err != nil {
		t.Fatal(err)
	}
	output := receiveTextStream(t, sender.texts)
	if err := stream.Finish(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	receiveTextFinish(t, output.finished)
	if output.initial != "partial" {
		t.Fatalf("partial stream = %#v", output)
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
	userSender := &captureSender{sent: make(chan delivery, 2), texts: make(chan *captureTextStream, 2), runs: make(chan *captureRunStream, 2)}
	botSender := &captureSender{sent: make(chan delivery, 2), texts: make(chan *captureTextStream, 2), runs: make(chan *captureRunStream, 2), read: make(chan string, 2)}
	inbound, err := New(Config{
		ProfileID: "work", UserID: "owner", BotID: "agent", Ledger: ledger, Runs: runs,
		UserMessages: fakeMessages{}, UserSender: userSender, BotSender: botSender, WorkspaceSender: botSender,
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
	if request.Events != nil {
		itemID := "message-answer"
		_ = request.Events(context.Background(), contracts.NewItemStartedEvent(time.Now().UnixMilli(), contracts.MessageItem{
			ID: itemID, Type: "message", Role: "assistant", Phase: "final", Content: []contracts.ContentBlock{},
		}))
		_ = request.Events(context.Background(), contracts.NewItemDeltaEvent(time.Now().UnixMilli(), itemID, contracts.TextBlock{Type: "text", Text: "ans"}))
		_ = request.Events(context.Background(), contracts.NewItemDeltaEvent(time.Now().UnixMilli(), itemID, contracts.TextBlock{Type: "text", Text: "wer"}))
		_ = request.Events(context.Background(), contracts.NewItemCompletedEvent(time.Now().UnixMilli(), itemID, "completed"))
	}
	return contracts.TurnResult{FinalText: "answer"}, nil
}
func (promptSession) Cancel(context.Context) error { return nil }
func (promptSession) Close(context.Context) error  { return nil }

type delivery struct{ text, recipientID, groupID string }
type captureSender struct {
	sent  chan delivery
	texts chan *captureTextStream
	runs  chan *captureRunStream
	read  chan string
}

func (s *captureSender) MarkConversationRead(_ context.Context, conversationID string) error {
	s.read <- conversationID
	return nil
}

func (s *captureSender) SendText(_ context.Context, text, recipientID, groupID string) error {
	s.sent <- delivery{text: text, recipientID: recipientID, groupID: groupID}
	return nil
}

func (s *captureSender) StartTextStream(_ context.Context, initialText, recipientID, groupID string) (abdimbridge.TextStream, error) {
	stream := &captureTextStream{
		initial: initialText, recipientID: recipientID, groupID: groupID,
		finished: make(chan struct{}, 1),
	}
	s.texts <- stream
	return stream, nil
}

type captureTextStream struct {
	mu          sync.Mutex
	initial     string
	recipientID string
	groupID     string
	packets     []string
	finished    chan struct{}
}

func (s *captureTextStream) Append(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, text)
	return nil
}

func (s *captureTextStream) Finish(context.Context) error {
	s.finished <- struct{}{}
	return nil
}

func (s *captureSender) StartAgentRun(_ context.Context, metadata contracts.AgentRunMetadata, recipientID, groupID string) (abdimbridge.AgentRunStream, error) {
	stream := &captureRunStream{metadata: metadata, recipientID: recipientID, groupID: groupID, finished: make(chan abdimbridge.RunFinish, 1)}
	s.runs <- stream
	return stream, nil
}

type captureRunStream struct {
	mu          sync.Mutex
	metadata    contracts.AgentRunMetadata
	recipientID string
	groupID     string
	events      []contracts.RunEvent
	finished    chan abdimbridge.RunFinish
}

func (s *captureRunStream) Queued(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, contracts.NewRunLifecycleEvent("run.queued", time.Now().UnixMilli()))
	return nil
}

func (s *captureRunStream) Started(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, contracts.NewRunLifecycleEvent("run.started", time.Now().UnixMilli()))
	return nil
}

func (s *captureRunStream) Append(_ context.Context, event contracts.RunEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *captureRunStream) Finish(_ context.Context, finish abdimbridge.RunFinish) error {
	s.finished <- finish
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

func receiveTextStream(t *testing.T, streams <-chan *captureTextStream) *captureTextStream {
	t.Helper()
	select {
	case stream := <-streams:
		return stream
	case <-time.After(time.Second):
		t.Fatal("text stream was not created")
		return nil
	}
}

func receiveTextFinish(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("text stream was not finished")
	}
}

func receiveRunStream(t *testing.T, streams <-chan *captureRunStream) *captureRunStream {
	t.Helper()
	select {
	case stream := <-streams:
		return stream
	case <-time.After(time.Second):
		t.Fatal("Agent run stream was not created")
		return nil
	}
}

func receiveRunFinish(t *testing.T, finishes <-chan abdimbridge.RunFinish) abdimbridge.RunFinish {
	t.Helper()
	select {
	case finish := <-finishes:
		return finish
	case <-time.After(time.Second):
		t.Fatal("Agent run stream was not finished")
		return abdimbridge.RunFinish{}
	}
}
