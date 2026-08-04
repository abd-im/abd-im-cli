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

func TestGroupAdministrationActions(t *testing.T) {
	source := &fakeAdministrationSource{canSetInfo: true, canSetMute: true, canSetMemberMute: true, canTransferOwner: true}
	tool, credential := newAdministrationTool(t, source, "run-available")
	if response := callAdministration(t, tool, credential, SetInfoMethod, "info", validSetInfoInput()); !response.OK || source.setInfoCalls != 1 || source.setInfo.GroupID != "group-1" || source.setInfo.Name == nil || *source.setInfo.Name != "project" {
		t.Fatalf("group.set_info = %+v, source=%+v", response, source)
	}
	if response := callAdministration(t, tool, credential, SetMuteMethod, "mute", SetMuteInput{GroupID: "group-1", Muted: boolPointer(true)}); !response.OK || source.setMuteCalls != 1 || !source.setMute.Muted {
		t.Fatalf("group.set_mute = %+v, source=%+v", response, source)
	}
	if response := callAdministration(t, tool, credential, SetMemberMuteMethod, "member-mute", SetMemberMuteInput{GroupID: "group-1", UserID: "user-1", Muted: boolPointer(true), DurationSeconds: uint32Pointer(60)}); !response.OK || source.setMemberMuteCalls != 1 || source.setMemberMute.DurationSeconds != 60 {
		t.Fatalf("group.set_member_mute = %+v, source=%+v", response, source)
	}
	if response := callAdministration(t, tool, credential, TransferOwnerMethod, "transfer", TransferOwnerInput{GroupID: "group-1", NewOwnerUserID: "user-1"}); !response.OK || source.transferOwnerCalls != 1 || source.transferOwner.NewOwnerUserID != "user-1" {
		t.Fatalf("group.transfer_owner = %+v, source=%+v", response, source)
	}
}

func TestGroupAdministrationFailsClosedForUnverifiedState(t *testing.T) {
	source := &fakeAdministrationSource{}
	tool, credential := newAdministrationTool(t, source, "run-state")
	for _, test := range []struct {
		method string
		input  any
	}{
		{SetInfoMethod, validSetInfoInput()},
		{SetMuteMethod, SetMuteInput{GroupID: "group-1", Muted: boolPointer(true)}},
		{SetMemberMuteMethod, SetMemberMuteInput{GroupID: "group-1", UserID: "user-1", Muted: boolPointer(true), DurationSeconds: uint32Pointer(60)}},
		{TransferOwnerMethod, TransferOwnerInput{GroupID: "group-1", NewOwnerUserID: "user-1"}},
	} {
		if response := callAdministration(t, tool, credential, test.method, "denied-"+test.method, test.input); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
			t.Fatalf("%s state denial = %+v", test.method, response)
		}
	}
	if source.setInfoCalls != 0 || source.setMuteCalls != 0 || source.setMemberMuteCalls != 0 || source.transferOwnerCalls != 0 {
		t.Fatalf("state denials invoked actions: %+v", source)
	}

	source.canSetInfo = true
	source.setInfoCheckErr = errors.New("state source unavailable")
	if response := callAdministration(t, tool, credential, SetInfoMethod, "state-error", validSetInfoInput()); response.Error == nil || response.Error.Code != contracts.CodeSDKError || source.setInfoCalls != 0 {
		t.Fatalf("group.set_info state read failure = %+v, source=%+v", response, source)
	}
}

func TestGroupAdministrationValidatesInputAndPreservesUnknownOutcome(t *testing.T) {
	source := &fakeAdministrationSource{canSetInfo: true, canSetMute: true, canSetMemberMute: true, canTransferOwner: true}
	tool, credential := newAdministrationTool(t, source, "run-validation")
	for _, test := range []struct {
		method string
		input  any
	}{
		{SetInfoMethod, SetInfoInput{GroupID: "group-1"}},
		{SetInfoMethod, SetInfoInput{GroupID: "group-1", Name: stringPointer("")}},
		{SetMuteMethod, SetMuteInput{GroupID: "group-1"}},
		{SetMemberMuteMethod, SetMemberMuteInput{GroupID: "group-1", UserID: "user-1", Muted: boolPointer(true)}},
		{SetMemberMuteMethod, SetMemberMuteInput{GroupID: "group-1", UserID: "user-1", Muted: boolPointer(false), DurationSeconds: uint32Pointer(1)}},
		{SetMemberMuteMethod, SetMemberMuteInput{GroupID: "group-1", UserID: "user-1", Muted: boolPointer(true), DurationSeconds: uint32Pointer(maxMemberMuteDurationSeconds + 1)}},
		{TransferOwnerMethod, TransferOwnerInput{GroupID: "group-1"}},
	} {
		if response := callAdministration(t, tool, credential, test.method, "invalid-"+test.method, test.input); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
			t.Fatalf("invalid %s = %+v", test.method, response)
		}
	}

	source.setInfoActionErr = operation.ErrOutcomeUnknown
	input := validSetInfoInput()
	if response := callAdministration(t, tool, credential, SetInfoMethod, "unknown", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.setInfoCalls != 1 {
		t.Fatalf("unknown group.set_info = %+v, source=%+v", response, source)
	}
	if response := callAdministration(t, tool, credential, SetInfoMethod, "unknown-new-key", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.setInfoCalls != 1 {
		t.Fatalf("new-key group.set_info rebuild = %+v, source=%+v", response, source)
	}
}

func TestGroupAdministrationScopesOperationIDsByMethod(t *testing.T) {
	source := &fakeAdministrationSource{canSetInfo: true, canSetMute: true, canSetMemberMute: true, canTransferOwner: true}
	tool, credential := newAdministrationTool(t, source, "run-ids")
	if response := callAdministration(t, tool, credential, SetInfoMethod, "same-key", validSetInfoInput()); !response.OK {
		t.Fatalf("group.set_info = %+v", response)
	}
	if response := callAdministration(t, tool, credential, SetMuteMethod, "same-key", SetMuteInput{GroupID: "group-1", Muted: boolPointer(true)}); !response.OK || source.setMuteCalls != 1 {
		t.Fatalf("group.set_mute sharing key = %+v, source=%+v", response, source)
	}
}

func TestNewAdministrationRequiresAllDependencies(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewAdministration(nil, &fakeAdministrationSource{}); err == nil {
		t.Fatal("NewAdministration(nil guard) error = nil")
	}
	if _, err := NewAdministration(guard, nil); err == nil {
		t.Fatal("NewAdministration(nil source) error = nil")
	}
}

func newAdministrationTool(t *testing.T, source AdministrationSource, runID string) (*proxy.Proxy, string) {
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
	handler, err := NewAdministration(guard, source)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{SetInfoMethod, SetMuteMethod, SetMemberMuteMethod, TransferOwnerMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 20,
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

func callAdministration(t *testing.T, tool *proxy.Proxy, credential, method, key string, input any) contracts.Response {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      key,
		ProfileID:      "work",
		Method:         method,
		Params:         raw,
		Grant:          credential,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func validSetInfoInput() SetInfoInput {
	return SetInfoInput{GroupID: "group-1", Name: stringPointer("project"), Notification: stringPointer("planning"), Introduction: stringPointer("focus"), FaceURL: stringPointer("https://example.invalid/group.png")}
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }
func uint32Pointer(value uint32) *uint32 { return &value }

type fakeAdministrationSource struct {
	canSetInfo       bool
	canSetMute       bool
	canSetMemberMute bool
	canTransferOwner bool

	setInfoCheckErr       error
	setMuteCheckErr       error
	setMemberMuteCheckErr error
	transferOwnerCheckErr error

	setInfoActionErr       error
	setMuteActionErr       error
	setMemberMuteActionErr error
	transferOwnerActionErr error

	setInfoCalls       int
	setMuteCalls       int
	setMemberMuteCalls int
	transferOwnerCalls int

	setInfo       GroupInfoUpdate
	setMute       GroupMuteUpdate
	setMemberMute GroupMemberMuteUpdate
	transferOwner GroupOwnerTransfer
}

func (s *fakeAdministrationSource) SetInfo(_ context.Context, input GroupInfoUpdate) error {
	s.setInfoCalls++
	s.setInfo = input
	return s.setInfoActionErr
}

func (s *fakeAdministrationSource) SetMute(_ context.Context, input GroupMuteUpdate) error {
	s.setMuteCalls++
	s.setMute = input
	return s.setMuteActionErr
}

func (s *fakeAdministrationSource) SetMemberMute(_ context.Context, input GroupMemberMuteUpdate) error {
	s.setMemberMuteCalls++
	s.setMemberMute = input
	return s.setMemberMuteActionErr
}

func (s *fakeAdministrationSource) TransferOwner(_ context.Context, input GroupOwnerTransfer) error {
	s.transferOwnerCalls++
	s.transferOwner = input
	return s.transferOwnerActionErr
}

func (s *fakeAdministrationSource) CanSetInfo(context.Context, string) (bool, error) {
	return s.canSetInfo, s.setInfoCheckErr
}

func (s *fakeAdministrationSource) CanSetMute(context.Context, string) (bool, error) {
	return s.canSetMute, s.setMuteCheckErr
}

func (s *fakeAdministrationSource) CanSetMemberMute(context.Context, string, string) (bool, error) {
	return s.canSetMemberMute, s.setMemberMuteCheckErr
}

func (s *fakeAdministrationSource) CanTransferOwner(context.Context, string, string) (bool, error) {
	return s.canTransferOwner, s.transferOwnerCheckErr
}

var _ AdministrationSource = (*fakeAdministrationSource)(nil)

func TestParseSetInfoTrimsBoundedFields(t *testing.T) {
	input, err := parseSetInfo(json.RawMessage(`{"group_id":" group-1 ","name":" project "}`))
	if err != nil || input.GroupID != "group-1" || input.Name == nil || *input.Name != "project" {
		t.Fatalf("parseSetInfo() = %+v, %v", input, err)
	}
	if _, err := parseSetInfo(json.RawMessage(`{"group_id":"group-1","notification":"` + strings.Repeat("x", maxGroupNotificationBytes+1) + `"}`)); err == nil {
		t.Fatal("parseSetInfo() accepted oversized notification")
	}
}
