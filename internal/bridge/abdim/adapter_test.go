package abdim

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/reply"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk_callback"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestAdapterOwnsSDKLifecycleAndRedactsLoginError(t *testing.T) {
	user := &fakeUserContext{loginErr: errors.New("token-marker")}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatalf("newAdapter() error = %v", err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatalf("InitSDK() error = %v", err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatalf("InitResources() error = %v", err)
	}
	if err := adapter.SetEventListener(nil); err != nil {
		t.Fatalf("SetEventListener() error = %v", err)
	}
	if err := adapter.Login(context.Background()); err == nil || strings.Contains(err.Error(), "token-marker") {
		t.Fatalf("Login() error = %v", err)
	}
	if !user.loginCalled || !user.loginTokenSet || user.listener == nil || user.messageListener == nil {
		t.Fatalf("SDK lifecycle was not fully configured: %#v", user)
	}
	loginConfig, ok := user.loginContext.Value(ccontext.GlobalConfigKey{}).(*ccontext.GlobalConfig)
	if !ok || loginConfig == nil || loginConfig.UserID != "user-1" || loginConfig.Token != "token-marker" || loginConfig.IMConfig == nil || loginConfig.IMConfig.ApiAddr != "https://api.example.test" {
		t.Fatalf("Login() context does not contain the SDK configuration")
	}
	if err := adapter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if user.logoutCalled || !user.uninitialized {
		t.Fatal("Shutdown() did not safely close the SDK user context")
	}
}

func TestAdapterLoginWaitsForOnlineCallback(t *testing.T) {
	user := &fakeUserContext{connectOnLogin: true}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetEventListener(nil); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Login(context.Background()); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if err := adapter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !user.logoutCalled || !user.uninitialized {
		t.Fatal("Shutdown() did not close a logged-in SDK user context")
	}
	config, ok := user.logoutContext.Value(ccontext.GlobalConfigKey{}).(*ccontext.GlobalConfig)
	if !ok || config == nil || config.UserID != "user-1" || config.Token != "token-marker" || config.IMConfig == nil || config.IMConfig.ApiAddr != "https://api.example.test" {
		t.Fatal("Shutdown() context does not contain the SDK configuration")
	}
}

func TestAdapterShutdownAfterLoginSubmissionFailureSkipsLogout(t *testing.T) {
	user := &fakeUserContext{loginErr: errors.New("submission failure")}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Login(context.Background()); err == nil {
		t.Fatal("Login() error = nil")
	}
	if err := adapter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if user.logoutCalled || !user.uninitialized {
		t.Fatalf("shutdown after failed login = %+v", user)
	}
}

func TestAdapterNormalizesMessageCallbacksWithoutBody(t *testing.T) {
	user := &fakeUserContext{}
	errorsSeen := make(chan error, 1)
	config := testConfig(t)
	config.OnError = func(err error) { errorsSeen <- err }
	adapter, err := newAdapter(config, func() userContext { return user })
	if err != nil {
		t.Fatalf("newAdapter() error = %v", err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	adapter.now = func() time.Time { return time.UnixMilli(999).UTC() }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatalf("InitSDK() error = %v", err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatalf("InitResources() error = %v", err)
	}
	events := make(chan contracts.SDKEvent, 1)
	if err := adapter.SetEventListener(func(_ context.Context, event contracts.SDKEvent) { events <- event }); err != nil {
		t.Fatalf("SetEventListener() error = %v", err)
	}
	raw, err := json.Marshal(sdk_struct.MsgStruct{
		ClientMsgID: "client-1",
		ServerMsgID: "server-1",
		SessionType: constant.SingleChatType,
		SendID:      "user-2",
		RecvID:      "user-1",
		SendTime:    123,
		Content:     `{"content":"message-body-marker"}`,
	})
	if err != nil {
		t.Fatalf("marshal callback fixture: %v", err)
	}
	user.messageListener.OnRecvNewMessage(string(raw))
	event := <-events
	if err := event.Validate(); err != nil {
		t.Fatalf("SDK event validation error = %v", err)
	}
	if event.ProfileID != "work" || event.DedupKey != "openim-message:server-1" || event.Type != string(contracts.EventMessageReceived) || !event.OccurredAt.Equal(time.UnixMilli(123).UTC()) {
		t.Fatalf("SDK event = %#v", event)
	}
	if strings.Contains(string(event.Data), "message-body-marker") {
		t.Fatalf("event data contains message body: %s", event.Data)
	}
	if event.MessageText != "message-body-marker" {
		t.Fatalf("event transient message text = %q", event.MessageText)
	}
	var data struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		SenderID       string `json:"sender_id"`
		SessionType    int32  `json:"session_type"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data.ConversationID != "si_user-1_user-2" || data.MessageID != "server-1" || data.SenderID != "user-2" || data.SessionType != constant.SingleChatType {
		t.Fatalf("event data = %#v", data)
	}
	payload, err := json.Marshal(event)
	if err != nil || strings.Contains(string(payload), "message-body-marker") {
		t.Fatalf("serialized SDK event leaked message body: %s, %v", payload, err)
	}
	user.messageListener.OnRecvNewMessage(`{"content":"message-body-marker"}`)
	if err := <-errorsSeen; strings.Contains(err.Error(), "message-body-marker") {
		t.Fatalf("callback error leaked body: %v", err)
	}
}

func TestNewRejectsIncompleteSDKConfiguration(t *testing.T) {
	config := testConfig(t)
	config.Token = nil
	if _, err := New(config); err == nil {
		t.Fatal("New() accepted missing token")
	}
	config = testConfig(t)
	config.SDKConfig.WsAddr = "https://not-websocket.example"
	if _, err := New(config); err == nil {
		t.Fatal("New() accepted invalid WebSocket endpoint")
	}
}

func TestAdapterRejectsLoggerInitializationFailure(t *testing.T) {
	adapter, err := newAdapter(testConfig(t), func() userContext { return &fakeUserContext{} })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return errors.New("logger failure") }
	if err := adapter.InitSDK(context.Background()); err == nil || strings.Contains(err.Error(), "logger failure") {
		t.Fatalf("InitSDK() error = %v", err)
	}
}

func TestAdapterRepliesOnlyToEventBoundRecipient(t *testing.T) {
	user := &fakeUserContext{}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatalf("InitSDK() error = %v", err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatalf("InitResources() error = %v", err)
	}
	if err := adapter.Reply(context.Background(), reply.Delivery{RecipientID: "user-2", Text: "final response"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if user.replyRecipient != "user-2" || user.replyGroup != "" || user.replyText != "final response" {
		t.Fatalf("SDK reply target = recipient=%q group=%q text=%q", user.replyRecipient, user.replyGroup, user.replyText)
	}
	replyConfig, ok := user.replyContext.Value(ccontext.GlobalConfigKey{}).(*ccontext.GlobalConfig)
	if !ok || replyConfig == nil || replyConfig.UserID != "user-1" || replyConfig.Token != "token-marker" || replyConfig.IMConfig == nil || replyConfig.IMConfig.ApiAddr != "https://api.example.test" {
		t.Fatal("Reply() context does not contain the SDK configuration")
	}
	if err := adapter.Reply(context.Background(), reply.Delivery{RecipientID: "user-2", GroupID: "group-1", Text: "must fail"}); err == nil {
		t.Fatal("Reply() accepted an ambiguous target")
	}
}

func TestAdapterSendsTextToOneExplicitTarget(t *testing.T) {
	user := &fakeUserContext{}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SendText(context.Background(), "outbound text", "user-2", ""); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if user.replyRecipient != "user-2" || user.replyGroup != "" || user.replyText != "outbound text" {
		t.Fatalf("SDK text target = recipient=%q group=%q text=%q", user.replyRecipient, user.replyGroup, user.replyText)
	}
	if err := adapter.SendText(context.Background(), "must fail", "user-2", "group-1"); err == nil {
		t.Fatal("SendText() accepted an ambiguous target")
	}
}

func TestAdapterSendsTextAtToExplicitGroupAndUsers(t *testing.T) {
	user := &fakeUserContext{}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SendAt(context.Background(), "attention", "group-1", []string{"user-2", "user-3"}); err != nil {
		t.Fatalf("SendAt() error = %v", err)
	}
	if user.replyGroup != "group-1" || user.replyText != "attention" || len(user.atMentionUserIDs) != 2 || user.atMentionUserIDs[0] != "user-2" || user.atMentionUserIDs[1] != "user-3" {
		t.Fatalf("SDK text-at target = %+v", user)
	}
	if err := adapter.SendAt(context.Background(), "must fail", "", []string{"user-2"}); err == nil {
		t.Fatal("SendAt() accepted an empty group")
	}
}

func TestAdapterSendsVerifiedQuoteToOneExplicitTarget(t *testing.T) {
	user := &fakeUserContext{}
	adapter, err := newAdapter(testConfig(t), func() userContext { return user })
	if err != nil {
		t.Fatal(err)
	}
	adapter.initLogger = func(sdk_struct.IMConfig) error { return nil }
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	quoted := &sdk_struct.MsgStruct{ServerMsgID: "quoted-1"}
	if err := adapter.SendQuote(context.Background(), "reply", "user-2", "", quoted); err != nil {
		t.Fatalf("SendQuote() error = %v", err)
	}
	if user.replyRecipient != "user-2" || user.replyGroup != "" || user.replyText != "reply" || user.quotedMessage != quoted {
		t.Fatalf("SDK quote target = %+v", user)
	}
	if err := adapter.SendQuote(context.Background(), "reply", "user-2", "group-1", quoted); err == nil {
		t.Fatal("SendQuote() accepted an ambiguous target")
	}
	if err := adapter.SendQuote(context.Background(), "reply", "user-2", "", nil); err == nil {
		t.Fatal("SendQuote() accepted a missing quote source")
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		ProfileID: "work",
		UserID:    "user-1",
		Token:     []byte("token-marker"),
		SDKConfig: sdk_struct.IMConfig{
			PlatformID:  5,
			ApiAddr:     "https://api.example.test",
			WsAddr:      "wss://ws.example.test",
			DataDir:     root + "/sdk",
			LogFilePath: root + "/logs/sdk.log",
		},
	}
}

type fakeUserContext struct {
	listener         open_im_sdk_callback.OnConnListener
	messageListener  open_im_sdk_callback.OnAdvancedMsgListener
	loginErr         error
	loginCalled      bool
	loginTokenSet    bool
	loginContext     context.Context
	connectOnLogin   bool
	logoutCalled     bool
	uninitialized    bool
	logoutContext    context.Context
	replyCallback    open_im_sdk_callback.SendMsgCallBack
	replyContext     context.Context
	replyText        string
	replyRecipient   string
	replyGroup       string
	atMentionUserIDs []string
	quotedMessage    *sdk_struct.MsgStruct
}

func (f *fakeUserContext) InitSDK(_ *sdk_struct.IMConfig, listener open_im_sdk_callback.OnConnListener) bool {
	f.listener = listener
	return true
}

func (f *fakeUserContext) InitResources() {}

func (f *fakeUserContext) SetAdvancedMsgListener(listener open_im_sdk_callback.OnAdvancedMsgListener) {
	f.messageListener = listener
}

func (f *fakeUserContext) Login(ctx context.Context, _ string, token string) error {
	f.loginCalled = true
	f.loginTokenSet = token != ""
	f.loginContext = ctx
	if f.loginErr == nil && f.connectOnLogin {
		f.listener.OnConnectSuccess()
	}
	return f.loginErr
}

func (f *fakeUserContext) SendTextMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, text, recipientID, groupID string) error {
	f.replyCallback = callback
	f.replyContext = ctx
	f.replyText = text
	f.replyRecipient = recipientID
	f.replyGroup = groupID
	callback.OnSuccess(`{}`)
	return nil
}

func (f *fakeUserContext) SendAtMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, text, groupID string, mentionUserIDs []string) error {
	f.replyCallback = callback
	f.replyContext = ctx
	f.replyText = text
	f.replyRecipient = ""
	f.replyGroup = groupID
	f.atMentionUserIDs = append([]string(nil), mentionUserIDs...)
	callback.OnSuccess(`{}`)
	return nil
}

func (f *fakeUserContext) SendQuoteMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, text, recipientID, groupID string, quoted *sdk_struct.MsgStruct) error {
	f.replyCallback = callback
	f.replyContext = ctx
	f.replyText = text
	f.replyRecipient = recipientID
	f.replyGroup = groupID
	f.quotedMessage = quoted
	callback.OnSuccess(`{}`)
	return nil
}

func (f *fakeUserContext) Logout(ctx context.Context) error {
	f.logoutCalled = true
	f.logoutContext = ctx
	return nil
}

func (f *fakeUserContext) UnInitSDK() { f.uninitialized = true }
