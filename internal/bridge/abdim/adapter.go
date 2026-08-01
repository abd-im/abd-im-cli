// Package abdim adapts the daemon-owned OpenIM UserContext to abdim's SDK
// lifecycle contract. It deliberately does not expose the SDK to callers.
package abdim

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-cli/internal/reply"
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

// Config contains the daemon-owned SDK connection settings. Token is never
// logged or embedded in emitted events; Adapter.Context exposes it only to
// daemon-internal SDK sources.
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
	Login(context.Context, string, string) error
	SendTextMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, string, string) error
	SendAtMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, string, []string) error
	SendQuoteMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, string, string, *sdk_struct.MsgStruct) error
	SendLocationMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, float64, float64, string, string) error
	SendCustomMessage(context.Context, open_im_sdk_callback.SendMsgCallBack, string, string, string, string, string) error
	Logout(context.Context) error
	UnInitSDK()
}

type sdkUserContext struct{ *open_im_sdk.UserContext }

func newSDKUserContext() userContext { return sdkUserContext{open_im_sdk.NewLoginMgr()} }

func (u sdkUserContext) SendAtMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, text, groupID string, mentionUserIDs []string) error {
	message, err := u.Conversation().CreateTextAtMessage(ctx, text, mentionUserIDs, nil, nil)
	if err != nil {
		return err
	}
	ctx = ccontext.WithSendMessageCallback(ctx, callback)
	_, err = u.Conversation().SendMessageNotOss(ctx, message, "", groupID, nil, false)
	return err
}

func (u sdkUserContext) SendQuoteMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, text, recipientID, groupID string, quoted *sdk_struct.MsgStruct) error {
	if quoted == nil {
		return errors.New("quoted message is required")
	}
	message, err := u.Conversation().CreateQuoteMessage(ctx, text, quoted)
	if err != nil {
		return err
	}
	ctx = ccontext.WithSendMessageCallback(ctx, callback)
	_, err = u.Conversation().SendMessageNotOss(ctx, message, recipientID, groupID, nil, false)
	return err
}

func (u sdkUserContext) SendLocationMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, description string, longitude, latitude float64, recipientID, groupID string) error {
	message, err := u.Conversation().CreateLocationMessage(ctx, description, longitude, latitude)
	if err != nil {
		return err
	}
	ctx = ccontext.WithSendMessageCallback(ctx, callback)
	_, err = u.Conversation().SendMessageNotOss(ctx, message, recipientID, groupID, nil, false)
	return err
}

func (u sdkUserContext) SendCustomMessage(ctx context.Context, callback open_im_sdk_callback.SendMsgCallBack, data, extension, description, recipientID, groupID string) error {
	message, err := u.Conversation().CreateCustomMessage(ctx, data, extension, description)
	if err != nil {
		return err
	}
	ctx = ccontext.WithSendMessageCallback(ctx, callback)
	_, err = u.Conversation().SendMessageNotOss(ctx, message, recipientID, groupID, nil, false)
	return err
}

// Adapter is the sole owner of one SDK UserContext. Its Context method is for
// daemon-internal typed services only; CLI, MCP, and providers never receive
// it.
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

// New creates an adapter that constructs a fresh SDK UserContext through the
// forked SDK's NewLoginMgr function when bridge.LoginMgr starts it.
func New(config Config) (*Adapter, error) {
	return newAdapter(config, newSDKUserContext)
}

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
		profileID:      config.ProfileID,
		userID:         config.UserID,
		token:          string(config.Token),
		config:         config.SDKConfig,
		onError:        config.OnError,
		newUserContext: factory,
		initLogger:     initSDKLogger,
		now:            time.Now,
		connectTimeout: config.ConnectTimeout,
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
	// Install a listener even without an inbound handler so the SDK's default
	// listener cannot log complete message payloads.
	user.SetAdvancedMsgListener(messageListener{adapter: a})
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
	connectionResult := make(chan error, 1)
	a.mu.Lock()
	a.connectionResult = connectionResult
	a.mu.Unlock()
	defer a.clearConnectionResult(connectionResult)
	config := a.config
	loginContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.Login(ccontext.WithOperationID(loginContext, uuid.NewString()), a.userID, a.token); err != nil {
		return errors.New("OpenIM login failed")
	}
	a.mu.Lock()
	if a.user == user {
		a.loginStarted = true
	}
	a.mu.Unlock()
	timer := time.NewTimer(a.connectTimeout)
	defer timer.Stop()
	select {
	case err := <-connectionResult:
		if err != nil {
			return err
		}
		return nil
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
	user := a.user
	config := a.config
	userID := a.userID
	token := a.token
	loginStarted := a.loginStarted
	a.user = nil
	a.listener = nil
	a.loginStarted = false
	a.mu.Unlock()
	if user == nil {
		return nil
	}
	var err error
	if loginStarted {
		logoutContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: userID, Token: token, IMConfig: &config})
		err = user.Logout(ccontext.WithOperationID(logoutContext, uuid.NewString()))
	}
	user.UnInitSDK()
	if err != nil {
		return errors.New("OpenIM logout failed")
	}
	return nil
}

// Reply delivers one text response through the daemon-owned SDK context. The
// reply service constructs Delivery exclusively from a persisted reply slot.
func (a *Adapter) Reply(ctx context.Context, delivery reply.Delivery) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(delivery.Text) == "" || (strings.TrimSpace(delivery.RecipientID) == "" && strings.TrimSpace(delivery.GroupID) == "") || (strings.TrimSpace(delivery.RecipientID) != "" && strings.TrimSpace(delivery.GroupID) != "") {
		return errors.New("invalid event-bound reply delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	replyContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendTextMessage(ccontext.WithOperationID(replyContext, uuid.NewString()), callback, delivery.Text, delivery.RecipientID, delivery.GroupID); err != nil {
		return errors.New("OpenIM reply submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return reply.ErrOutcomeUnknown
	}
}

// SendText delivers a grant-authorized text message through the daemon-owned
// SDK context. Authorization, target selection, and idempotency are enforced
// by the message capability handler before this method is reached.
func (a *Adapter) SendText(ctx context.Context, text, recipientID, groupID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" || len(text) > 4096 || (strings.TrimSpace(recipientID) == "" && strings.TrimSpace(groupID) == "") || (strings.TrimSpace(recipientID) != "" && strings.TrimSpace(groupID) != "") {
		return errors.New("invalid text message delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendTextMessage(ccontext.WithOperationID(sendContext, uuid.NewString()), callback, text, recipientID, groupID); err != nil {
		return errors.New("OpenIM message send submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return operation.ErrOutcomeUnknown
	}
}

// SendAt delivers a grant-authorized text message that mentions approved
// users in one approved group. The message capability validates all targets
// before this daemon-owned SDK call.
func (a *Adapter) SendAt(ctx context.Context, text, groupID string, mentionUserIDs []string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" || len(text) > 4096 || strings.TrimSpace(groupID) == "" || len(mentionUserIDs) == 0 || len(mentionUserIDs) > 10 {
		return errors.New("invalid text-at message delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendAtMessage(ccontext.WithOperationID(sendContext, uuid.NewString()), callback, text, groupID, append([]string(nil), mentionUserIDs...)); err != nil {
		return errors.New("OpenIM message at submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return operation.ErrOutcomeUnknown
	}
}

// SendQuote delivers a quote whose source message was retrieved and verified
// by a daemon-owned server source. Providers cannot supply the SDK message.
func (a *Adapter) SendQuote(ctx context.Context, text, recipientID, groupID string, quoted *sdk_struct.MsgStruct) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" || len(text) > 4096 || quoted == nil || (strings.TrimSpace(recipientID) == "" && strings.TrimSpace(groupID) == "") || (strings.TrimSpace(recipientID) != "" && strings.TrimSpace(groupID) != "") {
		return errors.New("invalid quote message delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendQuoteMessage(ccontext.WithOperationID(sendContext, uuid.NewString()), callback, text, recipientID, groupID, quoted); err != nil {
		return errors.New("OpenIM message quote submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return operation.ErrOutcomeUnknown
	}
}

// SendLocation delivers a grant-authorized location message through the
// daemon-owned SDK context. The message capability validates coordinates and
// its explicit target before this method is reached.
func (a *Adapter) SendLocation(ctx context.Context, description string, longitude, latitude float64, recipientID, groupID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(description) > 512 || math.IsNaN(longitude) || math.IsInf(longitude, 0) || math.IsNaN(latitude) || math.IsInf(latitude, 0) || longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90 || (strings.TrimSpace(recipientID) == "" && strings.TrimSpace(groupID) == "") || (strings.TrimSpace(recipientID) != "" && strings.TrimSpace(groupID) != "") {
		return errors.New("invalid location message delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendLocationMessage(ccontext.WithOperationID(sendContext, uuid.NewString()), callback, description, longitude, latitude, recipientID, groupID); err != nil {
		return errors.New("OpenIM location message submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return operation.ErrOutcomeUnknown
	}
}

// SendCustom delivers a grant-authorized custom message through the
// daemon-owned SDK context. Its payload is never persisted or logged here.
func (a *Adapter) SendCustom(ctx context.Context, data, extension, description, recipientID, groupID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 4096 || len(extension) > 1024 || len(description) > 512 || (strings.TrimSpace(recipientID) == "" && strings.TrimSpace(groupID) == "") || (strings.TrimSpace(recipientID) != "" && strings.TrimSpace(groupID) != "") {
		return errors.New("invalid custom message delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return err
	}
	callback := newSendCallback()
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	if err := user.SendCustomMessage(ccontext.WithOperationID(sendContext, uuid.NewString()), callback, data, extension, description, recipientID, groupID); err != nil {
		return errors.New("OpenIM custom message submission failed")
	}
	select {
	case err := <-callback.done:
		return err
	case <-ctx.Done():
		return operation.ErrOutcomeUnknown
	}
}

// Context returns a daemon-private SDK context for a typed server API source.
// The returned value must not cross an IPC, MCP, or provider boundary.
func (a *Adapter) Context() context.Context {
	config := a.config
	return ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   a.userID,
		Token:    a.token,
		IMConfig: &config,
	})
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

func (a *Adapter) report(err error) {
	if err != nil && a.onError != nil {
		a.onError(err)
	}
}

func (a *Adapter) finishConnection(err error) {
	a.mu.RLock()
	result := a.connectionResult
	a.mu.RUnlock()
	if result == nil {
		return
	}
	select {
	case result <- err:
	default:
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

func (l connectionListener) OnConnecting()     {}
func (l connectionListener) OnConnectSuccess() { l.adapter.finishConnection(nil) }
func (l connectionListener) OnConnectFailed(int32, string) {
	err := errors.New("OpenIM connection failed")
	l.adapter.finishConnection(err)
	l.adapter.report(err)
}
func (l connectionListener) OnKickedOffline() {
	err := errors.New("OpenIM session was kicked offline")
	l.adapter.finishConnection(err)
	l.adapter.report(err)
}
func (l connectionListener) OnUserTokenExpired() {
	err := errors.New("OpenIM token expired")
	l.adapter.finishConnection(err)
	l.adapter.report(err)
}
func (l connectionListener) OnUserTokenInvalid(string) {
	err := errors.New("OpenIM token is invalid")
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

func (l messageListener) OnRecvC2CReadReceipt(string)    {}
func (l messageListener) OnNewRecvMessageRevoked(string) {}
func (l messageListener) OnRecvOfflineNewMessage(raw string) {
	l.OnRecvNewMessage(raw)
}
func (l messageListener) OnMsgDeleted(string) {}
func (l messageListener) OnRecvOnlineOnlyMessage(raw string) {
	l.OnRecvNewMessage(raw)
}

func (l messageListener) event(raw string) (contracts.SDKEvent, error) {
	var message sdk_struct.MsgStruct
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
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
	data, err := json.Marshal(struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		SenderID       string `json:"sender_id,omitempty"`
		GroupID        string `json:"group_id,omitempty"`
		SessionType    int32  `json:"session_type"`
	}{ConversationID: conversationID, MessageID: messageID, SenderID: message.SendID, GroupID: message.GroupID, SessionType: message.SessionType})
	if err != nil {
		return contracts.SDKEvent{}, errors.New("encode OpenIM message callback")
	}
	return contracts.SDKEvent{
		ProfileID:   l.adapter.profileID,
		Type:        string(contracts.EventMessageReceived),
		OccurredAt:  occurredAt,
		DedupKey:    "openim-message:" + messageID,
		Data:        data,
		MessageText: messageText(message),
	}, nil
}

func messageText(message sdk_struct.MsgStruct) string {
	if message.TextElem != nil {
		return message.TextElem.Content
	}
	if message.AtTextElem != nil {
		return message.AtTextElem.Text
	}
	var text sdk_struct.TextElem
	if json.Unmarshal([]byte(message.Content), &text) == nil {
		return text.Content
	}
	return ""
}

type sendCallback struct {
	once sync.Once
	done chan error
}

func newSendCallback() *sendCallback { return &sendCallback{done: make(chan error, 1)} }

func (c *sendCallback) OnError(int32, string) {
	c.complete(errors.New("OpenIM message delivery failed"))
}
func (c *sendCallback) OnSuccess(string) { c.complete(nil) }
func (*sendCallback) OnProgress(int)     {}

func (c *sendCallback) complete(err error) {
	c.once.Do(func() { c.done <- err })
}

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
var _ reply.Sender = (*Adapter)(nil)
var _ open_im_sdk_callback.OnConnListener = connectionListener{}
var _ open_im_sdk_callback.OnAdvancedMsgListener = messageListener{}
