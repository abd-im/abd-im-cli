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

func TestSendAt(t *testing.T) {

	sender := &fakeAtSender{}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	defer store.Close()
	guard, _ := operation.NewGuard(store)
	handler, _ := NewAt(guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{AtMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 5,
	})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	call := func(key string, input AtInput) contracts.Response {
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion:     contracts.APIVersionV1,
			RequestID:      key,
			ProfileID:      "work",
			Method:         AtMethod,
			Params:         raw,
			Grant:          credential,
			IdempotencyKey: key,
		})
		return response
	}
	allowed := AtInput{Text: "hello", GroupID: "group-1", MentionUserIDs: []string{"user-1", "user-2"}}
	if response := call("allowed", allowed); !response.OK || sender.calls != 1 || sender.groupID != "group-1" || sender.text != "hello" || len(sender.mentionUserIDs) != 2 || sender.mentionUserIDs[0] != "user-1" || sender.mentionUserIDs[1] != "user-2" {
		t.Fatalf("allowed send = %+v, sender=%+v", response, sender)
	}
}

func TestSendAtIdempotencyConflictReturnsOriginalOperation(t *testing.T) {
	tool, credential, sender := newAtTool(t, nil)
	call := func(key string, input AtInput) contracts.Response {
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion:     contracts.APIVersionV1,
			RequestID:      key,
			ProfileID:      "work",
			Method:         AtMethod,
			Params:         raw,
			Grant:          credential,
			IdempotencyKey: key,
		})
		return response
	}
	input := AtInput{Text: "hello", GroupID: "group-1", MentionUserIDs: []string{"user-1"}}
	first := call("same-key", input)
	if !first.OK || sender.calls != 1 {
		t.Fatalf("first send = %+v, calls=%d", first, sender.calls)
	}
	repeated := call("same-key", input)
	if !repeated.OK || sender.calls != 1 || string(first.Data) != string(repeated.Data) {
		t.Fatalf("repeated send = %+v, first=%+v, calls=%d", repeated, first, sender.calls)
	}
	if response := call("same-key", AtInput{Text: "different", GroupID: "group-1", MentionUserIDs: []string{"user-1"}}); response.Error == nil || response.Error.Code != contracts.CodeIdempotencyConflict || sender.calls != 1 {
		t.Fatalf("conflicting send = %+v, calls=%d", response, sender.calls)
	}
}

func TestUnknownSendAtCannotBeRebuiltWithNewKey(t *testing.T) {
	tool, credential, sender := newAtTool(t, operation.ErrOutcomeUnknown)
	raw, _ := json.Marshal(AtInput{Text: "hello", GroupID: "group-1", MentionUserIDs: []string{"user-1"}})
	call := func(key string) contracts.Response {
		response, _ := tool.Call(context.Background(), contracts.Request{
			APIVersion:     contracts.APIVersionV1,
			RequestID:      key,
			ProfileID:      "work",
			Method:         AtMethod,
			Params:         raw,
			Grant:          credential,
			IdempotencyKey: key,
		})
		return response
	}
	if response := call("first"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown {
		t.Fatalf("first unknown = %+v", response)
	}
	if response := call("new-key"); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 1 {
		t.Fatalf("new key rebuild = %+v, calls=%d", response, sender.calls)
	}
}

func TestParseAtRejectsInvalidTargetsAndMentions(t *testing.T) {
	for _, input := range []AtInput{
		{Text: strings.Repeat("a", maxTextBytes+1), GroupID: "group-1", MentionUserIDs: []string{"user-1"}},
		{Text: "hello", MentionUserIDs: []string{"user-1"}},
		{Text: "hello", GroupID: "group-1"},
		{Text: "hello", GroupID: "group-1", MentionUserIDs: []string{"user-1", "user-1"}},
		{Text: "hello", GroupID: "group-1", MentionUserIDs: make([]string, maxAtMentions+1)},
	} {
		raw, _ := json.Marshal(input)
		if _, err := parseAt(raw); err == nil {
			t.Fatalf("parseAt(%+v) succeeded", input)
		}
	}
}

func newAtTool(t *testing.T, sendErr error) (*proxy.Proxy, string, *fakeAtSender) {
	t.Helper()

	sender := &fakeAtSender{err: sendErr}
	store, _ := control.Open(filepath.Join(t.TempDir(), "control.db"))
	t.Cleanup(func() { _ = store.Close() })
	guard, _ := operation.NewGuard(store)
	handler, _ := NewAt(guard, sender)
	grants := grant.NewStore()
	_, credential, _ := grants.Issue(grant.Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{AtMethod},

		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 5,
	})
	tool, _ := proxy.New(grants, "run-1", "work", []proxy.Method{handler.ProxyMethod()})
	return tool, credential, sender
}

type fakeAtSender struct {
	calls          int
	text           string
	groupID        string
	mentionUserIDs []string
	err            error
}

func (s *fakeAtSender) SendAt(_ context.Context, text, groupID string, mentionUserIDs []string) error {
	s.calls++
	s.text = text
	s.groupID = groupID
	s.mentionUserIDs = append([]string(nil), mentionUserIDs...)
	return s.err
}

var _ AtSender = (*fakeAtSender)(nil)
