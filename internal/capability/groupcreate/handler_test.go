package groupcreate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/agent/proxy"
	"github.com/abd-im-cli/abdim-cli/internal/capability"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/control"
	"github.com/abd-im-cli/abdim-cli/internal/operation"
)

func TestGroupCreateRequiresManifestGrantAndMemberAllowlist(t *testing.T) {
	manifest, _ := capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Gated}})
	creator := &fakeCreator{}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := New(manifest, guard, creator)
	tools := grant.NewStore()
	_, credential, _ := tools.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{Method}, Scopes: []string{Scope}, TargetAllowlist: []string{"member-1", "member-2"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	toolProxy, _ := proxy.New(tools, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key string, members []string) contracts.Response {
		raw, _ := json.Marshal(Input{Name: "team", MemberIDs: members})
		response, _ := toolProxy.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("key-1", []string{"member-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || creator.calls != 0 {
		t.Fatalf("gated capability = %+v, calls=%d", response, creator.calls)
	}
	manifest, _ = capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Available}})
	handler, _ = New(manifest, guard, creator)
	toolProxy, _ = proxy.New(tools, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	if response := call("key-2", []string{"member-outside"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || creator.calls != 0 {
		t.Fatalf("target denial = %+v, calls=%d", response, creator.calls)
	}
	if response := call("key-3", []string{"member-1", "member-2"}); !response.OK || creator.calls != 1 {
		t.Fatalf("allowed group.create = %+v, calls=%d", response, creator.calls)
	}
}

func TestUnknownGroupCreateCannotBeRebuiltWithNewKey(t *testing.T) {
	manifest, _ := capability.New([]capability.Entry{{Method: Method, Scope: Scope, Status: capability.Available}})
	creator := &fakeCreator{err: operation.ErrOutcomeUnknown}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := New(manifest, guard, creator)
	tools := grant.NewStore()
	_, credential, _ := tools.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{Method}, Scopes: []string{Scope}, TargetAllowlist: []string{"member-1"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	toolProxy, _ := proxy.New(tools, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	raw, _ := json.Marshal(Input{Name: "team", MemberIDs: []string{"member-1"}})
	call := func(key string) contracts.Response {
		response, _ := toolProxy.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: Method, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("first"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown {
		t.Fatalf("first unknown = %+v", response)
	}
	if response := call("new-key"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || creator.calls != 1 {
		t.Fatalf("new key rebuild = %+v, calls=%d", response, creator.calls)
	}
}

type fakeCreator struct {
	calls int
	err   error
}

func (c *fakeCreator) CreateGroup(context.Context, Input) error { c.calls++; return c.err }
