package e2e

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service/message"
)

func TestProviderGrantBoundMessageReadsE2E(t *testing.T) {
	source := &grantBoundMessageSource{}
	reader, err := message.New(source, message.Options{
		ProfileID:    "work",
		Capabilities: message.VerifiedCapabilities("sdk-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	methods := reader.Methods()
	method := make(map[string]proxy.Method, len(methods))
	for _, item := range methods {
		method[item.Name] = item
	}

	grants := grant.NewStore()
	_, historyCredential, err := grants.Issue(messageGrant("run-history", []string{message.HistoryMethod}, 3))
	if err != nil {
		t.Fatal(err)
	}
	historyProxy, err := proxy.New(grants, "run-history", "work", []proxy.Method{method[message.HistoryMethod]})
	if err != nil {
		t.Fatal(err)
	}

	history := callMessageTool(t, historyProxy, historyCredential, message.HistoryMethod, message.HistoryInput{ConversationID: "conversation-1", Limit: 10})
	assertMessageIDs(t, history.Data, "message-allowed-1", "message-allowed-2")
	if response := callMessageTool(t, historyProxy, historyCredential, message.SearchMethod, message.SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 10}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
		t.Fatalf("unselected message.search = %+v", response)
	}
	if response := callMessageTool(t, historyProxy, historyCredential, message.HistoryMethod, message.HistoryInput{ConversationID: "conversation-other", Limit: 10}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
		t.Fatalf("foreign conversation history = %+v", response)
	}
	if got := source.historyConversations; !reflect.DeepEqual(got, []string{"conversation-1"}) {
		t.Fatalf("history source conversations = %v, want only allowed target", got)
	}

	_, allCredential, err := grants.Issue(messageGrant("run-all", []string{message.HistoryMethod, message.SearchMethod, message.GetMethod}, 6))
	if err != nil {
		t.Fatal(err)
	}
	allProxy, err := proxy.New(grants, "run-all", "work", methods)
	if err != nil {
		t.Fatal(err)
	}
	search := callMessageTool(t, allProxy, allCredential, message.SearchMethod, message.SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 10})
	assertMessageIDs(t, search.Data, "message-allowed-1", "message-allowed-2")
	allowedGet := callMessageTool(t, allProxy, allCredential, message.GetMethod, message.GetInput{ConversationID: "conversation-1", MessageID: "message-allowed-1"})
	assertSingleMessageID(t, allowedGet.Data, "message-allowed-1")
	if response := callMessageTool(t, allProxy, allCredential, message.GetMethod, message.GetInput{ConversationID: "conversation-1", MessageID: "message-before"}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
		t.Fatalf("message before grant window = %+v", response)
	}
	if response := callMessageTool(t, allProxy, allCredential, message.GetMethod, message.GetInput{ConversationID: "conversation-1", MessageID: "message-after"}); response.OK || response.Error == nil || response.Error.Code != contracts.CodePolicyDenied {
		t.Fatalf("message after grant window = %+v", response)
	}
}

func messageGrant(runID string, methods []string, rateBudget int) grant.Policy {
	return grant.Policy{
		RunID:           runID,
		ProfileID:       "work",
		Principal:       "provider",
		Methods:         methods,
		Scopes:          []string{message.ReadScope},
		TargetAllowlist: []string{"conversation-1"},
		MessageWindow: grant.MessageWindow{
			ConversationID:  "conversation-1",
			AfterMessageID:  "message-trigger",
			BeforeMessageID: "message-stop",
		},
		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: rateBudget,
	}
}

func callMessageTool(t *testing.T, tool *proxy.Proxy, credential, method string, input any) contracts.Response {
	t.Helper()
	params, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tool.Call(context.Background(), contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "request-" + method,
		ProfileID:  "work",
		Method:     method,
		Params:     params,
		Grant:      credential,
	})
	if err != nil {
		t.Fatalf("%s Call() error = %v", method, err)
	}
	return response
}

func assertMessageIDs(t *testing.T, raw json.RawMessage, want ...string) {
	t.Helper()
	var page struct {
		Items []message.Message `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode message page: %v", err)
	}
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message IDs = %v, want %v", got, want)
	}
}

func assertSingleMessageID(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var item message.Message
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if item.ID != want {
		t.Fatalf("message ID = %q, want %q", item.ID, want)
	}
}

type grantBoundMessageSource struct {
	historyConversations []string
}

func (s *grantBoundMessageSource) History(_ context.Context, query message.HistoryQuery) ([]message.Message, error) {
	s.historyConversations = append(s.historyConversations, query.ConversationID)
	return grantBoundMessages(query.ConversationID), nil
}

func (s *grantBoundMessageSource) Search(_ context.Context, query message.HistoryQuery, _ string) ([]message.Message, error) {
	return grantBoundMessages(query.ConversationID), nil
}

func (s *grantBoundMessageSource) Get(_ context.Context, conversationID, messageID string) (message.Message, error) {
	for _, item := range grantBoundMessages(conversationID) {
		if item.ID == messageID {
			return item, nil
		}
	}
	return message.Message{ID: messageID, ConversationID: conversationID}, nil
}

func grantBoundMessages(conversationID string) []message.Message {
	return []message.Message{
		{ID: "message-before", ConversationID: conversationID},
		{ID: "message-trigger", ConversationID: conversationID},
		{ID: "message-allowed-1", ConversationID: conversationID, Text: "match"},
		{ID: "message-allowed-2", ConversationID: conversationID, Text: "match"},
		{ID: "message-stop", ConversationID: conversationID},
		{ID: "message-after", ConversationID: conversationID},
	}
}
