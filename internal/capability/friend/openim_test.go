package friend

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
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/relation"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMActionsUseFixedEndpointsAndProfileOwner(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Error("friend action request was not authenticated")
		}
		switch request.URL.Path {
		case "/friend/add_friend":
			var input relation.ApplyToAddFriendReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetFromUserID() != "owner-1" || input.GetToUserID() != "user-2" || input.GetReqMsg() != "hello" {
				t.Errorf("friend request = %+v", &input)
			}
			writeFriendAction(t, writer, nil)
		case "/friend/get_designated_friend_apply":
			var input relation.GetDesignatedFriendsApplyReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetFromUserID() != "user-2" || input.GetToUserID() != "owner-1" {
				t.Errorf("pending request source = %+v", &input)
			}
			writeFriendAction(t, writer, &relation.GetDesignatedFriendsApplyResp{FriendRequests: []*sdkws.FriendRequest{{FromUserID: "user-2", ToUserID: "owner-1", HandleResult: 0}}})
		case "/friend/add_friend_response":
			var input relation.RespondFriendApplyReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetFromUserID() != "user-2" || input.GetToUserID() != "owner-1" || input.GetHandleResult() != friendResponseAccept || input.GetHandleMsg() != "accepted" {
				t.Errorf("friend response = %+v", &input)
			}
			writeFriendAction(t, writer, nil)
		case "/friend/get_designated_friends":
			var input relation.GetDesignatedFriendsReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetOwnerUserID() != "owner-1" || !reflect.DeepEqual(input.GetFriendUserIDs(), []string{"user-2"}) {
				t.Errorf("friend source = %+v", &input)
			}
			writeFriendAction(t, writer, &relation.GetDesignatedFriendsResp{FriendsInfo: []*sdkws.FriendInfo{{OwnerUserID: "owner-1", FriendUser: &sdkws.UserInfo{UserID: "user-2"}}}})
		case "/friend/delete_friend":
			var input relation.DeleteFriendReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetOwnerUserID() != "owner-1" || input.GetFriendUserID() != "user-2" {
				t.Errorf("friend delete = %+v", &input)
			}
			writeFriendAction(t, writer, nil)
		case "/friend/set_friend_remark":
			var input relation.SetFriendRemarkReq
			if !decodeFriendAction(t, request, &input) {
				return
			}
			if input.GetOwnerUserID() != "owner-1" || input.GetFriendUserID() != "user-2" || input.GetRemark() != "colleague" {
				t.Errorf("friend remark = %+v", &input)
			}
			writeFriendAction(t, writer, nil)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	actions, err := NewOpenIMActions(OpenIMActions{Context: friendActionTestContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := actions.RequestFriend(context.Background(), RequestInput{UserID: "user-2", Message: "hello"}); err != nil {
		t.Fatalf("RequestFriend() error = %v", err)
	}
	if pending, err := actions.HasPendingRequest(context.Background(), "user-2"); err != nil || !pending {
		t.Fatalf("HasPendingRequest() = %t, %v", pending, err)
	}
	if err := actions.RespondFriend(context.Background(), RespondInput{UserID: "user-2", Response: "accept", Message: "accepted"}); err != nil {
		t.Fatalf("RespondFriend() error = %v", err)
	}
	if exists, err := actions.HasFriend(context.Background(), "user-2"); err != nil || !exists {
		t.Fatalf("HasFriend() = %t, %v", exists, err)
	}
	if err := actions.DeleteFriend(context.Background(), DeleteInput{UserID: "user-2"}); err != nil {
		t.Fatalf("DeleteFriend() error = %v", err)
	}
	if err := actions.SetFriendRemark(context.Background(), SetRemarkInput{UserID: "user-2", Remark: "colleague"}); err != nil {
		t.Fatalf("SetFriendRemark() error = %v", err)
	}
	if want := []string{"/friend/add_friend", "/friend/get_designated_friend_apply", "/friend/add_friend_response", "/friend/get_designated_friends", "/friend/delete_friend", "/friend/set_friend_remark"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestOpenIMActionsFailClosedAndRedactServerFailure(t *testing.T) {
	rejected := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer rejected.Close()
	actions, err := NewOpenIMActions(OpenIMActions{Context: friendActionTestContext(rejected.URL), HTTPClient: rejected.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := actions.RequestFriend(context.Background(), RequestInput{UserID: "user-2"}); err == nil || strings.Contains(err.Error(), "secret-token") || errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("rejected RequestFriend() = %v", err)
	}
	if _, err := actions.HasFriend(context.Background(), "user-2"); err == nil || strings.Contains(err.Error(), "secret-token") || errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("rejected HasFriend() = %v", err)
	}

	unknown := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusBadGateway) }))
	defer unknown.Close()
	actions, err = NewOpenIMActions(OpenIMActions{Context: friendActionTestContext(unknown.URL), HTTPClient: unknown.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := actions.RequestFriend(context.Background(), RequestInput{UserID: "user-2"}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("unknown RequestFriend() = %v", err)
	}
	if _, err := NewOpenIMActions(OpenIMActions{}); err == nil {
		t.Fatal("NewOpenIMActions accepted nil context")
	}
}

func friendActionTestContext(apiAddr string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "owner-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	return func() context.Context { return base }
}

func decodeFriendAction(t *testing.T, request *http.Request, output any) bool {
	t.Helper()
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(output); err != nil {
		t.Errorf("decode friend action request: %v", err)
		return false
	}
	return true
}

func writeFriendAction(t *testing.T, writer http.ResponseWriter, data any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": data}); err != nil {
		t.Errorf("encode friend action response: %v", err)
	}
}
