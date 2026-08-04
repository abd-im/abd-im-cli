package conversation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func TestConversationSettings(t *testing.T) {

	sender := &fakeSettingsSender{}
	pinned, err := NewSetPinned(newSettingsGuard(t), sender)
	if err != nil {
		t.Fatal(err)
	}
	receive, err := NewSetReceiveOption(newSettingsGuard(t), sender)
	if err != nil {
		t.Fatal(err)
	}
	tool, credential := newSettingsTool(t, pinned, receive, "run-1")

	if response := callSetting(t, tool, credential, "pinned", SetPinnedMethod, SetPinnedInput{ConversationID: "conversation-1", Pinned: true}); !response.OK || sender.pinnedCalls != 1 {
		t.Fatalf("pinned setting = %+v, calls=%d", response, sender.pinnedCalls)
	}
	if response := callSetting(t, tool, credential, "receive-allowed", SetReceiveOptionMethod, SetReceiveOptionInput{ConversationID: "conversation-1", Option: ReceiveOptionReceiveNoNotify}); !response.OK || sender.optionCalls != 1 || sender.option != (SetReceiveOptionInput{ConversationID: "conversation-1", Option: ReceiveOptionReceiveNoNotify}) {
		t.Fatalf("allowed receive setting = %+v, sender=%+v", response, sender)
	}
}

func TestConversationSettingsIdempotencyAndUnknownOutcomeFailClosed(t *testing.T) {

	sender := &fakeSettingsSender{}
	pinned, _ := NewSetPinned(newSettingsGuard(t), sender)
	receive, _ := NewSetReceiveOption(newSettingsGuard(t), sender)
	tool, credential := newSettingsTool(t, pinned, receive, "run-1")

	first := callSetting(t, tool, credential, "same", SetPinnedMethod, SetPinnedInput{ConversationID: "conversation-1", Pinned: true})
	if !first.OK || sender.pinnedCalls != 1 {
		t.Fatalf("first setting = %+v, calls=%d", first, sender.pinnedCalls)
	}
	second := callSetting(t, tool, credential, "same", SetPinnedMethod, SetPinnedInput{ConversationID: "conversation-1", Pinned: true})
	if !second.OK || string(first.Data) != string(second.Data) || sender.pinnedCalls != 1 {
		t.Fatalf("repeated setting = %+v, calls=%d", second, sender.pinnedCalls)
	}
	if response := callSetting(t, tool, credential, "same", SetPinnedMethod, SetPinnedInput{ConversationID: "conversation-1", Pinned: false}); response.Error == nil || response.Error.Code != contracts.CodeIdempotencyConflict || sender.pinnedCalls != 1 {
		t.Fatalf("conflicting setting = %+v, calls=%d", response, sender.pinnedCalls)
	}

	sender.optionErr = operation.ErrOutcomeUnknown
	input := SetReceiveOptionInput{ConversationID: "conversation-1", Option: ReceiveOptionDoNotReceive}
	if response := callSetting(t, tool, credential, "unknown", SetReceiveOptionMethod, input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.optionCalls != 1 {
		t.Fatalf("unknown setting = %+v, calls=%d", response, sender.optionCalls)
	}
	if response := callSetting(t, tool, credential, "new-key", SetReceiveOptionMethod, input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.optionCalls != 1 {
		t.Fatalf("unknown setting rebuilt = %+v, calls=%d", response, sender.optionCalls)
	}
}

func TestConversationSettingsRejectInvalidParameters(t *testing.T) {
	for _, raw := range []string{
		`{"conversation_id":""}`,
		`{"conversation_id":"conversation-1","option":"arbitrary_patch"}`,
		`{"conversation_id":"conversation-1","option":1}`,
		`{"conversation_id":"conversation-1","option":"receive","is_private_chat":true}`,
	} {
		if _, err := parseSetReceiveOption(json.RawMessage(raw)); err == nil {
			t.Fatalf("parseSetReceiveOption(%s) succeeded", raw)
		}
	}
	for _, raw := range []string{
		`{"conversation_id":""}`,
		`{"conversation_id":"conversation-1"}`,
		`{"conversation_id":"conversation-1","pinned":true,"is_private_chat":true}`,
	} {
		if _, err := parseSetPinned(json.RawMessage(raw)); err == nil {
			t.Fatalf("parseSetPinned(%s) succeeded", raw)
		}
	}
}

func newSettingsGuard(t *testing.T) *operation.Guard {
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

func newSettingsTool(t *testing.T, pinned *SetPinnedHandler, receive *SetReceiveOptionHandler, runID string) (*proxy.Proxy, string) {
	t.Helper()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:     runID,
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{SetPinnedMethod, SetReceiveOptionMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, runID, "work", []proxy.Method{pinned.ProxyMethod(), receive.ProxyMethod()})
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

func callSetting(t *testing.T, tool *proxy.Proxy, credential, key, method string, input any) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: method,
		Params: params, Grant: credential, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type fakeSettingsSender struct {
	pinnedCalls int
	optionCalls int
	pinned      SetPinnedInput
	option      SetReceiveOptionInput
	pinnedErr   error
	optionErr   error
}

func (s *fakeSettingsSender) SetPinned(_ context.Context, input SetPinnedInput) error {
	s.pinnedCalls++
	s.pinned = input
	return s.pinnedErr
}

func (s *fakeSettingsSender) SetReceiveOption(_ context.Context, input SetReceiveOptionInput) error {
	s.optionCalls++
	s.option = input
	return s.optionErr
}

var _ SettingsSender = (*fakeSettingsSender)(nil)
