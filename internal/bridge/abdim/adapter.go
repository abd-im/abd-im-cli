// Package abdim adapts one daemon-owned OpenIM UserContext.
package abdim

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk_callback"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/utils"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/google/uuid"
	pbconstant "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/log"
)

const defaultConnectTimeout = 30 * time.Second

type Config struct {
	ProfileID string
	UserID    string
	Token     []byte
	SDKConfig sdk_struct.IMConfig
	OnError   func(error)

	ConnectTimeout time.Duration
}

type userContext interface {
	InitSDK(*sdk_struct.IMConfig, open_im_sdk_callback.OnConnListener) bool
	InitResources()
	SetAdvancedMsgListener(open_im_sdk_callback.OnAdvancedMsgListener)
	SetCustomBusinessListener(open_im_sdk_callback.OnCustomBusinessListener)
	Login(context.Context, string, string) error
	MarkConversationMessageAsRead(context.Context, string) error
	StartStreamMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, string, string, string, string) (string, error)
	AppendStreamMessage(context.Context, string, string, int64, []string, bool) error
	Logout(context.Context) error
	UnInitSDK()
}

type sdkUserContext struct{ *open_im_sdk.UserContext }

func newSDKUserContext() userContext { return sdkUserContext{open_im_sdk.NewLoginMgr()} }

func (u sdkUserContext) StartStreamMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, streamType, content, clientMsgID, recipientID, groupID string) (string, error) {
	sourceID := recipientID
	sessionType := pbconstant.SingleChatType
	if groupID != "" {
		sourceID = groupID
		sessionType = pbconstant.ReadGroupChatType
	}
	conversationID := u.Conversation().GetConversationIDBySessionType(ctx, sourceID, sessionType)
	if err := u.SendStreamMessage(ctx, callback, streamType, content, clientMsgID, recipientID, groupID); err != nil {
		return "", err
	}
	return conversationID, nil
}

func (u sdkUserContext) MarkConversationMessageAsRead(ctx context.Context, conversationID string) error {
	return u.Conversation().MarkConversationMessageAsRead(ctx, conversationID)
}

type Adapter struct {
	profileID string
	userID    string
	token     string
	config    sdk_struct.IMConfig
	onError   func(error)

	newUserContext func() userContext
	initLogger     func(sdk_struct.IMConfig) error
	now            func() time.Time
	connectTimeout time.Duration

	mu               sync.RWMutex
	user             userContext
	listener         contracts.EventListener
	connectionResult chan error
	loginStarted     bool
}

func New(config Config) (*Adapter, error) { return newAdapter(config, newSDKUserContext) }

func newAdapter(config Config, factory func() userContext) (*Adapter, error) {
	if strings.TrimSpace(config.ProfileID) == "" || strings.TrimSpace(config.UserID) == "" || len(config.Token) == 0 {
		return nil, errors.New("profile ID, user ID, and token are required")
	}
	if !validEndpoint(config.SDKConfig.ApiAddr, "http", "https") || !validEndpoint(config.SDKConfig.WsAddr, "ws", "wss") || config.SDKConfig.PlatformID <= 0 || strings.TrimSpace(config.SDKConfig.DataDir) == "" || strings.TrimSpace(config.SDKConfig.LogFilePath) == "" {
		return nil, errors.New("valid SDK API, WebSocket, platform, data, and log configuration are required")
	}
	if factory == nil {
		return nil, errors.New("SDK user context factory is required")
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = defaultConnectTimeout
	}
	return &Adapter{
		profileID: config.ProfileID, userID: config.UserID, token: string(config.Token), config: config.SDKConfig,
		onError: config.OnError, newUserContext: factory, initLogger: initSDKLogger,
		now: time.Now, connectTimeout: config.ConnectTimeout,
	}, nil
}

func (a *Adapter) InitSDK(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.user != nil {
		return errors.New("OpenIM SDK is already initialized")
	}
	if a.initLogger == nil || a.initLogger(a.config) != nil {
		return errors.New("OpenIM SDK logger initialization failed")
	}
	user := a.newUserContext()
	if user == nil {
		return errors.New("OpenIM SDK factory returned nil")
	}
	config := a.config
	if !user.InitSDK(&config, connectionListener{adapter: a}) {
		return errors.New("OpenIM SDK initialization failed")
	}
	a.user = user
	return nil
}

func initSDKLogger(config sdk_struct.IMConfig) error {
	platformName, ok := pbconstant.PlatformID2Name[int(config.PlatformID)]
	if !ok || platformName == "" {
		return errors.New("OpenIM SDK platform is invalid")
	}
	return log.InitLoggerFromConfig("abdim", "sdk", config.SystemType, platformName, int(config.LogLevel), false, false, config.LogFilePath, 1, 24, open_im_sdk.GetSdkVersion(), true)
}

func (a *Adapter) InitResources(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	user.InitResources()
	return nil
}

func (a *Adapter) SetEventListener(listener contracts.EventListener) error {
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.listener = listener
	a.mu.Unlock()
	user.SetAdvancedMsgListener(messageListener{adapter: a})
	user.SetCustomBusinessListener(businessListener{adapter: a})
	return nil
}

func (a *Adapter) Login(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	a.mu.Lock()
	a.connectionResult = result
	a.mu.Unlock()
	defer a.clearConnectionResult(result)
	config := a.config
	loginContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.Login(ccontext.WithOperationID(loginContext, uuid.NewString()), a.userID, a.token); err != nil {
		return errors.New("OpenIM login failed")
	}
	a.mu.Lock()
	a.loginStarted = true
	a.mu.Unlock()
	timer := time.NewTimer(a.connectTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("OpenIM connection timed out")
	}
}

func (a *Adapter) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	a.mu.Lock()
	user, loginStarted := a.user, a.loginStarted
	a.user, a.listener, a.loginStarted = nil, nil, false
	a.mu.Unlock()
	if user == nil {
		return nil
	}
	var logoutErr error
	if loginStarted {
		config := a.config
		logoutContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
		logoutErr = user.Logout(ccontext.WithOperationID(logoutContext, uuid.NewString()))
	}
	user.UnInitSDK()
	if logoutErr != nil {
		return errors.New("OpenIM logout failed")
	}
	return nil
}

func (a *Adapter) SendText(ctx context.Context, text, recipientID, groupID string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("invalid text message delivery")
	}
	stream, err := a.StartTextStream(ctx, text, recipientID, groupID)
	if err != nil {
		return err
	}
	return stream.Finish(ctx)
}

func (a *Adapter) Context() context.Context {
	config := a.config
	return ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
}

func (a *Adapter) currentUser() (userContext, error) {
	a.mu.RLock()
	user := a.user
	a.mu.RUnlock()
	if user == nil {
		return nil, errors.New("OpenIM SDK is not initialized")
	}
	return user, nil
}

func (a *Adapter) emit(event contracts.SDKEvent) {
	a.mu.RLock()
	listener := a.listener
	a.mu.RUnlock()
	if listener != nil {
		listener(context.Background(), event)
	}
}

func (a *Adapter) MarkConversationRead(ctx context.Context, conversationID string) error {
	if strings.TrimSpace(conversationID) == "" {
		return errors.New("conversation ID is required")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	config := a.config
	ctx = ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	ctx = ccontext.WithOperationID(ctx, uuid.NewString())
	return user.MarkConversationMessageAsRead(ctx, conversationID)
}

func (a *Adapter) report(err error) {
	if err != nil && a.onError != nil {
		a.onError(err)
	}
}

func (a *Adapter) finishConnection(err error) {
	a.mu.RLock()
	result := a.connectionResult
	a.mu.RUnlock()
	if result != nil {
		select {
		case result <- err:
		default:
		}
	}
}

func (a *Adapter) clearConnectionResult(result chan error) {
	a.mu.Lock()
	if a.connectionResult == result {
		a.connectionResult = nil
	}
	a.mu.Unlock()
}

type connectionListener struct{ adapter *Adapter }

func (l connectionListener) OnConnecting()                 {}
func (l connectionListener) OnConnectSuccess()             { l.adapter.finishConnection(nil) }
func (l connectionListener) OnConnectFailed(int32, string) { l.fail("OpenIM connection failed") }
func (l connectionListener) OnKickedOffline()              { l.fail("OpenIM session was kicked offline") }
func (l connectionListener) OnUserTokenExpired()           { l.fail("OpenIM token expired") }
func (l connectionListener) OnUserTokenInvalid(string)     { l.fail("OpenIM token is invalid") }
func (l connectionListener) fail(message string) {
	err := errors.New(message)
	l.adapter.finishConnection(err)
	l.adapter.report(err)
}

type messageListener struct{ adapter *Adapter }

func (l messageListener) OnRecvNewMessage(raw string) {
	event, err := l.event(raw)
	if err != nil {
		l.adapter.report(err)
		return
	}
	l.adapter.emit(event)
}
func (l messageListener) OnRecvC2CReadReceipt(string)        {}
func (l messageListener) OnNewRecvMessageRevoked(string)     {}
func (l messageListener) OnMsgDeleted(string)                {}
func (l messageListener) OnRecvOfflineNewMessage(raw string) { l.OnRecvNewMessage(raw) }
func (l messageListener) OnRecvOnlineOnlyMessage(raw string) { l.OnRecvNewMessage(raw) }

func (l messageListener) event(raw string) (contracts.SDKEvent, error) {
	var message sdk_struct.MsgStruct
	if json.Unmarshal([]byte(raw), &message) != nil {
		return contracts.SDKEvent{}, errors.New("decode OpenIM message callback")
	}
	messageID := message.ServerMsgID
	if messageID == "" {
		messageID = message.ClientMsgID
	}
	conversationID := utils.GetConversationIDByMsg(&message)
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(conversationID) == "" {
		return contracts.SDKEvent{}, errors.New("OpenIM message callback has no stable reference")
	}
	occurredAt := l.adapter.now().UTC()
	if message.SendTime > 0 {
		occurredAt = time.UnixMilli(message.SendTime).UTC()
	}
	data, _ := json.Marshal(struct {
		ConversationID   string `json:"conversation_id"`
		MessageID        string `json:"message_id"`
		SenderID         string `json:"sender_id,omitempty"`
		GroupID          string `json:"group_id,omitempty"`
		SessionType      int32  `json:"session_type"`
		ContentType      int32  `json:"content_type"`
		SenderPlatformID int32  `json:"sender_platform_id"`
	}{conversationID, messageID, message.SendID, message.GroupID, message.SessionType, message.ContentType, message.SenderPlatformID})
	return contracts.SDKEvent{
		ProfileID: l.adapter.profileID, Type: string(contracts.EventMessageReceived), OccurredAt: occurredAt,
		DedupKey: "openim-message:" + messageID, Data: data, MessageText: messageText(message), MessageQuote: messageQuote(raw),
	}, nil
}

type businessListener struct{ adapter *Adapter }

func (l businessListener) OnRecvCustomBusinessMessage(raw string) {
	event, err := l.event(raw)
	if err != nil {
		l.adapter.report(err)
		return
	}
	if event.DedupKey != "" {
		l.adapter.emit(event)
	}
}

func (l businessListener) event(raw string) (contracts.SDKEvent, error) {
	var envelope struct {
		Key  string `json:"key"`
		Data string `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return contracts.SDKEvent{}, errors.New("decode OpenIM Business callback")
	}
	if envelope.Key != "secretary.business_message" {
		return contracts.SDKEvent{}, nil
	}
	var update struct {
		UpdateID string `json:"update_id"`
		Message  struct {
			BusinessConnectionID string `json:"business_connection_id"`
			OwnerUserID          string `json:"owner_user_id"`
			ConversationID       string `json:"conversation_id"`
			TriggerMessageID     string `json:"trigger_message_id"`
			Instruction          string `json:"instruction"`
			SenderID             string `json:"sender_id"`
			GroupID              string `json:"group_id"`
			SessionType          int32  `json:"session_type"`
			ContentType          int32  `json:"content_type"`
		} `json:"business_message"`
	}
	if json.Unmarshal([]byte(envelope.Data), &update) != nil {
		return contracts.SDKEvent{}, errors.New("decode Secretary Business update")
	}
	message := update.Message
	if update.UpdateID == "" || message.BusinessConnectionID == "" || message.OwnerUserID == "" || message.ConversationID == "" || message.TriggerMessageID == "" {
		return contracts.SDKEvent{}, errors.New("Secretary Business update has no stable reference")
	}
	if message.SessionType == 0 {
		message.SessionType = pbconstant.SingleChatType
	}
	data, _ := json.Marshal(struct {
		ConversationID       string `json:"conversation_id"`
		MessageID            string `json:"message_id"`
		SenderID             string `json:"sender_id,omitempty"`
		GroupID              string `json:"group_id,omitempty"`
		SessionType          int32  `json:"session_type"`
		ContentType          int32  `json:"content_type,omitempty"`
		BusinessConnectionID string `json:"business_connection_id"`
		OwnerUserID          string `json:"owner_user_id"`
		Instruction          string `json:"instruction,omitempty"`
	}{message.ConversationID, message.TriggerMessageID, message.SenderID, message.GroupID, message.SessionType, message.ContentType, message.BusinessConnectionID, message.OwnerUserID, message.Instruction})
	return contracts.SDKEvent{
		ProfileID: l.adapter.profileID, Type: string(contracts.EventMessageReceived), OccurredAt: l.adapter.now().UTC(),
		DedupKey: "openim-business:" + update.UpdateID, Data: data,
	}, nil
}

func messageText(message sdk_struct.MsgStruct) string {
	if message.TextElem != nil {
		return message.TextElem.Content
	}
	if message.AtTextElem != nil {
		return message.AtTextElem.Text
	}
	if message.QuoteElem != nil {
		return message.QuoteElem.Text
	}
	if message.StreamElem != nil {
		return message.StreamElem.Content + strings.Join(message.StreamElem.Packets, "")
	}
	var text sdk_struct.TextElem
	if json.Unmarshal([]byte(message.Content), &text) == nil {
		return text.Content
	}
	return ""
}

func messageQuote(raw string) *contracts.MessageQuote {
	var envelope struct {
		QuoteElem *struct {
			QuoteText    string `json:"quoteText"`
			QuoteOffset  int32  `json:"quoteOffset"`
			QuoteMessage *struct {
				ClientMsgID string `json:"clientMsgID"`
				ServerMsgID string `json:"serverMsgID"`
			} `json:"quoteMessage"`
		} `json:"quoteElem"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.QuoteElem == nil || envelope.QuoteElem.QuoteText == "" {
		return nil
	}
	quote := &contracts.MessageQuote{Text: envelope.QuoteElem.QuoteText, Offset: envelope.QuoteElem.QuoteOffset}
	if envelope.QuoteElem.QuoteMessage != nil {
		quote.SourceClientMsgID = envelope.QuoteElem.QuoteMessage.ClientMsgID
		quote.SourceServerMsgID = envelope.QuoteElem.QuoteMessage.ServerMsgID
	}
	return quote
}

type sendCallback struct {
	once sync.Once
	done chan error
}

func newSendCallback() *sendCallback { return &sendCallback{done: make(chan error, 1)} }
func (c *sendCallback) OnError(int32, string) {
	c.complete(errors.New("OpenIM message delivery failed"))
}
func (c *sendCallback) OnSuccess(string)   { c.complete(nil) }
func (*sendCallback) OnProgress(int)       {}
func (c *sendCallback) complete(err error) { c.once.Do(func() { c.done <- err }) }

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}

func validEndpoint(raw string, schemes ...string) bool {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" {
		return false
	}
	for _, scheme := range schemes {
		if endpoint.Scheme == scheme {
			return true
		}
	}
	return false
}

var _ contracts.SDK = (*Adapter)(nil)
var _ open_im_sdk_callback.OnConnListener = connectionListener{}
var _ open_im_sdk_callback.OnAdvancedMsgListener = messageListener{}
var _ open_im_sdk_callback.OnCustomBusinessListener = businessListener{}
