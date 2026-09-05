package abdim

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	pbconstant "github.com/abd-im/abd-im-protocol/constant"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk_callback"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestMessageListenerNormalizesStableReferences(t *testing.T) {
	adapter := &Adapter{profileID: "work", now: func() time.Time { return time.UnixMilli(999).UTC() }}
	raw, err := json.Marshal(sdk_struct.MsgStruct{
		ServerMsgID: "message-1", SendID: "peer", RecvID: "agent",
		SessionType: pbconstant.SingleChatType, ContentType: pbconstant.Text,
		SendTime: 123, Content: `{"content":"hello"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := (messageListener{adapter: adapter}).event(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if event.DedupKey != "openim-message:message-1" || event.MessageText != "hello" || !event.OccurredAt.Equal(time.UnixMilli(123).UTC()) {
		t.Fatalf("message event = %#v", event)
	}
	var reference struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		SenderID       string `json:"sender_id"`
	}
	if err := json.Unmarshal(event.Data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.ConversationID != "si_agent_peer" || reference.MessageID != "message-1" || reference.SenderID != "peer" {
		t.Fatalf("message reference = %#v", reference)
	}
}

func TestBusinessListenerNormalizesHostedReferences(t *testing.T) {
	adapter := &Adapter{profileID: "work", now: func() time.Time { return time.UnixMilli(999).UTC() }}
	update := `{"update_id":"update-1","business_message":{"business_connection_id":"connection-1","owner_user_id":"owner","conversation_id":"si_owner_peer","trigger_message_id":"message-1","instruction":"be concise","sender_id":"peer","session_type":1,"content_type":101}}`
	envelope, err := json.Marshal(struct {
		Key  string `json:"key"`
		Data string `json:"data"`
	}{Key: "secretary.business_message", Data: update})
	if err != nil {
		t.Fatal(err)
	}
	event, err := (businessListener{adapter: adapter}).event(string(envelope))
	if err != nil {
		t.Fatal(err)
	}
	if event.DedupKey != "openim-business:update-1" || event.MessageText != "" {
		t.Fatalf("business event = %#v", event)
	}
	var reference eventReference
	if err := json.Unmarshal(event.Data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.OwnerUserID != "owner" || reference.SenderID != "peer" || reference.MessageID != "message-1" || reference.BusinessConnectionID != "connection-1" {
		t.Fatalf("business reference = %#v", reference)
	}
}

type eventReference struct {
	OwnerUserID          string `json:"owner_user_id"`
	SenderID             string `json:"sender_id"`
	MessageID            string `json:"message_id"`
	BusinessConnectionID string `json:"business_connection_id"`
}

func TestAdapterSendTextUsesCurrentSDK(t *testing.T) {
	user := &fakeUserContext{conversationID: "si_agent_peer"}
	adapter := &Adapter{userID: "agent", token: "token", user: user}
	if err := adapter.SendText(context.Background(), "hello", "peer", ""); err != nil {
		t.Fatal(err)
	}
	if user.text != "hello" || user.recipientID != "peer" || user.groupID != "" || user.clientMsgID == "" {
		t.Fatalf("stream start = %#v", user)
	}
	if user.appendConversationID != "si_agent_peer" || user.appendClientMsgID != user.clientMsgID || !user.appendEnd {
		t.Fatalf("stream append = %#v", user)
	}
	if err := adapter.SendText(context.Background(), "invalid", "peer", "group"); err == nil {
		t.Fatal("SendText accepted two targets")
	}
}

func TestTextStreamWriterAppendsDeltasBeforeFinishing(t *testing.T) {
	user := &fakeUserContext{conversationID: "si_agent_peer"}
	adapter := &Adapter{userID: "agent", token: "token", user: user}
	stream, err := adapter.StartTextStream(context.Background(), "hel", "peer", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(context.Background(), "lo"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if user.streamType != "text" || user.text != "hel" {
		t.Fatalf("stream start = %#v", user)
	}
	if len(user.appends) != 2 || user.appends[0].startIndex != 0 || user.appends[0].end || len(user.appends[0].packets) != 1 || user.appends[0].packets[0] != "lo" {
		t.Fatalf("delta append = %#v", user.appends)
	}
	if user.appends[1].startIndex != 1 || !user.appends[1].end || len(user.appends[1].packets) != 0 {
		t.Fatalf("terminal append = %#v", user.appends[1])
	}
	if err := stream.Finish(context.Background()); err == nil {
		t.Fatal("writer accepted a second finish")
	}
}

func TestAgentRunWriterAppendsOrderedV2PacketsAndEndsWithTerminalEvent(t *testing.T) {
	user := &fakeUserContext{conversationID: "sg_workspace"}
	adapter := &Adapter{userID: "agent", token: "token", user: user}
	stream, err := adapter.StartAgentRun(context.Background(), contracts.AgentRunMetadata{
		Schema: contracts.AgentRunSchema, SchemaVersion: contracts.AgentRunSchemaVersion,
		RunID: "run-1", TriggerMessageID: "message-1",
	}, "", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if user.streamType != "agent_run_v2" || user.groupID != "workspace" {
		t.Fatalf("stream start = %#v", user)
	}
	if err := stream.Queued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := stream.Started(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(context.Background(), contracts.NewItemDeltaEvent(
		time.Now().UnixMilli(), "message-1", contracts.TextBlock{Type: "text", Text: "hello"},
	)); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(context.Background(), RunFinish{Outcome: "completed", Reason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	if len(user.appends) != 4 {
		t.Fatalf("append calls = %#v", user.appends)
	}
	for index, call := range user.appends {
		if call.startIndex != int64(index) || len(call.packets) != 1 {
			t.Fatalf("append %d = %#v", index, call)
		}
	}
	if user.appends[0].end || user.appends[1].end || user.appends[2].end || !user.appends[3].end {
		t.Fatalf("append end flags = %#v", user.appends)
	}
	var terminal struct {
		Event   string `json:"event"`
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal([]byte(user.appends[3].packets[0]), &terminal) != nil || terminal.Event != "run.finished" || terminal.Outcome != "completed" || terminal.Reason != "end_turn" {
		t.Fatalf("terminal packet = %s", user.appends[3].packets[0])
	}
	if err := stream.Finish(context.Background(), RunFinish{Outcome: "completed", Reason: "end_turn"}); err == nil {
		t.Fatal("writer accepted a second finish")
	}
}

type appendCall struct {
	startIndex int64
	packets    []string
	end        bool
}

type fakeUserContext struct {
	conversationID       string
	text                 string
	recipientID          string
	groupID              string
	clientMsgID          string
	streamType           string
	appendConversationID string
	appendClientMsgID    string
	appendEnd            bool
	appends              []appendCall
}

func (*fakeUserContext) InitSDK(*sdk_struct.IMConfig, open_im_sdk_callback.OnConnListener) bool {
	return true
}
func (*fakeUserContext) InitResources()                                                          {}
func (*fakeUserContext) SetAdvancedMsgListener(open_im_sdk_callback.OnAdvancedMsgListener)       {}
func (*fakeUserContext) SetCustomBusinessListener(open_im_sdk_callback.OnCustomBusinessListener) {}
func (*fakeUserContext) Login(context.Context, string, string) error                             { return nil }
func (f *fakeUserContext) MarkConversationMessageAsRead(_ context.Context, conversationID string) error {
	return nil
}
func (f *fakeUserContext) StartStreamMessage(_ context.Context, callback open_im_sdk_callback.SendMsgCallBack, streamType string, text, clientMsgID, recipientID, groupID string) (string, error) {
	f.streamType, f.text, f.clientMsgID, f.recipientID, f.groupID = streamType, text, clientMsgID, recipientID, groupID
	callback.OnSuccess(`{}`)
	return f.conversationID, nil
}
func (f *fakeUserContext) AppendStreamMessage(_ context.Context, conversationID, clientMsgID string, startIndex int64, packets []string, end bool) error {
	f.appendConversationID, f.appendClientMsgID, f.appendEnd = conversationID, clientMsgID, end
	f.appends = append(f.appends, appendCall{startIndex: startIndex, packets: append([]string(nil), packets...), end: end})
	return nil
}
func (*fakeUserContext) Logout(context.Context) error { return nil }
func (*fakeUserContext) UnInitSDK()                   {}
