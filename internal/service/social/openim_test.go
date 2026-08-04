package social

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/relation"
	"github.com/openimsdk/protocol/sdkws"
)

func TestSDKSourceUsesOnlyServerReadsAndMapsResponses(t *testing.T) {
	client := &fakeServerClient{
		friends: []*sdkws.FriendInfo{{FriendUser: &sdkws.UserInfo{UserID: "user-1", Nickname: "Alpha"}, Remark: "teammate", CreateTime: 1000}},
		friend:  []*sdkws.FriendInfo{{FriendUser: &sdkws.UserInfo{UserID: "user-2", Nickname: "Beta"}, Remark: "reviewer", CreateTime: 2000}},
		blacks:  []*sdkws.BlackInfo{{BlackUserInfo: &sdkws.PublicUserInfo{UserID: "user-3"}, CreateTime: 3000}},
		black:   []*sdkws.BlackInfo{{BlackUserInfo: &sdkws.PublicUserInfo{UserID: "user-4"}, CreateTime: 4000}},
	}
	source, err := NewSDKSource(client)
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}

	friends, err := source.Friends(context.Background())
	if want := []Friend{{UserID: "user-1", Nickname: "Alpha", Remark: "teammate", AddedAt: time.UnixMilli(1000)}}; err != nil || !reflect.DeepEqual(friends, want) {
		t.Fatalf("Friends() = %#v, %v", friends, err)
	}
	search, err := source.SearchFriends(context.Background(), "TEAM")
	if err != nil || !reflect.DeepEqual(search, friends) {
		t.Fatalf("SearchFriends() = %#v, %v", search, err)
	}
	friend, err := source.Friend(context.Background(), "user-2")
	if want := (Friend{UserID: "user-2", Nickname: "Beta", Remark: "reviewer", AddedAt: time.UnixMilli(2000)}); err != nil || !reflect.DeepEqual(friend, want) || !reflect.DeepEqual(client.friendIDs, []string{"user-2"}) {
		t.Fatalf("Friend() = %#v, %v; IDs = %v", friend, err, client.friendIDs)
	}
	blacklist, err := source.Blacklist(context.Background())
	if want := []BlacklistEntry{{UserID: "user-3", BlockedAt: time.UnixMilli(3000)}}; err != nil || !reflect.DeepEqual(blacklist, want) {
		t.Fatalf("Blacklist() = %#v, %v", blacklist, err)
	}
	black, err := source.Black(context.Background(), "user-4")
	if want := (BlacklistEntry{UserID: "user-4", BlockedAt: time.UnixMilli(4000)}); err != nil || !reflect.DeepEqual(black, want) || !reflect.DeepEqual(client.blackIDs, []string{"user-4"}) {
		t.Fatalf("Black() = %#v, %v; IDs = %v", black, err, client.blackIDs)
	}
}

func TestSDKSourceRejectsMissingTarget(t *testing.T) {
	if _, err := NewSDKSource(nil); err == nil {
		t.Fatal("NewSDKSource(nil) error = nil")
	}
	source, err := NewSDKSource(&fakeServerClient{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Friend(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Friend(missing) error = %v", err)
	}
	if _, err := source.Black(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Black(missing) error = %v", err)
	}
}

func TestOpenIMClientPostsAuthenticatedServerReads(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Error("social request was not authenticated")
		}
		switch request.URL.Path {
		case "/friend/get_friend_list":
			var input relation.GetPaginationFriendsReq
			if !socialDecodeRequest(t, request, &input) {
				return
			}
			if input.UserID != "user-1" || input.Pagination == nil || input.Pagination.PageNumber != 1 || input.Pagination.ShowNumber != serverPageSize {
				t.Errorf("friend list request user=%q page=%d size=%d", input.UserID, input.Pagination.GetPageNumber(), input.Pagination.GetShowNumber())
			}
			socialWriteResponse(t, writer, &relation.GetPaginationFriendsResp{FriendsInfo: []*sdkws.FriendInfo{{FriendUser: &sdkws.UserInfo{UserID: "user-2"}}}, Total: 1})
		case "/friend/get_designated_friends":
			var input relation.GetDesignatedFriendsReq
			if !socialDecodeRequest(t, request, &input) {
				return
			}
			if input.OwnerUserID != "user-1" || !reflect.DeepEqual(input.FriendUserIDs, []string{"user-2"}) {
				t.Errorf("designated friends request owner=%q IDs=%v", input.OwnerUserID, input.FriendUserIDs)
			}
			socialWriteResponse(t, writer, &relation.GetDesignatedFriendsResp{FriendsInfo: []*sdkws.FriendInfo{{FriendUser: &sdkws.UserInfo{UserID: "user-2"}}}})
		case "/friend/get_black_list":
			var input relation.GetPaginationBlacksReq
			if !socialDecodeRequest(t, request, &input) {
				return
			}
			if input.UserID != "user-1" || input.Pagination == nil || input.Pagination.PageNumber != 1 || input.Pagination.ShowNumber != serverPageSize {
				t.Errorf("blacklist request user=%q page=%d size=%d", input.UserID, input.Pagination.GetPageNumber(), input.Pagination.GetShowNumber())
			}
			socialWriteResponse(t, writer, &relation.GetPaginationBlacksResp{Blacks: []*sdkws.BlackInfo{{BlackUserInfo: &sdkws.PublicUserInfo{UserID: "user-3"}}}, Total: 1})
		case "/friend/get_specified_blacks":
			var input relation.GetSpecifiedBlacksReq
			if !socialDecodeRequest(t, request, &input) {
				return
			}
			if input.OwnerUserID != "user-1" || !reflect.DeepEqual(input.UserIDList, []string{"user-3"}) {
				t.Errorf("specified blacks request owner=%q IDs=%v", input.OwnerUserID, input.UserIDList)
			}
			socialWriteResponse(t, writer, &relation.GetSpecifiedBlacksResp{Blacks: []*sdkws.BlackInfo{{BlackUserInfo: &sdkws.PublicUserInfo{UserID: "user-3"}}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL},
	})
	client := OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}
	if friends, err := client.Friends(context.Background()); err != nil || len(friends) != 1 {
		t.Fatalf("Friends() = %#v, %v", friends, err)
	}
	if friends, err := client.FriendsByID(context.Background(), []string{"user-2"}); err != nil || len(friends) != 1 {
		t.Fatalf("FriendsByID() = %#v, %v", friends, err)
	}
	if blacks, err := client.Blacklist(context.Background()); err != nil || len(blacks) != 1 {
		t.Fatalf("Blacklist() = %#v, %v", blacks, err)
	}
	if blacks, err := client.BlacksByID(context.Background(), []string{"user-3"}); err != nil || len(blacks) != 1 {
		t.Fatalf("BlacksByID() = %#v, %v", blacks, err)
	}
	if want := []string{"/friend/get_friend_list", "/friend/get_designated_friends", "/friend/get_black_list", "/friend/get_specified_blacks"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestOpenIMClientRedactsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL}})
	_, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Friends(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("Friends() error = %v", err)
	}
}

func socialDecodeRequest(t *testing.T, request *http.Request, output any) bool {
	t.Helper()
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(output); err != nil {
		t.Errorf("decode request: %v", err)
		return false
	}
	return true
}

func socialWriteResponse(t *testing.T, writer http.ResponseWriter, data any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

type fakeServerClient struct {
	friends   []*sdkws.FriendInfo
	friend    []*sdkws.FriendInfo
	friendIDs []string
	blacks    []*sdkws.BlackInfo
	black     []*sdkws.BlackInfo
	blackIDs  []string
}

func (c *fakeServerClient) Friends(context.Context) ([]*sdkws.FriendInfo, error) {
	return c.friends, nil
}

func (c *fakeServerClient) FriendsByID(_ context.Context, ids []string) ([]*sdkws.FriendInfo, error) {
	c.friendIDs = append([]string(nil), ids...)
	return c.friend, nil
}

func (c *fakeServerClient) Blacklist(context.Context) ([]*sdkws.BlackInfo, error) {
	return c.blacks, nil
}

func (c *fakeServerClient) BlacksByID(_ context.Context, ids []string) ([]*sdkws.BlackInfo, error) {
	c.blackIDs = append([]string(nil), ids...)
	return c.black, nil
}
