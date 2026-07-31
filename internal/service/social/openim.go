package social

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
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
	"github.com/openimsdk/protocol/relation"
	"github.com/openimsdk/protocol/sdkws"
)

const serverPageSize = 100

var ErrNotFound = errors.New("social entry not found")

// Client is the narrow authenticated server-read surface used by SDKSource.
// It has no SDK database methods, so social reads cannot reach local SDK
// tables.
type Client interface {
	Friends(context.Context) ([]*sdkws.FriendInfo, error)
	FriendsByID(context.Context, []string) ([]*sdkws.FriendInfo, error)
	Blacklist(context.Context) ([]*sdkws.BlackInfo, error)
	BlacksByID(context.Context, []string) ([]*sdkws.BlackInfo, error)
}

// OpenIMClient invokes the fixed OpenIM friend and blacklist endpoints using
// the daemon-owned SDK context. It avoids the SDK API helper because that
// helper can log tokens.
type OpenIMClient struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func (c OpenIMClient) Friends(ctx context.Context) ([]*sdkws.FriendInfo, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var result []*sdkws.FriendInfo
	for page := int32(1); ; page++ {
		var response relation.GetPaginationFriendsResp
		if err := c.invoke(request, config, "/friend/get_friend_list", &relation.GetPaginationFriendsReq{
			UserID:     config.UserID,
			Pagination: &sdkws.RequestPagination{PageNumber: page, ShowNumber: serverPageSize},
		}, &response); err != nil {
			return nil, err
		}
		result = append(result, response.FriendsInfo...)
		if len(response.FriendsInfo) < serverPageSize || response.Total == 0 || len(result) >= int(response.Total) {
			return result, nil
		}
	}
}

func (c OpenIMClient) FriendsByID(ctx context.Context, ids []string) ([]*sdkws.FriendInfo, error) {
	if len(ids) == 0 {
		return nil, errors.New("OpenIM friend user IDs are required")
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response relation.GetDesignatedFriendsResp
	if err := c.invoke(request, config, "/friend/get_designated_friends", &relation.GetDesignatedFriendsReq{
		OwnerUserID:   config.UserID,
		FriendUserIDs: append([]string(nil), ids...),
	}, &response); err != nil {
		return nil, err
	}
	return response.FriendsInfo, nil
}

func (c OpenIMClient) Blacklist(ctx context.Context) ([]*sdkws.BlackInfo, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var result []*sdkws.BlackInfo
	for page := int32(1); ; page++ {
		var response relation.GetPaginationBlacksResp
		if err := c.invoke(request, config, "/friend/get_black_list", &relation.GetPaginationBlacksReq{
			UserID:     config.UserID,
			Pagination: &sdkws.RequestPagination{PageNumber: page, ShowNumber: serverPageSize},
		}, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Blacks...)
		if len(response.Blacks) < serverPageSize || response.Total == 0 || len(result) >= int(response.Total) {
			return result, nil
		}
	}
}

func (c OpenIMClient) BlacksByID(ctx context.Context, ids []string) ([]*sdkws.BlackInfo, error) {
	if len(ids) == 0 {
		return nil, errors.New("OpenIM blacklist user IDs are required")
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response relation.GetSpecifiedBlacksResp
	if err := c.invoke(request, config, "/friend/get_specified_blacks", &relation.GetSpecifiedBlacksReq{
		OwnerUserID: config.UserID,
		UserIDList:  append([]string(nil), ids...),
	}, &response); err != nil {
		return nil, err
	}
	return response.Blacks, nil
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

// SDKSource maps fixed server responses into the typed social service. It
// never calls SDK friend or blacklist methods that read local tables.
type SDKSource struct {
	client Client
}

func NewSDKSource(client Client) (*SDKSource, error) {
	if client == nil {
		return nil, errors.New("OpenIM social client is required")
	}
	return &SDKSource{client: client}, nil
}

func (s *SDKSource) Friends(ctx context.Context) ([]Friend, error) {
	items, err := s.client.Friends(ctx)
	if err != nil {
		return nil, err
	}
	return friendsFromSDK(items), nil
}

func (s *SDKSource) Friend(ctx context.Context, userID string) (Friend, error) {
	items, err := s.client.FriendsByID(ctx, []string{userID})
	if err != nil {
		return Friend{}, err
	}
	for _, item := range friendsFromSDK(items) {
		if item.UserID == userID {
			return item, nil
		}
	}
	return Friend{}, fmt.Errorf("%w: %s", ErrNotFound, userID)
}

func (s *SDKSource) SearchFriends(ctx context.Context, query string) ([]Friend, error) {
	items, err := s.Friends(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	result := make([]Friend, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.UserID), query) || strings.Contains(strings.ToLower(item.Nickname), query) || strings.Contains(strings.ToLower(item.Remark), query) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *SDKSource) Blacklist(ctx context.Context) ([]BlacklistEntry, error) {
	items, err := s.client.Blacklist(ctx)
	if err != nil {
		return nil, err
	}
	return blacksFromSDK(items), nil
}

func (s *SDKSource) Black(ctx context.Context, userID string) (BlacklistEntry, error) {
	items, err := s.client.BlacksByID(ctx, []string{userID})
	if err != nil {
		return BlacklistEntry{}, err
	}
	for _, item := range blacksFromSDK(items) {
		if item.UserID == userID {
			return item, nil
		}
	}
	return BlacklistEntry{}, fmt.Errorf("%w: %s", ErrNotFound, userID)
}

func friendsFromSDK(items []*sdkws.FriendInfo) []Friend {
	result := make([]Friend, 0, len(items))
	for _, item := range items {
		if item == nil || item.FriendUser == nil || strings.TrimSpace(item.FriendUser.UserID) == "" {
			continue
		}
		result = append(result, Friend{
			UserID:   item.FriendUser.UserID,
			Nickname: item.FriendUser.Nickname,
			Remark:   item.Remark,
			AddedAt:  fromMillis(item.CreateTime),
		})
	}
	return result
}

func blacksFromSDK(items []*sdkws.BlackInfo) []BlacklistEntry {
	result := make([]BlacklistEntry, 0, len(items))
	for _, item := range items {
		if item == nil || item.BlackUserInfo == nil || strings.TrimSpace(item.BlackUserInfo.UserID) == "" {
			continue
		}
		result = append(result, BlacklistEntry{UserID: item.BlackUserInfo.UserID, BlockedAt: fromMillis(item.CreateTime)})
	}
	return result
}

func fromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
