package blacklist

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestBlacklistActions(t *testing.T) {

	source := &fakeSource{blocked: map[string]bool{"user-1": true}}
	handler, tool, credential := newTool(t, source)
	call := func(method, key, userID string) contracts.Response {
		t.Helper()
		return callAction(t, tool, credential, method, key, Input{UserID: userID})
	}
	if handler == nil {
		t.Fatal("handler is nil")
	}
	if response := call(AddMethod, "allowed-add", "user-1"); !response.OK || source.adds != 1 || source.lastAdd != "user-1" {
		t.Fatalf("allowed blacklist.add = %+v, source=%+v", response, source)
	}
	if response := call(RemoveMethod, "allowed-remove", "user-1"); !response.OK || source.checks != 1 || source.removes != 1 || source.lastRemove != "user-1" {
		t.Fatalf("allowed blacklist.remove = %+v, source=%+v", response, source)
	}
}

func TestBlacklistRemoveVerifiesExistingRelationshipBeforeMutation(t *testing.T) {

	source := &fakeSource{blocked: map[string]bool{}}
	_, tool, credential := newTool(t, source)
	if response := callAction(t, tool, credential, RemoveMethod, "missing", Input{UserID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || source.checks != 1 || source.removes != 0 {
		t.Fatalf("missing blacklist.remove = %+v, source=%+v", response, source)
	}
	source.checkErr = errors.New("source unavailable")
	if response := callAction(t, tool, credential, RemoveMethod, "read-error", Input{UserID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodeSDKError || source.removes != 0 {
		t.Fatalf("failed relationship validation = %+v, source=%+v", response, source)
	}
}

func TestBlacklistActionsPreserveIdempotencyAndUnknownOutcomes(t *testing.T) {

	source := &fakeSource{blocked: map[string]bool{"user-1": true}}
	_, tool, credential := newTool(t, source)
	input := Input{UserID: "user-1"}
	first := callAction(t, tool, credential, AddMethod, "same", input)
	if !first.OK || source.adds != 1 {
		t.Fatalf("first blacklist.add = %+v, source=%+v", first, source)
	}
	if repeated := callAction(t, tool, credential, AddMethod, "same", input); !repeated.OK || source.adds != 1 || string(repeated.Data) != string(first.Data) {
		t.Fatalf("repeated blacklist.add = %+v, first=%+v, source=%+v", repeated, first, source)
	}

	source.removeErr = operation.ErrOutcomeUnknown
	if unknown := callAction(t, tool, credential, RemoveMethod, "unknown", input); unknown.Error == nil || unknown.Error.Code != contracts.CodeOutcomeUnknown || source.removes != 1 {
		t.Fatalf("unknown blacklist.remove = %+v, source=%+v", unknown, source)
	}
	if retry := callAction(t, tool, credential, RemoveMethod, "new-key", input); retry.Error == nil || retry.Error.Code != contracts.CodeOutcomeUnknown || source.removes != 1 {
		t.Fatalf("rebuilt blacklist.remove = %+v, source=%+v", retry, source)
	}
}

func TestParseRejectsInvalidUserID(t *testing.T) {
	for _, input := range []Input{
		{},
		{UserID: "  "},
		{UserID: strings.Repeat("a", maxUserIDBytes+1)},
	} {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parse(raw); err == nil {
			t.Fatalf("parse(%+v) succeeded", input)
		}
	}
}

func TestNewRequiresAllDependencies(t *testing.T) {
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, &fakeSource{}); err == nil {
		t.Fatal("New(nil guard) error = nil")
	}
	if _, err := New(guard, nil); err == nil {
		t.Fatal("New(nil source) error = nil")
	}
}

func newTool(t *testing.T, source *fakeSource) (*Handler, *proxy.Proxy, string) {
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
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{AddMethod, RemoveMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-1", "work", handler.ProxyMethods())
	if err != nil {
		t.Fatal(err)
	}
	return handler, tool, credential
}

func callAction(t *testing.T, tool *proxy.Proxy, credential, method, key string, input Input) contracts.Response {
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
	blocked    map[string]bool
	checks     int
	adds       int
	removes    int
	lastAdd    string
	lastRemove string
	checkErr   error
	addErr     error
	removeErr  error
}

func (s *fakeSource) AddBlacklist(_ context.Context, userID string) error {
	s.adds++
	s.lastAdd = userID
	return s.addErr
}

func (s *fakeSource) RemoveBlacklist(_ context.Context, userID string) error {
	s.removes++
	s.lastRemove = userID
	return s.removeErr
}

func (s *fakeSource) IsBlacklisted(_ context.Context, userID string) (bool, error) {
	s.checks++
	if s.checkErr != nil {
		return false, s.checkErr
	}
	return s.blocked[userID], nil
}

var _ Source = (*fakeSource)(nil)
