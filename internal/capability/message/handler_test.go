package message

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestSendTextRequiresManifestGrantAndTypedTarget(t *testing.T) {
	manifest, _ := capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Gated}})
	sender := &fakeSender{}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := New(manifest, guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{Method}, Scopes: []string{Scope}, TargetAllowlists: map[string][]string{Method: {grant.UserTarget("user-1"), grant.GroupTarget("group-1")}}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 4})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key string, input Input) contracts.Response {
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("gated", Input{Text: "hello", RecipientID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("gated send = %+v, calls=%d", response, sender.calls)
	}
	manifest, _ = capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Available}})
	handler, _ = New(manifest, guard, sender)
	tool, _ = proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	if response := call("outside", Input{Text: "hello", RecipientID: "user-2"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("outside recipient = %+v, calls=%d", response, sender.calls)
	}
	if response := call("user", Input{Text: "hello", RecipientID: "user-1"}); !response.OK || sender.calls != 1 || sender.recipientID != "user-1" || sender.groupID != "" {
		t.Fatalf("user send = %+v, sender=%+v", response, sender)
	}
	if response := call("group", Input{Text: "hello group", GroupID: "group-1"}); !response.OK || sender.calls != 2 || sender.recipientID != "" || sender.groupID != "group-1" {
		t.Fatalf("group send = %+v, sender=%+v", response, sender)
	}
}

func TestUnknownTextSendCannotBeRebuiltWithNewKey(t *testing.T) {
	manifest, _ := capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Available}})
	sender := &fakeSender{err: operation.ErrOutcomeUnknown}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := New(manifest, guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{Method}, Scopes: []string{Scope}, TargetAllowlists: map[string][]string{Method: {grant.UserTarget("user-1")}}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	raw, _ := json.Marshal(Input{Text: "hello", RecipientID: "user-1"})
	call := func(key string) contracts.Response {
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("first"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown {
		t.Fatalf("first unknown = %+v", response)
	}
	if response := call("new-key"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 1 {
		t.Fatalf("new key rebuild = %+v, calls=%d", response, sender.calls)
	}
}

type fakeSender struct {
	calls       int
	text        string
	recipientID string
	groupID     string
	err         error
}

func (s *fakeSender) SendText(_ context.Context, text, recipientID, groupID string) error {
	s.calls++
	s.text = text
	s.recipientID = recipientID
	s.groupID = groupID
	return s.err
}

var _ Sender = (*fakeSender)(nil)
