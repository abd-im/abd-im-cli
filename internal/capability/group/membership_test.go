package group

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestGroupMembershipActions(t *testing.T) {
	source := &fakeMembershipSource{canLeave: true, canInvite: true, canRemove: true}
	tool, credential := newMembershipTool(t, source, "run-available")
	if response := callMembership(t, tool, credential, JoinMethod, "join", JoinInput{GroupID: "group-1", Message: "please add me"}); !response.OK || source.joinCalls != 1 || source.join != (JoinInput{GroupID: "group-1", Message: "please add me"}) {
		t.Fatalf("group.join = %+v, source=%+v", response, source)
	}
	if response := callMembership(t, tool, credential, LeaveMethod, "leave", LeaveInput{GroupID: "group-1"}); !response.OK || source.leaveChecks != 1 || source.leaveCalls != 1 {
		t.Fatalf("group.leave = %+v, source=%+v", response, source)
	}
	invite := MembersInput{GroupID: "group-1", UserIDs: []string{"user-1", "user-2"}, Reason: "project"}
	if response := callMembership(t, tool, credential, InviteMembersMethod, "invite", invite); !response.OK || source.inviteChecks != 1 || source.inviteCalls != 1 || !sameMembersInput(source.invite, invite) {
		t.Fatalf("group.invite_members = %+v, source=%+v", response, source)
	}
	remove := MembersInput{GroupID: "group-1", UserIDs: []string{"user-1"}, Reason: "done"}
	if response := callMembership(t, tool, credential, RemoveMembersMethod, "remove", remove); !response.OK || source.removeChecks != 1 || source.removeCalls != 1 || !sameMembersInput(source.remove, remove) {
		t.Fatalf("group.remove_members = %+v, source=%+v", response, source)
	}
}

func TestGroupMembershipFailsClosedForUnverifiedState(t *testing.T) {
	source := &fakeMembershipSource{}
	tool, credential := newMembershipTool(t, source, "run-state")
	for _, test := range []struct {
		method string
		input  any
	}{
		{LeaveMethod, LeaveInput{GroupID: "group-1"}},
		{InviteMembersMethod, MembersInput{GroupID: "group-1", UserIDs: []string{"user-1"}}},
		{RemoveMembersMethod, MembersInput{GroupID: "group-1", UserIDs: []string{"user-1"}}},
	} {
		response := callMembership(t, tool, credential, test.method, "state-"+test.method, test.input)
		if response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
			t.Fatalf("%s unverified state = %+v", test.method, response)
		}
	}
	if source.leaveCalls != 0 || source.inviteCalls != 0 || source.removeCalls != 0 {
		t.Fatalf("membership action ran without verified state: %+v", source)
	}

	source.inviteErr = errors.New("state read failed")
	if response := callMembership(t, tool, credential, InviteMembersMethod, "state-error", MembersInput{GroupID: "group-1", UserIDs: []string{"user-1"}}); response.Error == nil || response.Error.Code != contracts.CodeSDKError || source.inviteCalls != 0 {
		t.Fatalf("group invite state read failure = %+v, source=%+v", response, source)
	}
}

func TestGroupMembershipValidatesInputAndPreservesUnknownOutcome(t *testing.T) {
	source := &fakeMembershipSource{canLeave: true, canInvite: true, canRemove: true}
	tool, credential := newMembershipTool(t, source, "run-unknown")
	for _, test := range []struct {
		method string
		input  any
	}{
		{JoinMethod, JoinInput{}},
		{LeaveMethod, LeaveInput{}},
		{InviteMembersMethod, MembersInput{GroupID: "group-1", UserIDs: []string{"user-1", "user-1"}}},
		{RemoveMembersMethod, MembersInput{GroupID: "group-1", UserIDs: []string{strings.Repeat("u", maxMembershipUserIDBytes+1)}}},
	} {
		if response := callMembership(t, tool, credential, test.method, "invalid-"+test.method, test.input); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
			t.Fatalf("invalid %s = %+v", test.method, response)
		}
	}
	source.inviteActionErr = operation.ErrOutcomeUnknown
	input := MembersInput{GroupID: "group-1", UserIDs: []string{"user-1"}}
	if response := callMembership(t, tool, credential, InviteMembersMethod, "unknown", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.inviteCalls != 1 {
		t.Fatalf("unknown group invite = %+v, source=%+v", response, source)
	}
	if response := callMembership(t, tool, credential, InviteMembersMethod, "unknown-new-key", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.inviteCalls != 1 {
		t.Fatalf("unknown group invite rebuild = %+v, source=%+v", response, source)
	}
}

func TestNewMembershipRequiresAllDependencies(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMembership(nil, &fakeMembershipSource{}); err == nil {
		t.Fatal("NewMembership(nil guard) error = nil")
	}
	if _, err := NewMembership(guard, nil); err == nil {
		t.Fatal("NewMembership(nil source) error = nil")
	}
}

func newMembershipTool(t *testing.T, source MembershipSource, runID string) (*proxy.Proxy, string) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewMembership(guard, source)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{JoinMethod, LeaveMethod, InviteMembersMethod, RemoveMembersMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, runID, "work", handler.ProxyMethods())
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func callMembership(t *testing.T, tool *proxy.Proxy, credential, method, key string, input any) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      key,
		ProfileID:      "work",
		Method:         method,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func sameMembersInput(left, right MembersInput) bool {
	if left.GroupID != right.GroupID || left.Reason != right.Reason || len(left.UserIDs) != len(right.UserIDs) {
		return false
	}
	for index := range left.UserIDs {
		if left.UserIDs[index] != right.UserIDs[index] {
			return false
		}
	}
	return true
}

type fakeMembershipSource struct {
	canLeave  bool
	canInvite bool
	canRemove bool

	leaveErr  error
	inviteErr error
	removeErr error

	joinActionErr   error
	leaveActionErr  error
	inviteActionErr error
	removeActionErr error

	joinCalls    int
	leaveCalls   int
	inviteCalls  int
	removeCalls  int
	leaveChecks  int
	inviteChecks int
	removeChecks int
	join         JoinInput
	leave        LeaveInput
	invite       MembersInput
	remove       MembersInput
}

func (s *fakeMembershipSource) JoinGroup(_ context.Context, input JoinInput) error {
	s.joinCalls++
	s.join = input
	return s.joinActionErr
}

func (s *fakeMembershipSource) LeaveGroup(_ context.Context, input LeaveInput) error {
	s.leaveCalls++
	s.leave = input
	return s.leaveActionErr
}

func (s *fakeMembershipSource) InviteMembers(_ context.Context, input MembersInput) error {
	s.inviteCalls++
	s.invite = input
	return s.inviteActionErr
}

func (s *fakeMembershipSource) RemoveMembers(_ context.Context, input MembersInput) error {
	s.removeCalls++
	s.remove = input
	return s.removeActionErr
}

func (s *fakeMembershipSource) CanLeaveGroup(context.Context, string) (bool, error) {
	s.leaveChecks++
	return s.canLeave, s.leaveErr
}

func (s *fakeMembershipSource) CanInviteMembers(context.Context, string, []string) (bool, error) {
	s.inviteChecks++
	return s.canInvite, s.inviteErr
}

func (s *fakeMembershipSource) CanRemoveMembers(context.Context, string, []string) (bool, error) {
	s.removeChecks++
	return s.canRemove, s.removeErr
}

var _ MembershipSource = (*fakeMembershipSource)(nil)
