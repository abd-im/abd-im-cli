package abdim

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk_callback"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbconstant "github.com/openimsdk/protocol/constant"
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

type fakeUserContext struct {
	conversationID       string
	text                 string
	recipientID          string
	groupID              string
	clientMsgID          string
	appendConversationID string
	appendClientMsgID    string
	appendEnd            bool
}

func (*fakeUserContext) InitSDK(*sdk_struct.IMConfig, open_im_sdk_callback.OnConnListener) bool {
	return true
}
func (*fakeUserContext) InitResources()                                                          {}
func (*fakeUserContext) SetAdvancedMsgListener(open_im_sdk_callback.OnAdvancedMsgListener)       {}
func (*fakeUserContext) SetCustomBusinessListener(open_im_sdk_callback.OnCustomBusinessListener) {}
func (*fakeUserContext) Login(context.Context, string, string) error                             { return nil }
func (f *fakeUserContext) StartStreamMessage(_ context.Context, callback open_im_sdk_callback.SendMsgCallBack, _ string, text, clientMsgID, recipientID, groupID string) (string, error) {
	f.text, f.clientMsgID, f.recipientID, f.groupID = text, clientMsgID, recipientID, groupID
	callback.OnSuccess(`{}`)
	return f.conversationID, nil
}
func (f *fakeUserContext) AppendStreamMessage(_ context.Context, conversationID, clientMsgID string, _ int64, _ []string, end bool) error {
	f.appendConversationID, f.appendClientMsgID, f.appendEnd = conversationID, clientMsgID, end
	return nil
}
func (*fakeUserContext) Logout(context.Context) error { return nil }
func (*fakeUserContext) UnInitSDK()                   {}
