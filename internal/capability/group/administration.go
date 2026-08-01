package group

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	SetInfoMethod       = "group.set_info"
	SetMuteMethod       = "group.set_mute"
	SetMemberMuteMethod = "group.set_member_mute"
	TransferOwnerMethod = "group.transfer_owner"

	SetInfoScope       = SetInfoMethod
	SetMuteScope       = SetMuteMethod
	SetMemberMuteScope = SetMemberMuteMethod
	TransferOwnerScope = TransferOwnerMethod

	maxGroupInfoNameBytes        = 256
	maxGroupNotificationBytes    = 1024
	maxGroupIntroductionBytes    = 1024
	maxGroupFaceURLBytes         = 2048
	maxMemberMuteDurationSeconds = 7 * 24 * 60 * 60
)

var (
	errCannotSetGroupInfo  = errors.New("group membership does not permit group info update")
	errCannotSetGroupMute  = errors.New("group membership does not permit group mute update")
	errCannotSetMemberMute = errors.New("group membership does not permit member mute update")
	errCannotTransferOwner = errors.New("group membership does not permit owner transfer")
)

// SetInfoInput exposes only the bounded group profile fields supported by the
// administration capability. A nil field is left unchanged.
type SetInfoInput struct {
	GroupID      string  `json:"group_id"`
	Name         *string `json:"name,omitempty"`
	Notification *string `json:"notification,omitempty"`
	Introduction *string `json:"introduction,omitempty"`
	FaceURL      *string `json:"face_url,omitempty"`
}

// SetMuteInput explicitly sets the all-member mute state for one group.
type SetMuteInput struct {
	GroupID string `json:"group_id"`
	Muted   *bool  `json:"muted"`
}

// SetMemberMuteInput explicitly sets one member's mute state. A mute action
// needs a bounded duration; unmuting does not accept a duration.
type SetMemberMuteInput struct {
	GroupID         string  `json:"group_id"`
	UserID          string  `json:"user_id"`
	Muted           *bool   `json:"muted"`
	DurationSeconds *uint32 `json:"duration_seconds,omitempty"`
}

// TransferOwnerInput moves ownership to one approved, existing member.
type TransferOwnerInput struct {
	GroupID        string `json:"group_id"`
	NewOwnerUserID string `json:"new_owner_user_id"`
}

// AdministrationSource is the narrow fixed server surface for group
// administration. It must validate current group role and member state before
// performing any remote mutation.
type AdministrationSource interface {
	SetInfo(context.Context, GroupInfoUpdate) error
	SetMute(context.Context, GroupMuteUpdate) error
	SetMemberMute(context.Context, GroupMemberMuteUpdate) error
	TransferOwner(context.Context, GroupOwnerTransfer) error
	CanSetInfo(context.Context, string) (bool, error)
	CanSetMute(context.Context, string) (bool, error)
	CanSetMemberMute(context.Context, string, string) (bool, error)
	CanTransferOwner(context.Context, string, string) (bool, error)
}

// AdministrationHandler exposes fixed group administration methods to a
// run-scoped proxy.
type AdministrationHandler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	source   AdministrationSource
}

func NewAdministration(manifest *capability.Manifest, guard *operation.Guard, source AdministrationSource) (*AdministrationHandler, error) {
	if manifest == nil || guard == nil || source == nil {
		return nil, errors.New("manifest, operation guard, and group administration source are required")
	}
	return &AdministrationHandler{manifest: manifest, guard: guard, source: source}, nil
}

func (h *AdministrationHandler) ProxyMethods() []proxy.Method {
	return []proxy.Method{
		{Name: SetInfoMethod, Scope: SetInfoScope, Allowed: func() bool { return h.manifest.Allows(SetInfoMethod, SetInfoScope) }, Targets: setInfoTargets, Handle: h.setInfo},
		{Name: SetMuteMethod, Scope: SetMuteScope, Allowed: func() bool { return h.manifest.Allows(SetMuteMethod, SetMuteScope) }, Targets: setMuteTargets, Handle: h.setMute},
		{Name: SetMemberMuteMethod, Scope: SetMemberMuteScope, Allowed: func() bool { return h.manifest.Allows(SetMemberMuteMethod, SetMemberMuteScope) }, Targets: setMemberMuteTargets, Handle: h.setMemberMute},
		{Name: TransferOwnerMethod, Scope: TransferOwnerScope, Allowed: func() bool { return h.manifest.Allows(TransferOwnerMethod, TransferOwnerScope) }, Targets: transferOwnerTargets, Handle: h.transferOwner},
	}
}

func setInfoTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseSetInfo(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func setMuteTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseSetMute(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func setMemberMuteTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseSetMemberMute(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID), grant.UserTarget(input.UserID)}, nil
}

func transferOwnerTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseTransferOwner(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID), grant.UserTarget(input.NewOwnerUserID)}, nil
}

func (h *AdministrationHandler) setInfo(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetInfo(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.set_info input")
	}
	return h.execute(ctx, request, SetInfoMethod, SetInfoScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanSetInfo(ctx, input.GroupID)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotSetGroupInfo
		}
		return h.source.SetInfo(ctx, GroupInfoUpdate{GroupID: input.GroupID, Name: input.Name, Notification: input.Notification, Introduction: input.Introduction, FaceURL: input.FaceURL})
	})
}

func (h *AdministrationHandler) setMute(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetMute(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.set_mute input")
	}
	return h.execute(ctx, request, SetMuteMethod, SetMuteScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanSetMute(ctx, input.GroupID)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotSetGroupMute
		}
		return h.source.SetMute(ctx, GroupMuteUpdate{GroupID: input.GroupID, Muted: *input.Muted})
	})
}

func (h *AdministrationHandler) setMemberMute(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetMemberMute(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.set_member_mute input")
	}
	return h.execute(ctx, request, SetMemberMuteMethod, SetMemberMuteScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanSetMemberMute(ctx, input.GroupID, input.UserID)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotSetMemberMute
		}
		update := GroupMemberMuteUpdate{GroupID: input.GroupID, UserID: input.UserID, Muted: *input.Muted}
		if input.DurationSeconds != nil {
			update.DurationSeconds = *input.DurationSeconds
		}
		return h.source.SetMemberMute(ctx, update)
	})
}

func (h *AdministrationHandler) transferOwner(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseTransferOwner(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.transfer_owner input")
	}
	return h.execute(ctx, request, TransferOwnerMethod, TransferOwnerScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanTransferOwner(ctx, input.GroupID, input.NewOwnerUserID)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotTransferOwner
		}
		return h.source.TransferOwner(ctx, GroupOwnerTransfer{GroupID: input.GroupID, NewOwnerUserID: input.NewOwnerUserID})
	})
}

func (h *AdministrationHandler) execute(ctx context.Context, request contracts.Request, method, scope string, input any, effect operation.Effect) (json.RawMessage, error) {
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, method+" requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             strings.ReplaceAll(method, ".", "-") + "-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, effect)
	if err != nil {
		switch {
		case errors.Is(err, errCannotSetGroupInfo), errors.Is(err, errCannotSetGroupMute), errors.Is(err, errCannotSetMemberMute), errors.Is(err, errCannotTransferOwner):
			return nil, proxy.Failure(contracts.CodePolicyDenied, "group membership state does not authorize action")
		case errors.Is(err, operation.ErrIdempotencyConflict):
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		case errors.Is(err, operation.ErrOutcomeUnknown):
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior "+method+" outcome is unknown")
		default:
			return nil, proxy.Failure(contracts.CodeSDKError, method+" failed")
		}
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func parseSetInfo(raw json.RawMessage) (SetInfoInput, error) {
	var input SetInfoInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return SetInfoInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.Name = trimGroupInfo(input.Name)
	input.Notification = trimGroupInfo(input.Notification)
	input.Introduction = trimGroupInfo(input.Introduction)
	input.FaceURL = trimGroupInfo(input.FaceURL)
	if !validGroupID(input.GroupID) || (input.Name == nil && input.Notification == nil && input.Introduction == nil && input.FaceURL == nil) || (input.Name != nil && (*input.Name == "" || len(*input.Name) > maxGroupInfoNameBytes)) || (input.Notification != nil && len(*input.Notification) > maxGroupNotificationBytes) || (input.Introduction != nil && len(*input.Introduction) > maxGroupIntroductionBytes) || (input.FaceURL != nil && len(*input.FaceURL) > maxGroupFaceURLBytes) {
		return SetInfoInput{}, errors.New("group info update is invalid")
	}
	return input, nil
}

func parseSetMute(raw json.RawMessage) (SetMuteInput, error) {
	var input SetMuteInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return SetMuteInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	if !validGroupID(input.GroupID) || input.Muted == nil {
		return SetMuteInput{}, errors.New("group mute update is invalid")
	}
	return input, nil
}

func parseSetMemberMute(raw json.RawMessage) (SetMemberMuteInput, error) {
	var input SetMemberMuteInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return SetMemberMuteInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.UserID = strings.TrimSpace(input.UserID)
	if !validGroupID(input.GroupID) || !validMembershipUserID(input.UserID) || input.Muted == nil || (*input.Muted && (input.DurationSeconds == nil || *input.DurationSeconds == 0 || *input.DurationSeconds > maxMemberMuteDurationSeconds)) || (!*input.Muted && input.DurationSeconds != nil) {
		return SetMemberMuteInput{}, errors.New("group member mute update is invalid")
	}
	return input, nil
}

func parseTransferOwner(raw json.RawMessage) (TransferOwnerInput, error) {
	var input TransferOwnerInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return TransferOwnerInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.NewOwnerUserID = strings.TrimSpace(input.NewOwnerUserID)
	if !validGroupID(input.GroupID) || !validMembershipUserID(input.NewOwnerUserID) {
		return TransferOwnerInput{}, errors.New("group owner transfer is invalid")
	}
	return input, nil
}

func trimGroupInfo(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}
