package proxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestProxyOnlyInvokesGrantedRegisteredMethods(t *testing.T) {
	store := grant.NewStore()
	_, credential, err := store.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider",
		Methods: []string{"conversation.get"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool, err := New(store, "run-1", "work", []Method{{
		Name: "conversation.get",
		Handle: func(_ context.Context, _ contracts.Request, _ grant.Grant) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"id":"conversation-1"}`), nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method string) contracts.Request {
		return contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: method, ProfileID: "work", Method: method, Params: json.RawMessage(`{}`), Grant: credential}
	}
	response, err := tool.Call(context.Background(), request("conversation.get"))
	if err != nil || !response.OK || calls != 1 {
		t.Fatalf("allowed Call() = %+v, %v; calls = %d", response, err, calls)
	}
	response, err = tool.Call(context.Background(), request("daemon.shutdown"))
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || calls != 1 {
		t.Fatalf("unregistered method = %+v, %v; calls = %d", response, err, calls)
	}
}

func TestProxyCloseFailsClosed(t *testing.T) {
	store := grant.NewStore()
	_, credential, err := store.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := New(store, "run-1", "work", []Method{{Name: "profile.get", Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tool.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "req-1", ProfileID: "work", Method: "profile.get", Params: json.RawMessage(`{}`), Grant: credential})
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodeGrantInvalid {
		t.Fatalf("Call() after Close = %+v, %v", response, err)
	}
}
