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
	JoinMethod          = "group.join"
	LeaveMethod         = "group.leave"
	InviteMembersMethod = "group.invite_members"
	RemoveMembersMethod = "group.remove_members"

	JoinScope          = JoinMethod
	LeaveScope         = LeaveMethod
	InviteMembersScope = InviteMembersMethod
	RemoveMembersScope = RemoveMembersMethod

	maxGroupIDBytes          = 256
	maxMembershipUserIDBytes = 256
	maxMembershipMessageByte = 512
	maxMembershipMembers     = 50
)

var (
	errCannotLeaveGroup    = errors.New("group membership does not permit leave")
	errCannotInviteMembers = errors.New("group membership does not permit invitation")
	errCannotRemoveMembers = errors.New("group membership does not permit removal")
)

// JoinInput applies the authenticated profile to one group. The profile user
// is derived by the daemon-owned source and cannot be supplied by a provider.
type JoinInput struct {
	GroupID string `json:"group_id"`
	Message string `json:"message,omitempty"`
}

// LeaveInput applies the authenticated profile to one current group.
type LeaveInput struct {
	GroupID string `json:"group_id"`
}

// MembersInput addresses a bounded list of group members. Reason is only
// accepted for invite/remove action notifications.
type MembersInput struct {
	GroupID string   `json:"group_id"`
	UserIDs []string `json:"user_ids"`
	Reason  string   `json:"reason,omitempty"`
}

// MembershipSource is the narrow server state/action surface required for
// group membership mutations. Implementations must not call SDK Group APIs
// that synchronize local SDK state.
type MembershipSource interface {
	JoinGroup(context.Context, JoinInput) error
	LeaveGroup(context.Context, LeaveInput) error
	InviteMembers(context.Context, MembersInput) error
	RemoveMembers(context.Context, MembersInput) error
	CanLeaveGroup(context.Context, string) (bool, error)
	CanInviteMembers(context.Context, string, []string) (bool, error)
	CanRemoveMembers(context.Context, string, []string) (bool, error)
}

// MembershipHandler exposes the fixed group membership action surface to one
// run-scoped proxy.
type MembershipHandler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	source   MembershipSource
}

func NewMembership(manifest *capability.Manifest, guard *operation.Guard, source MembershipSource) (*MembershipHandler, error) {
	if manifest == nil || guard == nil || source == nil {
		return nil, errors.New("manifest, operation guard, and group membership source are required")
	}
	return &MembershipHandler{manifest: manifest, guard: guard, source: source}, nil
}

func (h *MembershipHandler) ProxyMethods() []proxy.Method {
	return []proxy.Method{
		{Name: JoinMethod, Scope: JoinScope, Allowed: func() bool { return h.manifest.Allows(JoinMethod, JoinScope) }, Targets: joinTargets, Handle: h.join},
		{Name: LeaveMethod, Scope: LeaveScope, Allowed: func() bool { return h.manifest.Allows(LeaveMethod, LeaveScope) }, Targets: leaveTargets, Handle: h.leave},
		{Name: InviteMembersMethod, Scope: InviteMembersScope, Allowed: func() bool { return h.manifest.Allows(InviteMembersMethod, InviteMembersScope) }, Targets: inviteTargets, Handle: h.invite},
		{Name: RemoveMembersMethod, Scope: RemoveMembersScope, Allowed: func() bool { return h.manifest.Allows(RemoveMembersMethod, RemoveMembersScope) }, Targets: removeTargets, Handle: h.remove},
	}
}

func joinTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseJoin(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func leaveTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseLeave(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func inviteTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseMembers(raw)
	if err != nil {
		return nil, err
	}
	return membersTargets(input), nil
}

func removeTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseMembers(raw)
	if err != nil {
		return nil, err
	}
	return membersTargets(input), nil
}

func membersTargets(input MembersInput) []string {
	targets := make([]string, 0, len(input.UserIDs)+1)
	targets = append(targets, grant.GroupTarget(input.GroupID))
	for _, userID := range input.UserIDs {
		targets = append(targets, grant.UserTarget(userID))
	}
	return targets
}

func (h *MembershipHandler) join(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseJoin(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.join input")
	}
	return h.execute(ctx, request, JoinMethod, JoinScope, input, func(ctx context.Context) error {
		return h.source.JoinGroup(ctx, input)
	})
}

func (h *MembershipHandler) leave(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseLeave(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.leave input")
	}
	return h.execute(ctx, request, LeaveMethod, LeaveScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanLeaveGroup(ctx, input.GroupID)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotLeaveGroup
		}
		return h.source.LeaveGroup(ctx, input)
	})
}

func (h *MembershipHandler) invite(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseMembers(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.invite_members input")
	}
	return h.execute(ctx, request, InviteMembersMethod, InviteMembersScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanInviteMembers(ctx, input.GroupID, input.UserIDs)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotInviteMembers
		}
		return h.source.InviteMembers(ctx, input)
	})
}

func (h *MembershipHandler) remove(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseMembers(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.remove_members input")
	}
	return h.execute(ctx, request, RemoveMembersMethod, RemoveMembersScope, input, func(ctx context.Context) error {
		allowed, err := h.source.CanRemoveMembers(ctx, input.GroupID, input.UserIDs)
		if err != nil {
			return err
		}
		if !allowed {
			return errCannotRemoveMembers
		}
		return h.source.RemoveMembers(ctx, input)
	})
}

func (h *MembershipHandler) execute(ctx context.Context, request contracts.Request, method, scope string, input any, effect operation.Effect) (json.RawMessage, error) {
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
		case errors.Is(err, errCannotLeaveGroup), errors.Is(err, errCannotInviteMembers), errors.Is(err, errCannotRemoveMembers):
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

func parseJoin(raw json.RawMessage) (JoinInput, error) {
	var input JoinInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return JoinInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.Message = strings.TrimSpace(input.Message)
	if !validGroupID(input.GroupID) || len(input.Message) > maxMembershipMessageByte {
		return JoinInput{}, errors.New("group ID or join message is invalid")
	}
	return input, nil
}

func parseLeave(raw json.RawMessage) (LeaveInput, error) {
	var input LeaveInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return LeaveInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	if !validGroupID(input.GroupID) {
		return LeaveInput{}, errors.New("group ID is invalid")
	}
	return input, nil
}

func parseMembers(raw json.RawMessage) (MembersInput, error) {
	var input MembersInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return MembersInput{}, err
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validGroupID(input.GroupID) || len(input.Reason) > maxMembershipMessageByte || len(input.UserIDs) == 0 || len(input.UserIDs) > maxMembershipMembers {
		return MembersInput{}, errors.New("group ID, member IDs, or reason is invalid")
	}
	seen := make(map[string]struct{}, len(input.UserIDs))
	for index, userID := range input.UserIDs {
		userID = strings.TrimSpace(userID)
		if !validMembershipUserID(userID) {
			return MembersInput{}, errors.New("group member user ID is invalid")
		}
		if _, exists := seen[userID]; exists {
			return MembersInput{}, errors.New("duplicate group member user ID")
		}
		seen[userID] = struct{}{}
		input.UserIDs[index] = userID
	}
	return input, nil
}

func validGroupID(groupID string) bool {
	return groupID != "" && len(groupID) <= maxGroupIDBytes
}

func validMembershipUserID(userID string) bool {
	return userID != "" && len(userID) <= maxMembershipUserIDBytes
}
