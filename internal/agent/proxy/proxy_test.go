package proxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

func TestProxyOnlyInvokesAllowedTypedMethodsAndTargets(t *testing.T) {
	store := grant.NewStore()
	_, credential, err := store.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"conversation.get"}, Scopes: []string{"conversation.read"}, TargetAllowlist: []string{"conversation-1"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	calls := 0
	proxy, err := New(store, "run-1", "work", []Method{{
		Name:  "conversation.get",
		Scope: "conversation.read",
		Targets: func(params json.RawMessage) ([]string, error) {
			var input struct {
				ConversationID string `json:"conversation_id"`
			}
			if err := json.Unmarshal(params, &input); err != nil {
				return nil, err
			}
			return []string{input.ConversationID}, nil
		},
		Handle: func(_ context.Context, _ contracts.Request, _ grant.Grant) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"id":"conversation-1"}`), nil
		},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := func(method, target string) contracts.Request {
		return contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: method + "-request", ProfileID: "work", Method: method, Params: json.RawMessage(`{"conversation_id":"` + target + `"}`), Grant: credential}
	}
	response, err := proxy.Call(context.Background(), request("conversation.get", "conversation-1"))
	if err != nil || !response.OK || calls != 1 {
		t.Fatalf("allowed Call() = %+v, %v; calls = %d", response, err, calls)
	}
	response, err = proxy.Call(context.Background(), request("conversation.get", "conversation-2"))
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || calls != 1 {
		t.Fatalf("target denial = %+v, %v; calls = %d", response, err, calls)
	}
	response, err = proxy.Call(context.Background(), request("conversation.get", ""))
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument || calls != 1 {
		t.Fatalf("missing target = %+v, %v; calls = %d", response, err, calls)
	}
	response, err = proxy.Call(context.Background(), request("daemon.shutdown", "conversation-1"))
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || calls != 1 {
		t.Fatalf("controller denial = %+v, %v; calls = %d", response, err, calls)
	}
}

func TestProxyCloseAndGrantExpiryFailClosed(t *testing.T) {
	store := grant.NewStore()
	_, credential, err := store.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, Scopes: []string{"profile.read"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	proxy, err := New(store, "run-1", "work", []Method{{Name: "profile.get", Scope: "profile.read", Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := proxy.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	response, err := proxy.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "req-1", ProfileID: "work", Method: "profile.get", Params: json.RawMessage(`{}`), Grant: credential})
	if err != nil || response.Error == nil || response.Error.Code != contracts.CodeGrantInvalid {
		t.Fatalf("Call() after Close = %+v, %v", response, err)
	}
}
