package message

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbconstant "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
)

func TestRevoke(t *testing.T) {

	revoker := &fakeRevoker{}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := NewRevoke(guard, revoker)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{RevokeMethod}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key string, input RevokeInput) contracts.Response {
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: RevokeMethod, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("allowed", RevokeInput{ConversationID: "si_user-1_user-2", MessageID: "message-1"}); !response.OK || revoker.calls != 1 {
		t.Fatalf("allowed revoke = %+v, calls=%d", response, revoker.calls)
	}
}

func TestOpenIMRevokeVerifiesOwnerAndPostsFixedServerAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/msg/revoke_msg" || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("request = %s %s token=%q operation=%q", request.Method, request.URL.Path, request.Header.Get("token"), request.Header.Get("operationID"))
		}
		var input struct {
			UserID         string `json:"userID"`
			ConversationID string `json:"conversationID"`
			Seq            int64  `json:"seq"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.UserID != "user-1" || input.ConversationID != "si_user-1_user-2" || input.Seq != 12 {
			t.Fatalf("revoke input = %+v", input)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
	}))
	defer server.Close()
	client := &fakeRevokeClient{messages: []*sdkws.MsgData{{ServerMsgID: "message-1", SendID: "user-1", RecvID: "user-2", SessionType: pbconstant.SingleChatType, Seq: 12}}}
	action, err := NewOpenIMRevoke(OpenIMRevoke{Context: revokeTestContext(server.URL), Client: client, HTTPClient: server.Client()}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.Revoke(context.Background(), RevokeInput{ConversationID: "si_user-1_user-2", MessageID: "message-1"}); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	client.messages[0].SendID = "user-2"
	if err := action.Revoke(context.Background(), RevokeInput{ConversationID: "si_user-1_user-2", MessageID: "message-1"}); err == nil {
		t.Fatal("Revoke() accepted a message owned by another user")
	}
}

func TestOpenIMRevokePreservesUnknownOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusBadGateway) }))
	defer server.Close()
	action, err := NewOpenIMRevoke(OpenIMRevoke{Context: revokeTestContext(server.URL), Client: &fakeRevokeClient{messages: []*sdkws.MsgData{{ServerMsgID: "message-1", SendID: "user-1", RecvID: "user-2", SessionType: pbconstant.SingleChatType, Seq: 1}}}, HTTPClient: server.Client()}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.Revoke(context.Background(), RevokeInput{ConversationID: "si_user-1_user-2", MessageID: "message-1"}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("unknown revoke = %v", err)
	}
}

func revokeTestContext(apiAddr string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr}})
	return func() context.Context { return base }
}

type fakeRevoker struct{ calls int }

func (r *fakeRevoker) Revoke(_ context.Context, _ RevokeInput) error { r.calls++; return nil }

type fakeRevokeClient struct{ messages []*sdkws.MsgData }

func (c *fakeRevokeClient) Messages(_ context.Context, _ string, _ int) ([]*sdkws.MsgData, error) {
	return c.messages, nil
}

var _ Revoker = (*fakeRevoker)(nil)
