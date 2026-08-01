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
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/converter"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/utils"
	"github.com/google/uuid"
	pbmsg "github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/protocol/sdkws"
)

const markReadHistoryLimit = 100

// OpenIMMarkRead resolves a server message boundary and performs the fixed
// mark-conversation-as-read action without accessing the SDK local store.
type OpenIMMarkRead struct {
	Context    func() context.Context
	Client     messageservice.Client
	HTTPClient *http.Client
}

func NewOpenIMMarkRead(action OpenIMMarkRead) (*OpenIMMarkRead, error) {
	if action.Context == nil || action.Client == nil {
		return nil, errors.New("OpenIM SDK context and message client are required")
	}
	return &action, nil
}

func (c OpenIMMarkRead) ResolveBoundary(ctx context.Context, conversationID, messageID string) (Boundary, error) {
	items, err := c.Client.Messages(ctx, conversationID, markReadHistoryLimit)
	if err != nil {
		return Boundary{}, err
	}
	for _, item := range items {
		boundary, err := markReadBoundary(item, conversationID)
		if err != nil {
			return Boundary{}, err
		}
		if boundary.MessageID == messageID {
			return boundary, nil
		}
	}
	return Boundary{}, errors.New("OpenIM message boundary was not found")
}

func (c OpenIMMarkRead) MarkRead(ctx context.Context, input MarkReadRequest) error {
	if strings.TrimSpace(input.ConversationID) == "" || input.HasReadSeq < 1 {
		return errors.New("conversation ID and positive read sequence are required")
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()

	endpoint, err := markReadEndpoint(config.ApiAddr)
	if err != nil {
		return err
	}
	body, err := json.Marshal(&pbmsg.MarkConversationAsReadReq{
		UserID:         config.UserID,
		ConversationID: input.ConversationID,
		HasReadSeq:     input.HasReadSeq,
	})
	if err != nil {
		return errors.New("encode OpenIM mark read request")
	}
	httpRequest, err := http.NewRequestWithContext(request, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OpenIM mark read request")
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
		return unknownMarkReadOutcome()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return unknownMarkReadOutcome()
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return unknownMarkReadOutcome()
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownMarkReadOutcome()
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM mark read rejected")
	}
	return nil
}

func (c OpenIMMarkRead) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
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

func markReadBoundary(item *sdkws.MsgData, conversationID string) (Boundary, error) {
	message := converter.MsgDataToMsgStruct(item)
	if message == nil || utils.GetConversationIDByMsg(message) != conversationID {
		return Boundary{}, errors.New("OpenIM message boundary has an unexpected conversation")
	}
	messageID := strings.TrimSpace(item.ServerMsgID)
	if messageID == "" {
		messageID = strings.TrimSpace(item.ClientMsgID)
	}
	if messageID == "" || item.Seq < 1 {
		return Boundary{}, errors.New("OpenIM message boundary is incomplete")
	}
	return Boundary{ConversationID: conversationID, MessageID: messageID, ServerSeq: item.Seq}, nil
}

func markReadEndpoint(apiAddr string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + "/msg/mark_conversation_as_read", nil
}

func unknownMarkReadOutcome() error {
	return fmt.Errorf("OpenIM mark read did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ BoundaryResolver = (*OpenIMMarkRead)(nil)
var _ Sender = (*OpenIMMarkRead)(nil)
