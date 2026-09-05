package message

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	pbconstant "github.com/abd-im/abd-im-protocol/constant"
	"github.com/abd-im/abd-im-protocol/sdkws"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestSDKSourceUsesOnlyServerReadsAndMapsResponses(t *testing.T) {
	conversationID := "si_user-1_user-2"
	client := &fakeOpenIMClient{messages: []*sdkws.MsgData{
		{ServerMsgID: "server-1", ClientMsgID: "client-1", SendID: "user-2", RecvID: "user-1", SessionType: pbconstant.SingleChatType, ContentType: pbconstant.Text, Content: []byte(`{"content":"A matching message"}`), SendTime: 1000},
		{ServerMsgID: "server-stream", SendID: "user-1", RecvID: "user-2", SessionType: pbconstant.SingleChatType, ContentType: pbconstant.Stream, Content: []byte(`{"type":"text","content":"Streamed","packets":[" message"],"end":true}`), SendTime: 1500},
		{ServerMsgID: "server-2", SendID: "user-1", RecvID: "user-2", SessionType: pbconstant.SingleChatType, ContentType: pbconstant.File, Content: []byte(`{"fileName":"private.pdf"}`), SendTime: 2000},
	}}
	source, err := NewSDKSource(client)
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}

	history, err := source.History(context.Background(), HistoryQuery{ConversationID: conversationID, Limit: 100})
	want := []Message{
		{ID: "server-1", ConversationID: conversationID, SenderID: "user-2", Type: "text", Text: "A matching message", CreatedAt: time.UnixMilli(1000)},
		{ID: "server-stream", ConversationID: conversationID, SenderID: "user-1", Type: "text", Text: "Streamed message", CreatedAt: time.UnixMilli(1500)},
		{ID: "server-2", ConversationID: conversationID, SenderID: "user-1", Type: "unknown", CreatedAt: time.UnixMilli(2000)},
	}
	if err != nil || !reflect.DeepEqual(history, want) || client.conversationID != conversationID || client.limit != 100 {
		t.Fatalf("History() = %#v, %v; request = %q/%d", history, err, client.conversationID, client.limit)
	}

	search, err := source.Search(context.Background(), HistoryQuery{ConversationID: conversationID, Limit: 100}, "MESSAGE")
	if err != nil || !reflect.DeepEqual(search, want[:2]) {
		t.Fatalf("Search() = %#v, %v", search, err)
	}
	get, err := source.Get(context.Background(), conversationID, "server-1")
	if err != nil || !reflect.DeepEqual(get, want[0]) {
		t.Fatalf("Get() = %#v, %v", get, err)
	}
	if _, err := source.Get(context.Background(), conversationID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
}

func TestSDKSourceRejectsUnexpectedConversation(t *testing.T) {
	if _, err := NewSDKSource(nil); err == nil {
		t.Fatal("NewSDKSource(nil) error = nil")
	}
	source, err := NewSDKSource(&fakeOpenIMClient{messages: []*sdkws.MsgData{{ServerMsgID: "server-1", SendID: "user-3", RecvID: "user-4", SessionType: pbconstant.SingleChatType}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.History(context.Background(), HistoryQuery{ConversationID: "si_user-1_user-2", Limit: 100}); err == nil {
		t.Fatal("History() accepted a message from another conversation")
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

func TestOpenIMClientPostsAuthenticatedMessageRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/msg/pull_msg_by_seq" || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Error("message request was not authenticated")
		}
		var input sdkws.PullMessageBySeqsReq
		if !decodeRequest(t, request, &input) {
			return
		}
		if input.UserID != "user-1" || input.Order != sdkws.PullOrder_PullOrderAsc || len(input.SeqRanges) != 1 {
			t.Fatalf("message request user=%q order=%d ranges=%d", input.UserID, input.Order, len(input.SeqRanges))
		}
		rangeInput := input.SeqRanges[0]
		if rangeInput.ConversationID != "si_user-1_user-2" || rangeInput.Begin != 1 || rangeInput.End != math.MaxInt64 || rangeInput.Num != 100 {
			t.Errorf("message range = %#v", rangeInput)
		}
		writeResponse(t, writer, &sdkws.PullMessageBySeqsResp{Msgs: map[string]*sdkws.PullMsgs{
			"si_user-1_user-2": {Msgs: []*sdkws.MsgData{{ServerMsgID: "server-1"}}},
		}})
	}))
	defer server.Close()

	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL},
	})
	items, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Messages(context.Background(), "si_user-1_user-2", 200)
	if err != nil || len(items) != 1 || items[0].ServerMsgID != "server-1" {
		t.Fatalf("Messages() = %#v, %v", items, err)
	}
}

func TestOpenIMClientRedactsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL}})
	_, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Messages(context.Background(), "si_user-1_user-2", 1)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("Messages() error = %v", err)
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
	messages       []*sdkws.MsgData
	conversationID string
	limit          int
}

func (c *fakeOpenIMClient) Messages(_ context.Context, conversationID string, limit int) ([]*sdkws.MsgData, error) {
	c.conversationID = conversationID
	c.limit = limit
	return c.messages, nil
}
