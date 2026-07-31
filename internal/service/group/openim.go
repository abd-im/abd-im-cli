package group

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
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/google/uuid"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

var ErrNotFound = errors.New("group not found")

// Client is the small server-read surface used by SDKSource. It deliberately
// has no SDK database methods, so group reads cannot reach local SDK tables.
type Client interface {
	JoinedGroups(context.Context) ([]*sdkws.GroupInfo, error)
	Groups(context.Context, []string) ([]*sdkws.GroupInfo, error)
	GroupMembers(context.Context, string, string) ([]*sdkws.GroupMemberFullInfo, error)
}

// OpenIMClient invokes the fixed OpenIM server API using the daemon-owned SDK
// context. It does not use the SDK's API helper because that helper logs tokens.
type OpenIMClient struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func (c OpenIMClient) JoinedGroups(ctx context.Context) ([]*sdkws.GroupInfo, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var result []*sdkws.GroupInfo
	for page := int32(1); ; page++ {
		var response pbgroup.GetJoinedGroupListResp
		err := c.invoke(request, config, "/group/get_joined_group_list", &pbgroup.GetJoinedGroupListReq{
			FromUserID: config.UserID,
			Pagination: &sdkws.RequestPagination{PageNumber: page, ShowNumber: 100},
		}, &response)
		if err != nil {
			return nil, err
		}
		result = append(result, response.Groups...)
		if len(response.Groups) < 100 || response.Total == 0 || len(result) >= int(response.Total) {
			return result, nil
		}
	}
}

func (c OpenIMClient) Groups(ctx context.Context, ids []string) ([]*sdkws.GroupInfo, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response pbgroup.GetGroupsInfoResp
	err = c.invoke(request, config, "/group/get_groups_info", &pbgroup.GetGroupsInfoReq{GroupIDs: append([]string(nil), ids...)}, &response)
	return response.GroupInfos, err
}

func (c OpenIMClient) GroupMembers(ctx context.Context, groupID, query string) ([]*sdkws.GroupMemberFullInfo, error) {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var result []*sdkws.GroupMemberFullInfo
	for page := int32(1); ; page++ {
		var response pbgroup.GetGroupMemberListResp
		err := c.invoke(request, config, "/group/get_group_member_list", &pbgroup.GetGroupMemberListReq{
			GroupID:    groupID,
			Filter:     constant.GroupFilterAll,
			Keyword:    query,
			Pagination: &sdkws.RequestPagination{PageNumber: page, ShowNumber: 100},
		}, &response)
		if err != nil {
			return nil, err
		}
		result = append(result, response.Members...)
		if len(response.Members) < 100 || response.Total == 0 || len(result) >= int(response.Total) {
			return result, nil
		}
	}
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

// SDKSource maps direct server API responses into the typed group service.
// It never calls SDK Group methods that read the SDK's local database.
type SDKSource struct {
	client Client
	now    func() time.Time
}

func NewSDKSource(client Client) (*SDKSource, error) {
	if client == nil {
		return nil, errors.New("OpenIM group client is required")
	}
	return &SDKSource{client: client, now: time.Now}, nil
}

func (s *SDKSource) List(ctx context.Context) ([]Group, error) {
	items, err := s.client.JoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	return groupsFromSDK(items), nil
}

func (s *SDKSource) Get(ctx context.Context, id string) (Group, error) {
	items, err := s.client.Groups(ctx, []string{id})
	if err != nil {
		return Group{}, err
	}
	for _, item := range groupsFromSDK(items) {
		if item.ID == id {
			return item, nil
		}
	}
	return Group{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s *SDKSource) Search(ctx context.Context, query string) ([]Group, error) {
	items, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	result := make([]Group, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.ID), query) || strings.Contains(strings.ToLower(item.Name), query) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *SDKSource) Members(ctx context.Context, groupID string) ([]Member, error) {
	items, err := s.client.GroupMembers(ctx, groupID, "")
	if err != nil {
		return nil, err
	}
	return s.membersFromSDK(groupID, items)
}

func (s *SDKSource) SearchMembers(ctx context.Context, groupID, query string) ([]Member, error) {
	items, err := s.client.GroupMembers(ctx, groupID, query)
	if err != nil {
		return nil, err
	}
	return s.membersFromSDK(groupID, items)
}

func groupsFromSDK(items []*sdkws.GroupInfo) []Group {
	result := make([]Group, 0, len(items))
	for _, item := range items {
		if item == nil || item.GroupID == "" {
			continue
		}
		result = append(result, Group{
			ID:          item.GroupID,
			Name:        item.GroupName,
			OwnerID:     item.OwnerUserID,
			MemberCount: int(item.MemberCount),
			CreatedAt:   fromMillis(item.CreateTime),
		})
	}
	return result
}

func (s *SDKSource) membersFromSDK(groupID string, items []*sdkws.GroupMemberFullInfo) ([]Member, error) {
	result := make([]Member, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.GroupID != groupID {
			return nil, fmt.Errorf("unexpected group member response %q", item.GroupID)
		}
		result = append(result, Member{
			GroupID:  item.GroupID,
			UserID:   item.UserID,
			Nickname: item.Nickname,
			Role:     roleFromSDK(item.RoleLevel),
			JoinedAt: fromMillis(item.JoinTime),
			Muted:    item.MuteEndTime > s.now().UnixMilli(),
		})
	}
	return result, nil
}

func fromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func roleFromSDK(role int32) string {
	switch role {
	case constant.GroupOwner:
		return "owner"
	case constant.GroupAdmin:
		return "admin"
	case constant.GroupOrdinaryUsers:
		return "member"
	default:
		return "unknown"
	}
}
