package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbmsg "github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMMarkReadResolvesFixedServerBoundary(t *testing.T) {
	client := &fakeMarkReadClient{messages: []*sdkws.MsgData{{
		ServerMsgID: "message-1", SendID: "user-2", RecvID: "user-1", SessionType: pbconstant.SingleChatType, Seq: 12,
	}}}
	action, err := NewOpenIMMarkRead(OpenIMMarkRead{Context: markReadTestContext("https://im.example.test"), Client: client})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := action.ResolveBoundary(context.Background(), "si_user-1_user-2", "message-1")
	if err != nil || boundary != (Boundary{ConversationID: "si_user-1_user-2", MessageID: "message-1", ServerSeq: 12}) || client.limit != markReadHistoryLimit {
		t.Fatalf("ResolveBoundary() = %#v, %v; limit=%d", boundary, err, client.limit)
	}
	client.messages[0].RecvID = "user-3"
	if _, err := action.ResolveBoundary(context.Background(), "si_user-1_user-2", "message-1"); err == nil {
		t.Fatal("ResolveBoundary() accepted another conversation")
	}
}

func TestOpenIMMarkReadPostsAuthenticatedFixedAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/msg/mark_conversation_as_read" || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("request = %s %s token=%q operation=%q", request.Method, request.URL.Path, request.Header.Get("token"), request.Header.Get("operationID"))
		}
		var input pbmsg.MarkConversationAsReadReq
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.UserID != "user-1" || input.ConversationID != "si_user-1_user-2" || input.HasReadSeq != 12 || len(input.Seqs) != 0 {
			t.Fatalf("mark read input user=%q conversation=%q sequence=%d extra_sequences=%d", input.UserID, input.ConversationID, input.HasReadSeq, len(input.Seqs))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
	}))
	defer server.Close()
	action, err := NewOpenIMMarkRead(OpenIMMarkRead{Context: markReadTestContext(server.URL), Client: &fakeMarkReadClient{}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := action.MarkRead(context.Background(), MarkReadRequest{ConversationID: "si_user-1_user-2", HasReadSeq: 12}); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}
}

func TestOpenIMMarkReadPreservesUnknownAndRedactsRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/unknown" {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	action, err := NewOpenIMMarkRead(OpenIMMarkRead{Context: markReadTestContext(server.URL), Client: &fakeMarkReadClient{}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := action.MarkRead(context.Background(), MarkReadRequest{ConversationID: "si_user-1_user-2", HasReadSeq: 1}); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("MarkRead() rejection = %v", err)
	}
	unknownServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusBadGateway) }))
	defer unknownServer.Close()
	unknown, err := NewOpenIMMarkRead(OpenIMMarkRead{Context: markReadTestContext(unknownServer.URL), Client: &fakeMarkReadClient{}, HTTPClient: unknownServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown.MarkRead(context.Background(), MarkReadRequest{ConversationID: "si_user-1_user-2", HasReadSeq: 1}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("MarkRead() unknown = %v", err)
	}
}

func markReadTestContext(apiAddr string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr}})
	return func() context.Context { return base }
}

type fakeMarkReadClient struct {
	messages []*sdkws.MsgData
	limit    int
}

func (c *fakeMarkReadClient) Messages(_ context.Context, _ string, limit int) ([]*sdkws.MsgData, error) {
	c.limit = limit
	return c.messages, nil
}

var _ messageservice.Client = (*fakeMarkReadClient)(nil)
