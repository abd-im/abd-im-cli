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

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbconversation "github.com/openimsdk/protocol/conversation"
)

var (
	ErrNotFound          = errors.New("conversation not found")
	ErrUnreadUnavailable = errors.New("server conversation API does not expose unread count")
)

// Client is the narrow authenticated server-read surface used by SDKSource.
// It has no SDK database methods, so conversation reads cannot reach local
// SDK tables.
type Client interface {
	AllConversations(context.Context) ([]*pbconversation.Conversation, error)
	Conversations(context.Context, []string) ([]*pbconversation.Conversation, error)
}

// OpenIMClient invokes the fixed OpenIM conversation endpoints using the
// daemon-owned SDK context. It avoids the SDK API helper because that helper
// can log tokens.
type OpenIMClient struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func (c OpenIMClient) AllConversations(ctx context.Context) ([]*pbconversation.Conversation, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response pbconversation.GetAllConversationsResp
	err = c.invoke(request, config, "/conversation/get_all_conversations", &pbconversation.GetAllConversationsReq{OwnerUserID: config.UserID}, &response)
	return response.Conversations, err
}

func (c OpenIMClient) Conversations(ctx context.Context, ids []string) ([]*pbconversation.Conversation, error) {
	if len(ids) == 0 {
		return nil, errors.New("OpenIM conversation IDs are required")
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response pbconversation.GetConversationsResp
	err = c.invoke(request, config, "/conversation/get_conversations", &pbconversation.GetConversationsReq{OwnerUserID: config.UserID, ConversationIDs: append([]string(nil), ids...)}, &response)
	return response.Conversations, err
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

// SDKSource maps the fixed server responses into the typed conversation
// service. It never calls SDK conversation methods that read local tables.
type SDKSource struct {
	client Client
}

func NewSDKSource(client Client) (*SDKSource, error) {
	if client == nil {
		return nil, errors.New("OpenIM conversation client is required")
	}
	return &SDKSource{client: client}, nil
}

func (s *SDKSource) List(ctx context.Context) ([]Conversation, error) {
	items, err := s.client.AllConversations(ctx)
	if err != nil {
		return nil, err
	}
	return conversationsFromSDK(items), nil
}

func (s *SDKSource) Get(ctx context.Context, id string) (Conversation, error) {
	items, err := s.client.Conversations(ctx, []string{id})
	if err != nil {
		return Conversation{}, err
	}
	for _, item := range conversationsFromSDK(items) {
		if item.ID == id {
			return item, nil
		}
	}
	return Conversation{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s *SDKSource) Search(ctx context.Context, query string) ([]Conversation, error) {
	items, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	result := make([]Conversation, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.ID), query) || strings.Contains(strings.ToLower(item.Type), query) || strings.Contains(strings.ToLower(item.UserID), query) || strings.Contains(strings.ToLower(item.GroupID), query) {
			result = append(result, item)
		}
	}
	return result, nil
}

// Unread deliberately fails: OpenIM keeps unread counts in the local SDK
// database, while the verified conversation endpoints only expose server-side
// conversation settings. Its capability consequently remains not_validated.
func (s *SDKSource) Unread(context.Context) (int, error) {
	return 0, ErrUnreadUnavailable
}

func conversationsFromSDK(items []*pbconversation.Conversation) []Conversation {
	result := make([]Conversation, 0, len(items))
	for _, item := range items {
		if item == nil || item.ConversationID == "" {
			continue
		}
		result = append(result, Conversation{
			ID:      item.ConversationID,
			Type:    conversationType(item.ConversationType),
			UserID:  item.UserID,
			GroupID: item.GroupID,
			Pinned:  item.IsPinned,
			Muted:   item.RecvMsgOpt == pbconstant.ReceiveNotNotifyMessage,
		})
	}
	return result
}

func conversationType(value int32) string {
	switch value {
	case pbconstant.SingleChatType:
		return "single"
	case pbconstant.WriteGroupChatType:
		return "group_write"
	case pbconstant.ReadGroupChatType:
		return "group_read"
	case pbconstant.NotificationChatType:
		return "notification"
	default:
		return "unknown"
	}
}
