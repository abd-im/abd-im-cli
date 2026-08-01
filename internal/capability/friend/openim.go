package friend

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
	"github.com/google/uuid"
	"github.com/openimsdk/protocol/relation"
)

const (
	friendResponseAccept int32 = 1
	friendResponseReject int32 = -1
)

// OpenIMActions performs fixed friend lifecycle requests through the
// daemon-owned SDK authentication context. It never calls the SDK relation API
// because that API synchronizes the local SDK database after mutations.
type OpenIMActions struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func NewOpenIMActions(actions OpenIMActions) (*OpenIMActions, error) {
	if actions.Context == nil {
		return nil, errors.New("OpenIM SDK context is required")
	}
	return &actions, nil
}

func (a OpenIMActions) RequestFriend(ctx context.Context, input RequestInput) error {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	if input.UserID == config.UserID {
		return errors.New("profile owner cannot request itself")
	}
	return a.action(request, config, "/friend/add_friend", &relation.ApplyToAddFriendReq{
		FromUserID: config.UserID,
		ToUserID:   input.UserID,
		ReqMsg:     input.Message,
	})
}

func (a OpenIMActions) RespondFriend(ctx context.Context, input RespondInput) error {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	result := friendResponseReject
	if input.Response == "accept" {
		result = friendResponseAccept
	}
	return a.action(request, config, "/friend/add_friend_response", &relation.RespondFriendApplyReq{
		FromUserID:   input.UserID,
		ToUserID:     config.UserID,
		HandleResult: result,
		HandleMsg:    input.Message,
	})
}

func (a OpenIMActions) DeleteFriend(ctx context.Context, input DeleteInput) error {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return a.action(request, config, "/friend/delete_friend", &relation.DeleteFriendReq{
		OwnerUserID:  config.UserID,
		FriendUserID: input.UserID,
	})
}

func (a OpenIMActions) SetFriendRemark(ctx context.Context, input SetRemarkInput) error {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return a.action(request, config, "/friend/set_friend_remark", &relation.SetFriendRemarkReq{
		OwnerUserID:  config.UserID,
		FriendUserID: input.UserID,
		Remark:       input.Remark,
	})
}

// HasPendingRequest queries the one server-side request that the profile owner
// is permitted to respond to. Handled, reversed, or mismatched requests are
// not accepted as proof.
func (a OpenIMActions) HasPendingRequest(ctx context.Context, userID string) (bool, error) {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	var response relation.GetDesignatedFriendsApplyResp
	if err := a.read(request, config, "/friend/get_designated_friend_apply", &relation.GetDesignatedFriendsApplyReq{
		FromUserID: userID,
		ToUserID:   config.UserID,
	}, &response); err != nil {
		return false, err
	}
	for _, item := range response.FriendRequests {
		if item != nil && item.GetFromUserID() == userID && item.GetToUserID() == config.UserID && item.GetHandleResult() == 0 {
			return true, nil
		}
	}
	return false, nil
}

// HasFriend queries only the designated relationship for the authenticated
// owner before deletion. It avoids any local friend cache.
func (a OpenIMActions) HasFriend(ctx context.Context, userID string) (bool, error) {
	request, config, done, err := a.requestContext(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	var response relation.GetDesignatedFriendsResp
	if err := a.read(request, config, "/friend/get_designated_friends", &relation.GetDesignatedFriendsReq{
		OwnerUserID:   config.UserID,
		FriendUserIDs: []string{userID},
	}, &response); err != nil {
		return false, err
	}
	for _, item := range response.FriendsInfo {
		if item != nil && item.GetOwnerUserID() == config.UserID && item.GetFriendUser() != nil && item.GetFriendUser().GetUserID() == userID {
			return true, nil
		}
	}
	return false, nil
}

func (a OpenIMActions) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if caller == nil {
		return nil, nil, nil, errors.New("caller context is required")
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	base := a.Context()
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

func (a OpenIMActions) action(ctx context.Context, config *ccontext.GlobalConfig, path string, input any) error {
	status, payload, err := a.post(ctx, config, path, input)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return unknownFriendOutcome()
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownFriendOutcome()
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM friend action rejected")
	}
	return nil
}

func (a OpenIMActions) read(ctx context.Context, config *ccontext.GlobalConfig, path string, input, output any) error {
	status, payload, err := a.post(ctx, config, path, input)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return errors.New("OpenIM friend state read failed")
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return errors.New("decode OpenIM friend state response")
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM friend state read rejected")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return errors.New("decode OpenIM friend state data")
	}
	return nil
}

func (a OpenIMActions) post(ctx context.Context, config *ccontext.GlobalConfig, path string, input any) (int, []byte, error) {
	endpoint, err := friendEndpoint(config.ApiAddr, path)
	if err != nil {
		return 0, nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal OpenIM friend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("create OpenIM friend request: %w", err)
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return 0, nil, errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)

	client := a.HTTPClient
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

func friendEndpoint(apiAddr, path string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + path, nil
}

func unknownFriendOutcome() error {
	return fmt.Errorf("OpenIM friend action did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ Source = (*OpenIMActions)(nil)
