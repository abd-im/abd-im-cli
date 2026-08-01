package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestGroupCreateAllowlistAndIdempotencyE2E(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	creator := &e2eGroupCreator{}
	tool, credential := newGroupCreateTool(t, store, creator, "run-confirmed", []string{"member-allowed"})
	if response := callGroupCreate(t, tool, credential, "outside", groupcapability.Input{Name: "team", MemberIDs: []string{"member-outside"}}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || creator.calls != 0 {
		t.Fatalf("outside member response/calls = %+v/%d", response, creator.calls)
	}

	input := groupcapability.Input{Name: "team", MemberIDs: []string{"member-allowed"}}
	first := callGroupCreate(t, tool, credential, "confirmed", input)
	if !first.OK || creator.calls != 1 {
		t.Fatalf("first group.create response/calls = %+v/%d", first, creator.calls)
	}
	var firstResult struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(first.Data, &firstResult); err != nil || firstResult.OperationID == "" || firstResult.Status != string(control.OperationConfirmed) {
		t.Fatalf("first group.create data = %s, %v", first.Data, err)
	}
	second := callGroupCreate(t, tool, credential, "confirmed", input)
	if !second.OK || creator.calls != 1 || string(second.Data) != string(first.Data) {
		t.Fatalf("same key response/calls = %+v/%d", second, creator.calls)
	}
	if response := callGroupCreate(t, tool, credential, "confirmed", groupcapability.Input{Name: "different-team", MemberIDs: []string{"member-allowed"}}); response.OK || response.Error == nil || response.Error.Code != contracts.CodeIdempotencyConflict || creator.calls != 1 {
		t.Fatalf("conflicting key response/calls = %+v/%d", response, creator.calls)
	}
}

func TestUnknownGroupCreateSurvivesRecoveryWithoutRetryE2E(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "control.db")
	store, err := control.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	input := groupcapability.Input{Name: "team", MemberIDs: []string{"member-allowed"}}
	firstCreator := &e2eGroupCreator{err: operation.ErrOutcomeUnknown}
	tool, credential := newGroupCreateTool(t, store, firstCreator, "run-unknown", []string{"member-allowed"})
	if response := callGroupCreate(t, tool, credential, "unknown-first", input); response.OK || response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || firstCreator.calls != 1 {
		t.Fatalf("first unknown response/calls = %+v/%d", response, firstCreator.calls)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := control.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	secondCreator := &e2eGroupCreator{}
	recoveredTool, recoveredCredential := newGroupCreateTool(t, reopened, secondCreator, "run-recovered", []string{"member-allowed"})
	if response := callGroupCreate(t, recoveredTool, recoveredCredential, "unknown-new-key", input); response.OK || response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || secondCreator.calls != 0 {
		t.Fatalf("recovered unknown response/calls = %+v/%d", response, secondCreator.calls)
	}
}

func newGroupCreateTool(t *testing.T, store *control.Store, creator groupcapability.Creator, runID string, members []string) (*proxy.Proxy, string) {
	t.Helper()
	manifest, err := capability.New([]capability.Entry{{Method: groupcapability.Method, Scope: groupcapability.Scope, Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := groupcapability.New(manifest, guard, creator)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:           runID,
		ProfileID:       "work",
		Principal:       "provider",
		Methods:         []string{groupcapability.Method},
		Scopes:          []string{groupcapability.Scope},
		TargetAllowlist: append([]string(nil), members...),
		ExpiresAt:       time.Now().Add(time.Hour),
		RateBudget:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, runID, "work", []proxy.Method{handler.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func callGroupCreate(t *testing.T, tool *proxy.Proxy, credential, idempotencyKey string, input groupcapability.Input) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      "request-" + idempotencyKey,
		ProfileID:      "work",
		Method:         groupcapability.Method,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		t.Fatalf("group.create Call() error = %v", err)
	}
	return response
}

type e2eGroupCreator struct {
	calls int
	err   error
}

func (c *e2eGroupCreator) CreateGroup(context.Context, groupcapability.Input) error {
	c.calls++
	return c.err
}

var _ groupcapability.Creator = (*e2eGroupCreator)(nil)
