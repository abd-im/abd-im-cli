package message

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
	pbconstant "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
)

const maxServerHistory = 100

var ErrNotFound = errors.New("message not found")

// Client is the narrow authenticated server-read surface used by SDKSource.
// It has no SDK database methods, so message reads cannot reach local SDK
// tables.
type Client interface {
	Messages(context.Context, string, int) ([]*sdkws.MsgData, error)
}

// OpenIMClient invokes the fixed OpenIM message sequence endpoint using the
// daemon-owned SDK context. It avoids the SDK API helper because that helper
// can log tokens.
type OpenIMClient struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func (c OpenIMClient) Messages(ctx context.Context, conversationID string, limit int) ([]*sdkws.MsgData, error) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, errors.New("OpenIM conversation ID is required")
	}
	if limit <= 0 {
		return nil, errors.New("OpenIM message limit must be positive")
	}
	if limit > maxServerHistory {
		limit = maxServerHistory
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response sdkws.PullMessageBySeqsResp
	err = c.invoke(request, config, "/msg/pull_msg_by_seq", &sdkws.PullMessageBySeqsReq{
		UserID: config.UserID,
		SeqRanges: []*sdkws.SeqRange{{
			ConversationID: conversationID,
			Begin:          1,
			End:            math.MaxInt64,
			Num:            int64(limit),
		}},
		Order: sdkws.PullOrder_PullOrderAsc,
	}, &response)
	if err != nil {
		return nil, err
	}
	if result := response.Msgs[conversationID]; result != nil {
		return append([]*sdkws.MsgData(nil), result.Msgs...), nil
	}
	if result := response.NotificationMsgs[conversationID]; result != nil {
		return append([]*sdkws.MsgData(nil), result.Msgs...), nil
	}
	return nil, nil
}

func (c OpenIMClient) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if c.Context == nil {
		return nil, nil, nil, errors.New("OpenIM SDK context is required")
	}
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

func (c OpenIMClient) invoke(ctx context.Context, config *ccontext.GlobalConfig, path string, input, output any) error {
	endpoint, err := endpointURL(config.ApiAddr, path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal OpenIM request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM request: %w", err)
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("OpenIM request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenIM request failed with status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read OpenIM response: %w", err)
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode OpenIM response: %w", err)
	}
	if envelope.ErrCode != 0 {
		return fmt.Errorf("OpenIM request failed with code %d", envelope.ErrCode)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return fmt.Errorf("decode OpenIM response data: %w", err)
	}
	return nil
}

func endpointURL(apiAddr, path string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + path, nil
}

// SDKSource maps the fixed server response into the typed message service. It
// never calls SDK message methods that read local tables.
type SDKSource struct {
	client Client
}

func NewSDKSource(client Client) (*SDKSource, error) {
	if client == nil {
		return nil, errors.New("OpenIM message client is required")
	}
	return &SDKSource{client: client}, nil
}

func (s *SDKSource) History(ctx context.Context, query HistoryQuery) ([]Message, error) {
	items, err := s.client.Messages(ctx, query.ConversationID, query.Limit)
	if err != nil {
		return nil, err
	}
	return messagesFromSDK(query.ConversationID, items)
}

func (s *SDKSource) Search(ctx context.Context, query HistoryQuery, text string) ([]Message, error) {
	items, err := s.History(ctx, query)
	if err != nil {
		return nil, err
	}
	text = strings.ToLower(text)
	result := make([]Message, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Text), text) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *SDKSource) Get(ctx context.Context, conversationID, messageID string) (Message, error) {
	items, err := s.History(ctx, HistoryQuery{ConversationID: conversationID, Limit: maxServerHistory})
	if err != nil {
		return Message{}, err
	}
	for _, item := range items {
		if item.ID == messageID {
			return item, nil
		}
	}
	return Message{}, fmt.Errorf("%w: %s", ErrNotFound, messageID)
}

func messagesFromSDK(conversationID string, items []*sdkws.MsgData) ([]Message, error) {
	result := make([]Message, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		id := item.ServerMsgID
		if id == "" {
			id = item.ClientMsgID
		}
		if id == "" {
			continue
		}
		sourceID := sourceConversationID(item)
		if sourceID != conversationID {
			return nil, fmt.Errorf("unexpected server message conversation %q", sourceID)
		}
		createdAt := item.SendTime
		if createdAt == 0 {
			createdAt = item.CreateTime
		}
		result = append(result, Message{
			ID:             id,
			ConversationID: conversationID,
			SenderID:       item.SendID,
			Type:           messageType(item.ContentType),
			Text:           messageText(item.ContentType, item.Content),
			CreatedAt:      fromMillis(createdAt),
			Revoked:        item.ContentType == pbconstant.MsgRevokeNotification,
		})
	}
	return result, nil
}

func sourceConversationID(item *sdkws.MsgData) string {
	switch item.SessionType {
	case pbconstant.SingleChatType:
		return pairConversationID("si_", item.SendID, item.RecvID)
	case pbconstant.WriteGroupChatType:
		return "g_" + firstNonEmpty(item.GroupID, item.RecvID)
	case pbconstant.ReadGroupChatType:
		return "sg_" + firstNonEmpty(item.GroupID, item.RecvID)
	case pbconstant.NotificationChatType:
		return pairConversationID("sn_", item.SendID, item.RecvID)
	default:
		return ""
	}
}

func pairConversationID(prefix, first, second string) string {
	if first == "" || second == "" {
		return ""
	}
	pair := []string{first, second}
	sort.Strings(pair)
	return prefix + strings.Join(pair, "_")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func messageType(value int32) string {
	switch value {
	case pbconstant.Text:
		return "text"
	case pbconstant.AtText:
		return "at_text"
	case pbconstant.AdvancedText:
		return "advanced_text"
	case pbconstant.MarkdownText:
		return "markdown"
	case pbconstant.Quote:
		return "quote"
	case pbconstant.MsgRevokeNotification:
		return "revoked"
	default:
		return "unknown"
	}
}

func messageText(contentType int32, content []byte) string {
	switch contentType {
	case pbconstant.Text, pbconstant.MarkdownText:
		var value struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(content, &value) == nil {
			return value.Content
		}
	case pbconstant.AtText, pbconstant.AdvancedText, pbconstant.Quote:
		var value struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &value) == nil {
			return value.Text
		}
	}
	return ""
}

func fromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
