package group

import (
	"context"
	"errors"
	"net/http"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/wrapperspb"
)

// GroupInfoUpdate is the bounded group profile surface that can be changed by
// the administration capability. Nil fields are left unchanged.
type GroupInfoUpdate struct {
	GroupID      string
	Name         *string
	Notification *string
	Introduction *string
	FaceURL      *string
}

// GroupMuteUpdate changes the all-member mute state for one group.
type GroupMuteUpdate struct {
	GroupID string
	Muted   bool
}

// GroupMemberMuteUpdate changes one member's mute state. DurationSeconds is
// only sent when Muted is true.
type GroupMemberMuteUpdate struct {
	GroupID         string
	UserID          string
	Muted           bool
	DurationSeconds uint32
}

// GroupOwnerTransfer moves ownership from the authenticated owner to one
// existing group member.
type GroupOwnerTransfer struct {
	GroupID        string
	NewOwnerUserID string
}

// OpenIMAdministrationSource invokes only fixed group-administration server
// actions through the daemon-owned SDK context. It deliberately avoids SDK
// Group APIs because their successful mutation paths synchronize local state.
type OpenIMAdministrationSource struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func NewOpenIMAdministrationSource(source OpenIMAdministrationSource) (*OpenIMAdministrationSource, error) {
	if source.Context == nil {
		return nil, errors.New("OpenIM SDK context is required")
	}
	return &source, nil
}

func (s OpenIMAdministrationSource) SetInfo(ctx context.Context, input GroupInfoUpdate) error {
	return s.action(ctx, "/group/set_group_info_ex", &pbgroup.SetGroupInfoExReq{
		GroupID:      input.GroupID,
		GroupName:    optionalString(input.Name),
		Notification: optionalString(input.Notification),
		Introduction: optionalString(input.Introduction),
		FaceURL:      optionalString(input.FaceURL),
	})
}

func (s OpenIMAdministrationSource) SetMute(ctx context.Context, input GroupMuteUpdate) error {
	if input.Muted {
		return s.action(ctx, "/group/mute_group", &pbgroup.MuteGroupReq{GroupID: input.GroupID})
	}
	return s.action(ctx, "/group/cancel_mute_group", &pbgroup.CancelMuteGroupReq{GroupID: input.GroupID})
}

func (s OpenIMAdministrationSource) SetMemberMute(ctx context.Context, input GroupMemberMuteUpdate) error {
	if input.Muted {
		return s.action(ctx, "/group/mute_group_member", &pbgroup.MuteGroupMemberReq{
			GroupID:      input.GroupID,
			UserID:       input.UserID,
			MutedSeconds: input.DurationSeconds,
		})
	}
	return s.action(ctx, "/group/cancel_mute_group_member", &pbgroup.CancelMuteGroupMemberReq{
		GroupID: input.GroupID,
		UserID:  input.UserID,
	})
}

func (s OpenIMAdministrationSource) TransferOwner(ctx context.Context, input GroupOwnerTransfer) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.membership().invokeAction(request, config, "/group/transfer_group", &pbgroup.TransferGroupOwnerReq{
		GroupID:        input.GroupID,
		OldOwnerUserID: config.UserID,
		NewOwnerUserID: input.NewOwnerUserID,
	})
}

// CanSetInfo and CanSetMute require the authenticated user to be a current
// owner or administrator. The server enforces this again at mutation time.
func (s OpenIMAdministrationSource) CanSetInfo(ctx context.Context, groupID string) (bool, error) {
	return s.canAdminister(ctx, groupID)
}

func (s OpenIMAdministrationSource) CanSetMute(ctx context.Context, groupID string) (bool, error) {
	return s.canAdminister(ctx, groupID)
}

// CanSetMemberMute matches the server role hierarchy: an owner cannot be
// muted, and an administrator cannot mute a peer administrator.
func (s OpenIMAdministrationSource) CanSetMemberMute(ctx context.Context, groupID, userID string) (bool, error) {
	config, members, err := s.membership().members(ctx, groupID, []string{userID})
	if err != nil {
		return false, err
	}
	actor, actorExists := members[config.UserID]
	member, memberExists := members[userID]
	if !actorExists || !memberExists {
		return false, nil
	}
	switch member.RoleLevel {
	case constant.GroupOwner:
		return false, nil
	case constant.GroupAdmin:
		return actor.RoleLevel == constant.GroupOwner, nil
	case constant.GroupOrdinaryUsers:
		return actor.RoleLevel == constant.GroupOwner || actor.RoleLevel == constant.GroupAdmin, nil
	default:
		return false, nil
	}
}

// CanTransferOwner restricts transfer to the authenticated current owner and
// an existing, distinct target member.
func (s OpenIMAdministrationSource) CanTransferOwner(ctx context.Context, groupID, newOwnerUserID string) (bool, error) {
	config, members, err := s.membership().members(ctx, groupID, []string{newOwnerUserID})
	if err != nil {
		return false, err
	}
	actor, actorExists := members[config.UserID]
	_, targetExists := members[newOwnerUserID]
	return actorExists && targetExists && config.UserID != newOwnerUserID && actor.RoleLevel == constant.GroupOwner, nil
}

func (s OpenIMAdministrationSource) canAdminister(ctx context.Context, groupID string) (bool, error) {
	config, members, err := s.membership().members(ctx, groupID, nil)
	if err != nil {
		return false, err
	}
	actor, ok := members[config.UserID]
	return ok && (actor.RoleLevel == constant.GroupOwner || actor.RoleLevel == constant.GroupAdmin), nil
}

func (s OpenIMAdministrationSource) action(ctx context.Context, path string, input any) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.membership().invokeAction(request, config, path, input)
}

func (s OpenIMAdministrationSource) requestContext(ctx context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	request, config, done, err := s.membership().requestContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return request, config, done, nil
}

func (s OpenIMAdministrationSource) membership() OpenIMMembershipSource {
	return OpenIMMembershipSource{Context: s.Context, HTTPClient: s.HTTPClient}
}

func optionalString(value *string) *wrapperspb.StringValue {
	if value == nil {
		return nil
	}
	return wrapperspb.String(*value)
}

var _ interface {
	SetInfo(context.Context, GroupInfoUpdate) error
	SetMute(context.Context, GroupMuteUpdate) error
	SetMemberMute(context.Context, GroupMemberMuteUpdate) error
	TransferOwner(context.Context, GroupOwnerTransfer) error
} = (*OpenIMAdministrationSource)(nil)
