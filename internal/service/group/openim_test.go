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
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

func TestSDKSourceUsesOnlyServerReadsAndMapsResponses(t *testing.T) {
	client := &fakeOpenIMClient{
		joined:  []*sdkws.GroupInfo{{GroupID: "group-1", GroupName: "Alpha", OwnerUserID: "owner-1", MemberCount: 2, CreateTime: 1000}},
		groups:  []*sdkws.GroupInfo{{GroupID: "group-1", GroupName: "Alpha", OwnerUserID: "owner-1", MemberCount: 2, CreateTime: 1000}},
		members: []*sdkws.GroupMemberFullInfo{{GroupID: "group-1", UserID: "user-1", Nickname: "Alice", RoleLevel: 100, JoinTime: 2000, MuteEndTime: 3000}},
	}
	source, err := NewSDKSource(client)
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}
	source.now = func() time.Time { return time.UnixMilli(2500) }

	groups, err := source.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	wantGroups := []Group{{ID: "group-1", Name: "Alpha", OwnerID: "owner-1", MemberCount: 2, CreatedAt: time.UnixMilli(1000)}}
	if !reflect.DeepEqual(groups, wantGroups) {
		t.Fatalf("List() = %#v, want %#v", groups, wantGroups)
	}

	got, err := source.Get(context.Background(), "group-1")
	if err != nil || !reflect.DeepEqual(got, wantGroups[0]) || !reflect.DeepEqual(client.groupIDs, []string{"group-1"}) {
		t.Fatalf("Get() = %#v, %v; IDs = %v", got, err, client.groupIDs)
	}

	search, err := source.Search(context.Background(), "LPH")
	if err != nil || !reflect.DeepEqual(search, wantGroups) {
		t.Fatalf("Search() = %#v, %v", search, err)
	}

	members, err := source.Members(context.Background(), "group-1")
	wantMembers := []Member{{GroupID: "group-1", UserID: "user-1", Nickname: "Alice", Role: "owner", JoinedAt: time.UnixMilli(2000), Muted: true}}
	if err != nil || !reflect.DeepEqual(members, wantMembers) || client.memberQuery != "" {
		t.Fatalf("Members() = %#v, %v; query = %q", members, err, client.memberQuery)
	}

	if _, err := source.SearchMembers(context.Background(), "group-1", "alice"); err != nil || client.memberQuery != "alice" {
		t.Fatalf("SearchMembers() error = %v; query = %q", err, client.memberQuery)
	}
}

func TestSDKSourceRejectsUnexpectedOrMissingGroups(t *testing.T) {
	if _, err := NewSDKSource(nil); err == nil {
		t.Fatal("NewSDKSource(nil) error = nil")
	}
	source, err := NewSDKSource(&fakeOpenIMClient{})
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}
	if _, err := source.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
	source.client = &fakeOpenIMClient{members: []*sdkws.GroupMemberFullInfo{{GroupID: "other", UserID: "user-1"}}}
	if _, err := source.Members(context.Background(), "group-1"); err == nil {
		t.Fatal("Members(unexpected group) error = nil")
	}
}

func TestOpenIMClientDerivesCancellableSDKContext(t *testing.T) {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: "http://example.invalid"},
	})
	client := OpenIMClient{Context: func() context.Context { return base }}
	caller, cancel := context.WithCancel(context.Background())
	request, _, done, err := client.requestContext(caller)
	if err != nil {
		t.Fatalf("requestContext() error = %v", err)
	}
	defer done()
	if ccontext.Info(request).UserID() != "user-1" || request.Value("operationID") == "" {
		t.Fatalf("request context lost SDK metadata: %#v", request)
	}
	cancel()
	select {
	case <-request.Done():
	case <-time.After(time.Second):
		t.Fatal("request context was not cancelled")
	}

	if _, _, _, err := (OpenIMClient{}).requestContext(context.Background()); err == nil {
		t.Fatal("requestContext() without SDK context error = nil")
	}
}

func TestOpenIMClientPostsAuthenticatedServerReads(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Errorf("unexpected request headers: token=%q operationID=%q", request.Header.Get("token"), request.Header.Get("operationID"))
		}
		switch request.URL.Path {
		case "/group/get_joined_group_list":
			var input pbgroup.GetJoinedGroupListReq
			if !decodeRequest(t, request, &input) {
				return
			}
			if input.FromUserID != "user-1" || input.Pagination.GetShowNumber() != 100 {
				t.Errorf("joined groups request user=%q showNumber=%d", input.FromUserID, input.Pagination.GetShowNumber())
			}
			if input.Pagination.GetPageNumber() == 1 {
				groups := make([]*sdkws.GroupInfo, 100)
				for index := range groups {
					groups[index] = &sdkws.GroupInfo{GroupID: "group-page-1"}
				}
				writeResponse(t, writer, &pbgroup.GetJoinedGroupListResp{Total: 101, Groups: groups})
				return
			}
			writeResponse(t, writer, &pbgroup.GetJoinedGroupListResp{Total: 101, Groups: []*sdkws.GroupInfo{{GroupID: "group-page-2"}}})
		case "/group/get_groups_info":
			var input pbgroup.GetGroupsInfoReq
			if !decodeRequest(t, request, &input) {
				return
			}
			if !reflect.DeepEqual(input.GroupIDs, []string{"group-1"}) {
				t.Errorf("group info request IDs=%v", input.GroupIDs)
			}
			writeResponse(t, writer, &pbgroup.GetGroupsInfoResp{GroupInfos: []*sdkws.GroupInfo{{GroupID: "group-1"}}})
		case "/group/get_group_member_list":
			var input pbgroup.GetGroupMemberListReq
			if !decodeRequest(t, request, &input) {
				return
			}
			if input.GroupID != "group-1" || input.Keyword != "alice" || input.Filter != 0 || input.Pagination.GetPageNumber() != 1 || input.Pagination.GetShowNumber() != 100 {
				t.Errorf("group members request group=%q keyword=%q filter=%d page=%d showNumber=%d", input.GroupID, input.Keyword, input.Filter, input.Pagination.GetPageNumber(), input.Pagination.GetShowNumber())
			}
			writeResponse(t, writer, &pbgroup.GetGroupMemberListResp{Total: 1, Members: []*sdkws.GroupMemberFullInfo{{GroupID: "group-1", UserID: "user-1"}}})
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
	joined, err := client.JoinedGroups(context.Background())
	if err != nil || len(joined) != 101 {
		t.Fatalf("JoinedGroups() = %d results, %v", len(joined), err)
	}
	groups, err := client.Groups(context.Background(), []string{"group-1"})
	if err != nil || len(groups) != 1 || groups[0].GroupID != "group-1" {
		t.Fatalf("Groups() = %#v, %v", groups, err)
	}
	members, err := client.GroupMembers(context.Background(), "group-1", "alice")
	if err != nil || len(members) != 1 || members[0].UserID != "user-1" {
		t.Fatalf("GroupMembers() = %#v, %v", members, err)
	}
	wantPaths := []string{
		"/group/get_joined_group_list", "/group/get_joined_group_list",
		"/group/get_groups_info", "/group/get_group_member_list",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
}

func TestOpenIMClientRedactsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"}); err != nil {
			t.Errorf("encode error response: %v", err)
		}
	}))
	defer server.Close()
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL},
	})
	_, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Groups(context.Background(), []string{"group-1"})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("Groups() error = %v", err)
	}
}

func decodeRequest(t *testing.T, request *http.Request, output any) bool {
	t.Helper()
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(output); err != nil {
		t.Errorf("decode request: %v", err)
		return false
	}
	return true
}

func writeResponse(t *testing.T, writer http.ResponseWriter, data any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

type fakeOpenIMClient struct {
	joined      []*sdkws.GroupInfo
	groups      []*sdkws.GroupInfo
	members     []*sdkws.GroupMemberFullInfo
	groupIDs    []string
	memberQuery string
}

func (c *fakeOpenIMClient) JoinedGroups(context.Context) ([]*sdkws.GroupInfo, error) {
	return c.joined, nil
}

func (c *fakeOpenIMClient) Groups(_ context.Context, ids []string) ([]*sdkws.GroupInfo, error) {
	c.groupIDs = append([]string(nil), ids...)
	return c.groups, nil
}

func (c *fakeOpenIMClient) GroupMembers(_ context.Context, _ string, query string) ([]*sdkws.GroupMemberFullInfo, error) {
	c.memberQuery = query
	return c.members, nil
}
