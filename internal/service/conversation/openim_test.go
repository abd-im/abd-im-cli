package conversation

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

	pbconstant "github.com/abd-im/abd-im-protocol/constant"
	pbconversation "github.com/abd-im/abd-im-protocol/conversation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestSDKSourceUsesOnlyServerReadsAndMapsResponses(t *testing.T) {
	serverItems := []*pbconversation.Conversation{
		{ConversationID: "single_user-1", ConversationType: pbconstant.SingleChatType, UserID: "user-1", IsPinned: true},
		{ConversationID: "group_group-1", ConversationType: pbconstant.ReadGroupChatType, GroupID: "group-1", RecvMsgOpt: pbconstant.ReceiveNotNotifyMessage},
	}
	client := &fakeOpenIMClient{all: serverItems, byID: serverItems[:1]}
	source, err := NewSDKSource(client)
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}

	items, err := source.List(context.Background())
	want := []Conversation{
		{ID: "single_user-1", Type: "single", UserID: "user-1", Pinned: true},
		{ID: "group_group-1", Type: "group_read", GroupID: "group-1", Muted: true},
	}
	if err != nil || !reflect.DeepEqual(items, want) {
		t.Fatalf("List() = %#v, %v, want %#v", items, err, want)
	}

	item, err := source.Get(context.Background(), "single_user-1")
	if err != nil || !reflect.DeepEqual(item, want[0]) || !reflect.DeepEqual(client.ids, []string{"single_user-1"}) {
		t.Fatalf("Get() = %#v, %v; IDs = %v", item, err, client.ids)
	}
	search, err := source.Search(context.Background(), "GROUP-1")
	if err != nil || !reflect.DeepEqual(search, want[1:]) {
		t.Fatalf("Search() = %#v, %v", search, err)
	}
}

func TestSDKSourceRejectsMissingConversation(t *testing.T) {
	if _, err := NewSDKSource(nil); err == nil {
		t.Fatal("NewSDKSource(nil) error = nil")
	}
	source, err := NewSDKSource(&fakeOpenIMClient{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
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
			t.Error("conversation request was not authenticated")
		}
		switch request.URL.Path {
		case "/conversation/get_all_conversations":
			var input pbconversation.GetAllConversationsReq
			if !decodeRequest(t, request, &input) {
				return
			}
			if input.OwnerUserID != "user-1" {
				t.Errorf("all conversations owner = %q", input.OwnerUserID)
			}
			writeResponse(t, writer, &pbconversation.GetAllConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1"}}})
		case "/conversation/get_conversations":
			var input pbconversation.GetConversationsReq
			if !decodeRequest(t, request, &input) {
				return
			}
			if input.OwnerUserID != "user-1" || !reflect.DeepEqual(input.ConversationIDs, []string{"conversation-1"}) {
				t.Errorf("conversation request owner=%q IDs=%v", input.OwnerUserID, input.ConversationIDs)
			}
			writeResponse(t, writer, &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1"}}})
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
	all, err := client.AllConversations(context.Background())
	if err != nil || len(all) != 1 || all[0].ConversationID != "conversation-1" {
		t.Fatalf("AllConversations() = %#v, %v", all, err)
	}
	items, err := client.Conversations(context.Background(), []string{"conversation-1"})
	if err != nil || len(items) != 1 || items[0].ConversationID != "conversation-1" {
		t.Fatalf("Conversations() = %#v, %v", items, err)
	}
	if want := []string{"/conversation/get_all_conversations", "/conversation/get_conversations"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestOpenIMClientRedactsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL}})
	_, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).AllConversations(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("AllConversations() error = %v", err)
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
	all  []*pbconversation.Conversation
	byID []*pbconversation.Conversation
	ids  []string
}

func (c *fakeOpenIMClient) AllConversations(context.Context) ([]*pbconversation.Conversation, error) {
	return c.all, nil
}

func (c *fakeOpenIMClient) Conversations(_ context.Context, ids []string) ([]*pbconversation.Conversation, error) {
	c.ids = append([]string(nil), ids...)
	return c.byID, nil
}
