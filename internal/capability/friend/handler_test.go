package friend

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestFriendActions(t *testing.T) {
	source := &fakeSource{pending: true, friend: true}
	tool, credential := newFriendTool(t, source, "run-1")
	if response := callFriend(t, tool, credential, RequestMethod, "request", RequestInput{UserID: "user-1", Message: "hello"}); !response.OK || source.requestCalls != 1 || source.request != (RequestInput{UserID: "user-1", Message: "hello"}) {
		t.Fatalf("request = %+v, source=%+v", response, source)
	}
	if response := callFriend(t, tool, credential, RespondMethod, "respond", RespondInput{UserID: "user-1", Response: "accept", Message: "welcome"}); !response.OK || source.pendingCalls != 1 || source.respondCalls != 1 || source.respond != (RespondInput{UserID: "user-1", Response: "accept", Message: "welcome"}) {
		t.Fatalf("respond = %+v, source=%+v", response, source)
	}
	if response := callFriend(t, tool, credential, DeleteMethod, "delete", DeleteInput{UserID: "user-1"}); !response.OK || source.friendCalls != 1 || source.deleteCalls != 1 || source.deleted != (DeleteInput{UserID: "user-1"}) {
		t.Fatalf("delete = %+v, source=%+v", response, source)
	}
	if response := callFriend(t, tool, credential, SetRemarkMethod, "remark", SetRemarkInput{UserID: "user-1", Remark: "colleague"}); !response.OK || source.remarkCalls != 1 || source.remark != (SetRemarkInput{UserID: "user-1", Remark: "colleague"}) {
		t.Fatalf("set remark = %+v, source=%+v", response, source)
	}
}

func TestFriendResponseAndDeleteFailClosedForUnverifiedState(t *testing.T) {
	source := &fakeSource{}
	tool, credential := newFriendTool(t, source, "run-1")
	if response := callFriend(t, tool, credential, RespondMethod, "response-missing", RespondInput{UserID: "user-1", Response: "accept"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || source.respondCalls != 0 {
		t.Fatalf("missing pending request = %+v, source=%+v", response, source)
	}
	if response := callFriend(t, tool, credential, DeleteMethod, "delete-missing", DeleteInput{UserID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || source.deleteCalls != 0 {
		t.Fatalf("missing friend = %+v, source=%+v", response, source)
	}

	source.pendingErr = errors.New("server unavailable")
	if response := callFriend(t, tool, credential, RespondMethod, "response-read-error", RespondInput{UserID: "user-1", Response: "reject"}); response.Error == nil || response.Error.Code != contracts.CodeSDKError || source.respondCalls != 0 {
		t.Fatalf("pending read error = %+v, source=%+v", response, source)
	}
}

func TestFriendActionsValidateInputAndPreserveUnknownOutcomes(t *testing.T) {
	source := &fakeSource{requestErr: operation.ErrOutcomeUnknown}
	tool, credential := newFriendTool(t, source, "run-1")
	for _, input := range []struct {
		method string
		value  any
	}{
		{RequestMethod, RequestInput{UserID: ""}},
		{RespondMethod, RespondInput{UserID: "user-1", Response: "maybe"}},
		{DeleteMethod, DeleteInput{UserID: ""}},
		{SetRemarkMethod, SetRemarkInput{UserID: "user-1", Remark: string(make([]byte, maxRemarkBytes+1))}},
	} {
		if response := callFriend(t, tool, credential, input.method, "invalid-"+input.method, input.value); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
			t.Fatalf("invalid %s = %+v", input.method, response)
		}
	}
	request := RequestInput{UserID: "user-1", Message: "hello"}
	if response := callFriend(t, tool, credential, RequestMethod, "unknown", request); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.requestCalls != 1 {
		t.Fatalf("unknown request = %+v, source=%+v", response, source)
	}
	if response := callFriend(t, tool, credential, RequestMethod, "unknown-new-key", request); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || source.requestCalls != 1 {
		t.Fatalf("unknown request rebuilt = %+v, source=%+v", response, source)
	}
}

func TestFriendActionsRequireIdempotencyKey(t *testing.T) {
	source := &fakeSource{pending: true, friend: true}
	tool, credential := newFriendTool(t, source, "run-1")
	params, err := json.Marshal(RequestInput{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "missing-key",
		ProfileID:  "work",
		Method:     RequestMethod,
		Params:     params,
		Grant:      credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument || source.requestCalls != 0 {
		t.Fatalf("missing idempotency key = %+v, source=%+v", response, source)
	}
}

func newFriendTool(t *testing.T, source Source, runID string) (*proxy.Proxy, string) {
	t.Helper()
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(guard, source)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{RequestMethod, RespondMethod, DeleteMethod, SetRemarkMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, runID, "work", handler.ProxyMethods())
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func callFriend(t *testing.T, tool *proxy.Proxy, credential, method, key string, input any) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      key,
		ProfileID:      "work",
		Method:         method,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type fakeSource struct {
	pending    bool
	friend     bool
	pendingErr error
	friendErr  error
	requestErr error
	respondErr error
	deleteErr  error
	remarkErr  error

	pendingCalls int
	friendCalls  int
	requestCalls int
	respondCalls int
	deleteCalls  int
	remarkCalls  int
	request      RequestInput
	respond      RespondInput
	deleted      DeleteInput
	remark       SetRemarkInput
}

func (s *fakeSource) RequestFriend(_ context.Context, input RequestInput) error {
	s.requestCalls++
	s.request = input
	return s.requestErr
}

func (s *fakeSource) RespondFriend(_ context.Context, input RespondInput) error {
	s.respondCalls++
	s.respond = input
	return s.respondErr
}

func (s *fakeSource) DeleteFriend(_ context.Context, input DeleteInput) error {
	s.deleteCalls++
	s.deleted = input
	return s.deleteErr
}

func (s *fakeSource) SetFriendRemark(_ context.Context, input SetRemarkInput) error {
	s.remarkCalls++
	s.remark = input
	return s.remarkErr
}

func (s *fakeSource) HasPendingRequest(context.Context, string) (bool, error) {
	s.pendingCalls++
	return s.pending, s.pendingErr
}

func (s *fakeSource) HasFriend(context.Context, string) (bool, error) {
	s.friendCalls++
	return s.friend, s.friendErr
}

var _ Source = (*fakeSource)(nil)
