package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/operation"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbconversation "github.com/openimsdk/protocol/conversation"
	"github.com/openimsdk/protocol/wrapperspb"
)

// OpenIMSettings verifies the target through the fixed server-read source,
// then invokes /conversation/set_conversations directly. It never calls the
// SDK conversation API, whose setting path reads and updates the local SDK
// database.
type OpenIMSettings struct {
	Context    func() context.Context
	Client     conversationservice.Client
	HTTPClient *http.Client
}

func NewOpenIMSettings(settings OpenIMSettings) (*OpenIMSettings, error) {
	if settings.Context == nil || settings.Client == nil {
		return nil, errors.New("OpenIM SDK context and conversation client are required")
	}
	return &settings, nil
}

func (c OpenIMSettings) SetPinned(ctx context.Context, input SetPinnedInput) error {
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	if !validIdentifier(input.ConversationID) {
		return errors.New("conversation ID must contain 1-256 bytes")
	}
	return c.set(ctx, input.ConversationID, func(item *pbconversation.Conversation) *pbconversation.ConversationReq {
		return &pbconversation.ConversationReq{ConversationID: input.ConversationID, ConversationType: item.ConversationType, UserID: item.UserID, GroupID: item.GroupID, IsPinned: wrapperspb.Bool(input.Pinned)}
	})
}

func (c OpenIMSettings) SetReceiveOption(ctx context.Context, input SetReceiveOptionInput) error {
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Option = ReceiveOption(strings.TrimSpace(string(input.Option)))
	if !validIdentifier(input.ConversationID) {
		return errors.New("conversation ID must contain 1-256 bytes")
	}
	option, err := openIMReceiveOption(input.Option)
	if err != nil {
		return err
	}
	return c.set(ctx, input.ConversationID, func(item *pbconversation.Conversation) *pbconversation.ConversationReq {
		return &pbconversation.ConversationReq{ConversationID: input.ConversationID, ConversationType: item.ConversationType, UserID: item.UserID, GroupID: item.GroupID, RecvMsgOpt: wrapperspb.Int32(option)}
	})
}

func (c OpenIMSettings) set(caller context.Context, conversationID string, setting func(*pbconversation.Conversation) *pbconversation.ConversationReq) error {
	conversation, err := c.verifyConversation(caller, conversationID)
	if err != nil {
		return err
	}
	request, config, done, err := c.requestContext(caller)
	if err != nil {
		return err
	}
	defer done()

	endpoint, err := conversationSettingsEndpoint(config.ApiAddr)
	if err != nil {
		return err
	}
	body, err := json.Marshal(&pbconversation.SetConversationsReq{
		UserIDs:      []string{config.UserID},
		Conversation: setting(conversation),
	})
	if err != nil {
		return errors.New("encode OpenIM conversation setting request")
	}
	httpRequest, err := http.NewRequestWithContext(request, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OpenIM conversation setting request")
	}
	operationID, _ := request.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("operationID", operationID)
	httpRequest.Header.Set("token", config.Token)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return unknownSettingsOutcome()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return unknownSettingsOutcome()
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return unknownSettingsOutcome()
	}
	var envelope struct {
		ErrCode *int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownSettingsOutcome()
	}
	if envelope.ErrCode == nil {
		return unknownSettingsOutcome()
	}
	if *envelope.ErrCode != 0 {
		return errors.New("OpenIM conversation setting rejected")
	}
	return nil
}

func (c OpenIMSettings) verifyConversation(ctx context.Context, conversationID string) (*pbconversation.Conversation, error) {
	items, err := c.Client.Conversations(ctx, []string{conversationID})
	if err != nil {
		return nil, errors.New("verify server conversation")
	}
	for _, item := range items {
		if item != nil && item.ConversationID == conversationID {
			if item.ConversationType == 0 || (item.ConversationType == pbconstant.SingleChatType && item.UserID == "") || (item.ConversationType == pbconstant.ReadGroupChatType && item.GroupID == "") {
				return nil, errors.New("server conversation identity is incomplete")
			}
			return item, nil
		}
	}
	return nil, errors.New("server conversation was not found")
}

func (c OpenIMSettings) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if caller == nil {
		return nil, nil, nil, errors.New("caller context is required")
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	base := c.Context()
	if base == nil {
		return nil, nil, nil, errors.New("OpenIM SDK context is nil")
	}
	config, ok := base.Value(ccontext.GlobalConfigKey{}).(*ccontext.GlobalConfig)
	if !ok || config == nil || strings.TrimSpace(config.UserID) == "" || strings.TrimSpace(config.Token) == "" || strings.TrimSpace(config.ApiAddr) == "" {
		return nil, nil, nil, errors.New("OpenIM SDK context is not authenticated")
	}
	request, cancel := context.WithCancel(base)
	stop := context.AfterFunc(caller, cancel)
	return ccontext.WithOperationID(request, uuid.NewString()), config, func() {
		stop()
		cancel()
	}, nil
}

func openIMReceiveOption(option ReceiveOption) (int32, error) {
	switch option {
	case ReceiveOptionReceive:
		return pbconstant.ReceiveMessage, nil
	case ReceiveOptionDoNotReceive:
		return pbconstant.NotReceiveMessage, nil
	case ReceiveOptionReceiveNoNotify:
		return pbconstant.ReceiveNotNotifyMessage, nil
	default:
		return 0, errors.New("receive option is not supported")
	}
}

func conversationSettingsEndpoint(apiAddr string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + "/conversation/set_conversations", nil
}

func unknownSettingsOutcome() error {
	return fmt.Errorf("OpenIM conversation setting did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ SettingsSender = (*OpenIMSettings)(nil)
