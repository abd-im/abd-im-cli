package group

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMAdministrationSourceUsesFixedEndpointsAndBoundedRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("invalid authenticated request %s %s", request.Method, request.URL.Path)
		}
		switch request.URL.Path {
		case "/group/get_group_members_info":
			var input pbgroup.GetGroupMembersInfoReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || len(input.UserIDs) == 0 || input.UserIDs[0] != "owner-1" {
				t.Fatalf("member state request group=%q users=%v", input.GroupID, input.UserIDs)
			}
			members := []*sdkws.GroupMemberFullInfo{{GroupID: "group-1", UserID: "owner-1", RoleLevel: constant.GroupOwner}}
			if len(input.UserIDs) == 2 && input.UserIDs[1] == "member-1" {
				members = append(members, &sdkws.GroupMemberFullInfo{GroupID: "group-1", UserID: "member-1", RoleLevel: constant.GroupOrdinaryUsers})
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": map[string]any{"members": members}})
			return
		case "/group/set_group_info_ex":
			var input pbgroup.SetGroupInfoExReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.GroupName == nil || input.GroupName.Value != "project" || input.Notification == nil || input.Notification.Value != "planning" || input.Introduction == nil || input.Introduction.Value != "focus" || input.FaceURL == nil || input.FaceURL.Value != "https://example.invalid/group.png" || input.Ex != nil || input.NeedVerification != nil || input.LookMemberInfo != nil || input.ApplyMemberFriend != nil {
				t.Fatalf("set group info group=%q name=%v notification=%v introduction=%v face_url=%v ex=%v need_verification=%v look_member_info=%v apply_member_friend=%v", input.GroupID, input.GroupName, input.Notification, input.Introduction, input.FaceURL, input.Ex, input.NeedVerification, input.LookMemberInfo, input.ApplyMemberFriend)
			}
		case "/group/mute_group":
			var input pbgroup.MuteGroupReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" {
				t.Fatalf("mute group = %q", input.GroupID)
			}
		case "/group/cancel_mute_group":
			var input pbgroup.CancelMuteGroupReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" {
				t.Fatalf("cancel group mute = %q", input.GroupID)
			}
		case "/group/mute_group_member":
			var input pbgroup.MuteGroupMemberReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.UserID != "member-1" || input.MutedSeconds != 60 {
				t.Fatalf("mute member group=%q user=%q seconds=%d", input.GroupID, input.UserID, input.MutedSeconds)
			}
		case "/group/cancel_mute_group_member":
			var input pbgroup.CancelMuteGroupMemberReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.UserID != "member-1" {
				t.Fatalf("cancel member mute group=%q user=%q", input.GroupID, input.UserID)
			}
		case "/group/transfer_group":
			var input pbgroup.TransferGroupOwnerReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.OldOwnerUserID != "owner-1" || input.NewOwnerUserID != "member-1" {
				t.Fatalf("transfer input group=%q old=%q new=%q", input.GroupID, input.OldOwnerUserID, input.NewOwnerUserID)
			}
		default:
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
	}))
	defer server.Close()

	source, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if allowed, err := source.CanSetInfo(ctx, "group-1"); err != nil || !allowed {
		t.Fatalf("CanSetInfo() = %t, %v", allowed, err)
	}
	if err := source.SetInfo(ctx, GroupInfoUpdate{GroupID: "group-1", Name: administrationStringValue("project"), Notification: administrationStringValue("planning"), Introduction: administrationStringValue("focus"), FaceURL: administrationStringValue("https://example.invalid/group.png")}); err != nil {
		t.Fatalf("SetInfo() error = %v", err)
	}
	if allowed, err := source.CanSetMute(ctx, "group-1"); err != nil || !allowed {
		t.Fatalf("CanSetMute() = %t, %v", allowed, err)
	}
	if err := source.SetMute(ctx, GroupMuteUpdate{GroupID: "group-1", Muted: true}); err != nil {
		t.Fatalf("SetMute(true) error = %v", err)
	}
	if err := source.SetMute(ctx, GroupMuteUpdate{GroupID: "group-1"}); err != nil {
		t.Fatalf("SetMute(false) error = %v", err)
	}
	if allowed, err := source.CanSetMemberMute(ctx, "group-1", "member-1"); err != nil || !allowed {
		t.Fatalf("CanSetMemberMute() = %t, %v", allowed, err)
	}
	if err := source.SetMemberMute(ctx, GroupMemberMuteUpdate{GroupID: "group-1", UserID: "member-1", Muted: true, DurationSeconds: 60}); err != nil {
		t.Fatalf("SetMemberMute(true) error = %v", err)
	}
	if err := source.SetMemberMute(ctx, GroupMemberMuteUpdate{GroupID: "group-1", UserID: "member-1"}); err != nil {
		t.Fatalf("SetMemberMute(false) error = %v", err)
	}
	if allowed, err := source.CanTransferOwner(ctx, "group-1", "member-1"); err != nil || !allowed {
		t.Fatalf("CanTransferOwner() = %t, %v", allowed, err)
	}
	if err := source.TransferOwner(ctx, GroupOwnerTransfer{GroupID: "group-1", NewOwnerUserID: "member-1"}); err != nil {
		t.Fatalf("TransferOwner() error = %v", err)
	}
}

func TestOpenIMAdministrationSourceFailsClosedForRoles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/group/get_group_members_info" {
			t.Fatalf("unexpected action request %s", request.URL.Path)
		}
		var input pbgroup.GetGroupMembersInfoReq
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		members := []*sdkws.GroupMemberFullInfo{
			{GroupID: input.GroupID, UserID: "owner-1", RoleLevel: constant.GroupOwner},
			{GroupID: input.GroupID, UserID: "admin-1", RoleLevel: constant.GroupAdmin},
			{GroupID: input.GroupID, UserID: "member-1", RoleLevel: constant.GroupOrdinaryUsers},
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": map[string]any{"members": members}})
	}))
	defer server.Close()

	admin, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: testAdministrationContext(server.URL, "admin-1"), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := admin.CanSetMemberMute(context.Background(), "group-1", "owner-1"); err != nil || allowed {
		t.Fatalf("admin CanSetMemberMute(owner) = %t, %v", allowed, err)
	}
	if allowed, err := admin.CanSetMemberMute(context.Background(), "group-1", "admin-1"); err != nil || allowed {
		t.Fatalf("admin CanSetMemberMute(admin) = %t, %v", allowed, err)
	}
	if allowed, err := admin.CanTransferOwner(context.Background(), "group-1", "member-1"); err != nil || allowed {
		t.Fatalf("admin CanTransferOwner() = %t, %v", allowed, err)
	}

	member, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: testAdministrationContext(server.URL, "member-1"), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := member.CanSetInfo(context.Background(), "group-1"); err != nil || allowed {
		t.Fatalf("member CanSetInfo() = %t, %v", allowed, err)
	}
	if allowed, err := member.CanSetMute(context.Background(), "group-1"); err != nil || allowed {
		t.Fatalf("member CanSetMute() = %t, %v", allowed, err)
	}
	if allowed, err := member.CanSetMemberMute(context.Background(), "group-1", "member-1"); err != nil || allowed {
		t.Fatalf("member CanSetMemberMute() = %t, %v", allowed, err)
	}
}

func TestOpenIMAdministrationSourcePreservesUnknownOutcomeAndRedactsRejection(t *testing.T) {
	unknownServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer unknownServer.Close()
	unknown, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: testOpenIMContext(unknownServer.URL), HTTPClient: unknownServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown.SetMute(context.Background(), GroupMuteUpdate{GroupID: "group-1", Muted: true}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("SetMute() error = %v, want unknown outcome", err)
	}

	rejectedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer rejectedServer.Close()
	rejected, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: testOpenIMContext(rejectedServer.URL), HTTPClient: rejectedServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = rejected.TransferOwner(context.Background(), GroupOwnerTransfer{GroupID: "group-1", NewOwnerUserID: "member-1"})
	if err == nil || errors.Is(err, operation.ErrOutcomeUnknown) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("TransferOwner() error = %v", err)
	}
}

func TestNewOpenIMAdministrationSourceRequiresContext(t *testing.T) {
	if _, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{}); err == nil {
		t.Fatal("NewOpenIMAdministrationSource() error = nil")
	}
}

func testAdministrationContext(apiAddr, userID string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   userID,
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	return func() context.Context { return base }
}

func administrationStringValue(value string) *string { return &value }
