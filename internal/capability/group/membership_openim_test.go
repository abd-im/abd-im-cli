package group

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMMembershipSourceUsesFixedStateAndActionEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("invalid authenticated request %s %s", request.Method, request.URL.Path)
		}
		switch request.URL.Path {
		case "/group/join_group":
			var input pbgroup.JoinGroupReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.ReqMessage != "please add me" || input.JoinSource != pbconstant.JoinBySearch || input.InviterUserID != "owner-1" || input.Ex != "" {
				t.Fatalf("join input group=%q message=%q source=%d inviter=%q ex=%q", input.GroupID, input.ReqMessage, input.JoinSource, input.InviterUserID, input.Ex)
			}
		case "/group/quit_group":
			var input pbgroup.QuitGroupReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.UserID != "owner-1" {
				t.Fatalf("leave input group=%q user=%q", input.GroupID, input.UserID)
			}
		case "/group/invite_user_to_group":
			var input pbgroup.InviteUserToGroupReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.Reason != "project" || !reflect.DeepEqual(input.InvitedUserIDs, []string{"member-new"}) || input.SendMessage != nil {
				t.Fatalf("invite input group=%q reason=%q users=%v send_message=%v", input.GroupID, input.Reason, input.InvitedUserIDs, input.SendMessage)
			}
		case "/group/kick_group":
			var input pbgroup.KickGroupMemberReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || input.Reason != "done" || !reflect.DeepEqual(input.KickedUserIDs, []string{"member-old"}) || input.SendMessage != nil {
				t.Fatalf("remove input group=%q reason=%q users=%v send_message=%v", input.GroupID, input.Reason, input.KickedUserIDs, input.SendMessage)
			}
		case "/group/get_group_members_info":
			var input pbgroup.GetGroupMembersInfoReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.GroupID != "group-1" || len(input.UserIDs) == 0 || input.UserIDs[0] != "owner-1" {
				t.Fatalf("state input group=%q users=%v", input.GroupID, input.UserIDs)
			}
			members := []*sdkws.GroupMemberFullInfo{{GroupID: "group-1", UserID: "owner-1", RoleLevel: constant.GroupOwner}}
			switch {
			case len(input.UserIDs) == 1:
				members[0].RoleLevel = constant.GroupAdmin
			case len(input.UserIDs) == 2 && input.UserIDs[1] == "member-new":
				// The invitee is absent, as required for a fresh invitation.
			case len(input.UserIDs) == 2 && input.UserIDs[1] == "member-old":
				members[0].RoleLevel = constant.GroupAdmin
				members = append(members, &sdkws.GroupMemberFullInfo{GroupID: "group-1", UserID: "member-old", RoleLevel: constant.GroupOrdinaryUsers})
			default:
				t.Fatalf("unexpected state member query group=%q users=%v", input.GroupID, input.UserIDs)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": map[string]any{"members": members}})
			return
		default:
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
	}))
	defer server.Close()

	source, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := source.JoinGroup(ctx, JoinInput{GroupID: "group-1", Message: "please add me"}); err != nil {
		t.Fatalf("JoinGroup() error = %v", err)
	}
	if allowed, err := source.CanLeaveGroup(ctx, "group-1"); err != nil || !allowed {
		t.Fatalf("CanLeaveGroup() = %t, %v", allowed, err)
	}
	if err := source.LeaveGroup(ctx, LeaveInput{GroupID: "group-1"}); err != nil {
		t.Fatalf("LeaveGroup() error = %v", err)
	}
	if allowed, err := source.CanInviteMembers(ctx, "group-1", []string{"member-new"}); err != nil || !allowed {
		t.Fatalf("CanInviteMembers() = %t, %v", allowed, err)
	}
	if err := source.InviteMembers(ctx, MembersInput{GroupID: "group-1", UserIDs: []string{"member-new"}, Reason: "project"}); err != nil {
		t.Fatalf("InviteMembers() error = %v", err)
	}
	if allowed, err := source.CanRemoveMembers(ctx, "group-1", []string{"member-old"}); err != nil || !allowed {
		t.Fatalf("CanRemoveMembers() = %t, %v", allowed, err)
	}
	if err := source.RemoveMembers(ctx, MembersInput{GroupID: "group-1", UserIDs: []string{"member-old"}, Reason: "done"}); err != nil {
		t.Fatalf("RemoveMembers() error = %v", err)
	}
}

func TestOpenIMMembershipSourceFailsClosedForRolesAndExistingMembers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/group/get_group_members_info" {
			t.Fatalf("unexpected action request %s", request.URL.Path)
		}
		var input pbgroup.GetGroupMembersInfoReq
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		members := []*sdkws.GroupMemberFullInfo{{GroupID: input.GroupID, UserID: "owner-1", RoleLevel: constant.GroupOwner}}
		if len(input.UserIDs) == 2 {
			members = append(members, &sdkws.GroupMemberFullInfo{GroupID: input.GroupID, UserID: input.UserIDs[1], RoleLevel: constant.GroupAdmin})
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": map[string]any{"members": members}})
	}))
	defer server.Close()
	source, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := source.CanLeaveGroup(context.Background(), "group-1"); err != nil || allowed {
		t.Fatalf("owner CanLeaveGroup() = %t, %v", allowed, err)
	}
	if allowed, err := source.CanInviteMembers(context.Background(), "group-1", []string{"existing"}); err != nil || allowed {
		t.Fatalf("existing CanInviteMembers() = %t, %v", allowed, err)
	}
	if allowed, err := source.CanRemoveMembers(context.Background(), "group-1", []string{"owner-1"}); err != nil || allowed {
		t.Fatalf("owner CanRemoveMembers(self) = %t, %v", allowed, err)
	}
}

func TestOpenIMMembershipSourcePreservesUnknownOutcomeAndRedactsRejection(t *testing.T) {
	unknownServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer unknownServer.Close()
	unknown, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: testOpenIMContext(unknownServer.URL), HTTPClient: unknownServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown.JoinGroup(context.Background(), JoinInput{GroupID: "group-1"}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("JoinGroup() error = %v, want unknown outcome", err)
	}

	rejectedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer rejectedServer.Close()
	rejected, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: testOpenIMContext(rejectedServer.URL), HTTPClient: rejectedServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = rejected.LeaveGroup(context.Background(), LeaveInput{GroupID: "group-1"})
	if err == nil || errors.Is(err, operation.ErrOutcomeUnknown) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("LeaveGroup() error = %v", err)
	}
}

func TestNewOpenIMMembershipSourceRequiresContext(t *testing.T) {
	if _, err := NewOpenIMMembershipSource(OpenIMMembershipSource{}); err == nil {
		t.Fatal("NewOpenIMMembershipSource() error = nil")
	}
}
