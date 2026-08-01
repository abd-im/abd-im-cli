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

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/google/uuid"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

// OpenIMMembershipSource invokes only fixed group member read/action
// endpoints through the daemon-owned SDK context. It never calls the SDK
// Group API, whose successful mutations synchronize local SDK state.
type OpenIMMembershipSource struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func NewOpenIMMembershipSource(source OpenIMMembershipSource) (*OpenIMMembershipSource, error) {
	if source.Context == nil {
		return nil, errors.New("OpenIM SDK context is required")
	}
	return &source, nil
}

func (s OpenIMMembershipSource) JoinGroup(ctx context.Context, input JoinInput) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/group/join_group", &pbgroup.JoinGroupReq{
		GroupID:       input.GroupID,
		ReqMessage:    input.Message,
		JoinSource:    pbconstant.JoinBySearch,
		InviterUserID: config.UserID,
	})
}

func (s OpenIMMembershipSource) LeaveGroup(ctx context.Context, input LeaveInput) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/group/quit_group", &pbgroup.QuitGroupReq{GroupID: input.GroupID, UserID: config.UserID})
}

func (s OpenIMMembershipSource) InviteMembers(ctx context.Context, input MembersInput) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/group/invite_user_to_group", &pbgroup.InviteUserToGroupReq{
		GroupID:        input.GroupID,
		Reason:         input.Reason,
		InvitedUserIDs: append([]string(nil), input.UserIDs...),
	})
}

func (s OpenIMMembershipSource) RemoveMembers(ctx context.Context, input MembersInput) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/group/kick_group", &pbgroup.KickGroupMemberReq{
		GroupID:       input.GroupID,
		KickedUserIDs: append([]string(nil), input.UserIDs...),
		Reason:        input.Reason,
	})
}

func (s OpenIMMembershipSource) CanLeaveGroup(ctx context.Context, groupID string) (bool, error) {
	config, members, err := s.members(ctx, groupID, nil)
	if err != nil {
		return false, err
	}
	actor, ok := members[config.UserID]
	return ok && actor.RoleLevel != constant.GroupOwner, nil
}

func (s OpenIMMembershipSource) CanInviteMembers(ctx context.Context, groupID string, userIDs []string) (bool, error) {
	config, members, err := s.members(ctx, groupID, userIDs)
	if err != nil {
		return false, err
	}
	actor, ok := members[config.UserID]
	if !ok || (actor.RoleLevel != constant.GroupOwner && actor.RoleLevel != constant.GroupAdmin) {
		return false, nil
	}
	for _, userID := range userIDs {
		if _, exists := members[userID]; exists {
			return false, nil
		}
	}
	return true, nil
}

func (s OpenIMMembershipSource) CanRemoveMembers(ctx context.Context, groupID string, userIDs []string) (bool, error) {
	config, members, err := s.members(ctx, groupID, userIDs)
	if err != nil {
		return false, err
	}
	actor, ok := members[config.UserID]
	if !ok || (actor.RoleLevel != constant.GroupOwner && actor.RoleLevel != constant.GroupAdmin) {
		return false, nil
	}
	for _, userID := range userIDs {
		member, exists := members[userID]
		if !exists || userID == config.UserID || member.RoleLevel == constant.GroupOwner {
			return false, nil
		}
		if actor.RoleLevel == constant.GroupAdmin && member.RoleLevel == constant.GroupAdmin {
			return false, nil
		}
	}
	return true, nil
}

func (s OpenIMMembershipSource) members(ctx context.Context, groupID string, userIDs []string) (*ccontext.GlobalConfig, map[string]*sdkws.GroupMemberFullInfo, error) {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	ids := make([]string, 0, len(userIDs)+1)
	ids = append(ids, config.UserID)
	for _, userID := range userIDs {
		if userID != config.UserID {
			ids = append(ids, userID)
		}
	}
	var response pbgroup.GetGroupMembersInfoResp
	if err := s.invokeRead(request, config, "/group/get_group_members_info", &pbgroup.GetGroupMembersInfoReq{GroupID: groupID, UserIDs: ids}, &response); err != nil {
		return nil, nil, err
	}
	members := make(map[string]*sdkws.GroupMemberFullInfo, len(response.Members))
	for _, member := range response.Members {
		if member != nil && member.GroupID == groupID && member.UserID != "" {
			members[member.UserID] = member
		}
	}
	return config, members, nil
}

func (s OpenIMMembershipSource) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
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

func (s OpenIMMembershipSource) invokeAction(ctx context.Context, config *ccontext.GlobalConfig, path string, input any) error {
	status, payload, err := s.post(ctx, config, path, input)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return unknownMembershipOutcome()
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownMembershipOutcome()
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM group membership action rejected")
	}
	return nil
}

func (s OpenIMMembershipSource) invokeRead(ctx context.Context, config *ccontext.GlobalConfig, path string, input, output any) error {
	status, payload, err := s.post(ctx, config, path, input)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return errors.New("OpenIM group membership state read failed")
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return errors.New("decode OpenIM group membership state response")
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM group membership state read rejected")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return errors.New("decode OpenIM group membership state data")
	}
	return nil
}

func (s OpenIMMembershipSource) post(ctx context.Context, config *ccontext.GlobalConfig, path string, input any) (int, []byte, error) {
	endpoint, err := groupMembershipEndpoint(config.ApiAddr, path)
	if err != nil {
		return 0, nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal OpenIM group membership request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("create OpenIM group membership request: %w", err)
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return 0, nil, errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, payload, nil
}

func groupMembershipEndpoint(apiAddr, path string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + path, nil
}

func unknownMembershipOutcome() error {
	return fmt.Errorf("OpenIM group membership action did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ MembershipSource = (*OpenIMMembershipSource)(nil)
