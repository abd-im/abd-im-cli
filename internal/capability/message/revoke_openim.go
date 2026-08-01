package message

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
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/converter"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/utils"
	"github.com/google/uuid"
	pbmsg "github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/protocol/sdkws"
)

const revokeHistoryLimit = 100

// OpenIMRevoke verifies the exact server message before posting the fixed
// OpenIM revoke endpoint. It has no local SDK message access.
type OpenIMRevoke struct {
	Context    func() context.Context
	Client     messageservice.Client
	HTTPClient *http.Client
	selfID     string
}

func NewOpenIMRevoke(action OpenIMRevoke, selfID string) (*OpenIMRevoke, error) {
	if action.Context == nil || action.Client == nil || strings.TrimSpace(selfID) == "" {
		return nil, errors.New("OpenIM SDK context, message client, and self ID are required")
	}
	action.selfID = selfID
	return &action, nil
}

func (s *OpenIMRevoke) Revoke(ctx context.Context, input RevokeInput) error {
	if s == nil {
		return errors.New("OpenIM message revoker is required")
	}
	items, err := s.Client.Messages(ctx, input.ConversationID, revokeHistoryLimit)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item == nil {
			return errors.New("OpenIM revoke source returned an empty message")
		}
		messageID, err := revokeMessageID(item, input.ConversationID)
		if err != nil {
			return err
		}
		if messageID != input.MessageID {
			continue
		}
		if item.SendID != s.selfID || item.Seq < 1 {
			return errors.New("only a profile-owned server message can be revoked")
		}
		return s.post(ctx, input.ConversationID, item.Seq)
	}
	return errors.New("OpenIM message to revoke was not found")
}

func (s *OpenIMRevoke) post(caller context.Context, conversationID string, sequence int64) error {
	request, config, done, err := s.requestContext(caller)
	if err != nil {
		return err
	}
	defer done()
	if config.UserID != s.selfID {
		return errors.New("OpenIM authenticated user does not match profile owner")
	}
	endpoint, err := revokeEndpoint(config.ApiAddr)
	if err != nil {
		return err
	}
	body, err := json.Marshal(&pbmsg.RevokeMsgReq{UserID: config.UserID, ConversationID: conversationID, Seq: sequence})
	if err != nil {
		return errors.New("encode OpenIM revoke request")
	}
	httpRequest, err := http.NewRequestWithContext(request, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OpenIM revoke request")
	}
	operationID, _ := request.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("operationID", operationID)
	httpRequest.Header.Set("token", config.Token)
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return unknownRevokeOutcome()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return unknownRevokeOutcome()
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return unknownRevokeOutcome()
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownRevokeOutcome()
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM revoke rejected")
	}
	return nil
}

func (s *OpenIMRevoke) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if caller == nil {
		return nil, nil, nil, errors.New("caller context is required")
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	base := s.Context()
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

func revokeMessageID(item *sdkws.MsgData, conversationID string) (string, error) {
	message := converter.MsgDataToMsgStruct(item)
	if message == nil || utils.GetConversationIDByMsg(message) != conversationID {
		return "", errors.New("OpenIM revoke source returned an unexpected conversation")
	}
	if id := strings.TrimSpace(item.ServerMsgID); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(item.ClientMsgID); id != "" {
		return id, nil
	}
	return "", errors.New("OpenIM revoke source message has no ID")
}

func revokeEndpoint(apiAddr string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + "/msg/revoke_msg", nil
}

func unknownRevokeOutcome() error {
	return fmt.Errorf("OpenIM revoke did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ Revoker = (*OpenIMRevoke)(nil)
