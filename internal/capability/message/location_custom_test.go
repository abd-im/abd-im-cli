package message

import (
	"context"
	"encoding/json"
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

func TestSendLocation(t *testing.T) {

	sender := &fakeLocationSender{}
	guard := newMessageGuard(t)
	handler, _ := NewLocation(guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{LocationMethod},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3,
	})
	call := func(key string, input LocationInput) contracts.Response {
		raw, _ := json.Marshal(input)
		tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: LocationMethod, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	allowed := LocationInput{Description: "office", Longitude: 120.1, Latitude: 30.2, RecipientID: "user-1"}
	if response := call("allowed", allowed); !response.OK || sender.calls != 1 || sender.description != "office" || sender.recipientID != "user-1" {
		t.Fatalf("allowed location = %+v, sender=%+v", response, sender)
	}
}

func TestSendCustomBoundsPayloadAndFailsClosedOnUnknownOutcome(t *testing.T) {

	sender := &fakeCustomSender{err: operation.ErrOutcomeUnknown}
	guard := newMessageGuard(t)
	handler, _ := NewCustom(guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{CustomMethod},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3,
	})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key string, input CustomInput) contracts.Response {
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: CustomMethod, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("too-large", CustomInput{Data: strings.Repeat("x", maxCustomDataBytes+1), GroupID: "group-1"}); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("too-large custom = %+v", response)
	}
	input := CustomInput{Data: "opaque", Extension: "v1", Description: "description", GroupID: "group-1"}
	if response := call("unknown", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown {
		t.Fatalf("unknown custom = %+v", response)
	}
	if response := call("new-key", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 1 {
		t.Fatalf("new key custom = %+v, calls=%d", response, sender.calls)
	}
}

func TestParseLocationRejectsInvalidCoordinatesAndTargets(t *testing.T) {
	for _, input := range []LocationInput{
		{Longitude: 181, Latitude: 0, RecipientID: "user-1"},
		{Longitude: 0, Latitude: 91, RecipientID: "user-1"},
		{Longitude: 0, Latitude: 0},
		{Longitude: 0, Latitude: 0, RecipientID: "user-1", GroupID: "group-1"},
		{Description: strings.Repeat("x", maxLocationDescriptionBytes+1), Longitude: 0, Latitude: 0, RecipientID: "user-1"},
	} {
		raw, _ := json.Marshal(input)
		if _, err := parseLocation(raw); err == nil {
			t.Fatalf("parseLocation(%+v) succeeded", input)
		}
	}
}

func newMessageGuard(t *testing.T) *operation.Guard {
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
	return guard
}

type fakeLocationSender struct {
	calls       int
	description string
	recipientID string
}

func (s *fakeLocationSender) SendLocation(_ context.Context, description string, _ float64, _ float64, recipientID, _ string) error {
	s.calls++
	s.description = description
	s.recipientID = recipientID
	return nil
}

type fakeCustomSender struct {
	calls int
	err   error
}

func (s *fakeCustomSender) SendCustom(_ context.Context, _, _, _, _, _ string) error {
	s.calls++
	return s.err
}

var _ LocationSender = (*fakeLocationSender)(nil)
var _ CustomSender = (*fakeCustomSender)(nil)
