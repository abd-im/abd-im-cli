package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestMessageReadsAreBoundToGrantConversationAndWindow(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Capabilities: available(HistoryMethod, SearchMethod, GetMethod)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{HistoryMethod, SearchMethod, GetMethod}, Scopes: []string{ReadScope}, TargetAllowlists: map[string][]string{HistoryMethod: {grant.ConversationTarget("conversation-1")}, SearchMethod: {grant.ConversationTarget("conversation-1")}, GetMethod: {grant.ConversationTarget("conversation-1")}}, MessageWindow: grant.MessageWindow{ConversationID: "conversation-1", AfterMessageID: "message-1", BeforeMessageID: "message-3"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 5})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	owner := service.OwnerAccess(reader.capability(HistoryMethod))
	first, err := reader.History(context.Background(), owner, HistoryInput{ConversationID: "conversation-1", Limit: 2})
	if err != nil || len(first.Data.Items) != 2 || first.Data.NextCursor == "" {
		t.Fatalf("owner History() = %+v, %v", first, err)
	}
	assertMeta(t, first.Meta, HistoryMethod)
	second, err := reader.History(context.Background(), owner, HistoryInput{ConversationID: "conversation-1", Limit: 2, Cursor: first.Data.NextCursor})
	if err != nil || len(second.Data.Items) != 2 || second.Data.NextCursor != "" {
		t.Fatalf("owner History() second page = %+v, %v", second, err)
	}
	assertMeta(t, second.Meta, HistoryMethod)
	if _, err := reader.Search(context.Background(), service.OwnerAccess(reader.capability(SearchMethod)), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2, Cursor: first.Data.NextCursor}); !errors.Is(err, service.ErrCursorInvalid) {
		t.Fatalf("Search() foreign cursor error = %v", err)
	}
	searchFirst, err := reader.Search(context.Background(), service.OwnerAccess(reader.capability(SearchMethod)), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2})
	if err != nil || len(searchFirst.Data.Items) != 2 || searchFirst.Data.NextCursor == "" {
		t.Fatalf("owner Search() = %+v, %v", searchFirst, err)
	}
	assertMeta(t, searchFirst.Meta, SearchMethod)
	searchSecond, err := reader.Search(context.Background(), service.OwnerAccess(reader.capability(SearchMethod)), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2, Cursor: searchFirst.Data.NextCursor})
	if err != nil || len(searchSecond.Data.Items) != 2 || searchSecond.Data.NextCursor != "" {
		t.Fatalf("owner Search() second page = %+v, %v", searchSecond, err)
	}
	assertMeta(t, searchSecond.Meta, SearchMethod)
	history, err := reader.History(context.Background(), service.ProviderAccess(item, reader.capability(HistoryMethod)), HistoryInput{ConversationID: "conversation-1", Limit: 10})
	if err != nil || len(history.Data.Items) != 1 || history.Data.Items[0].ID != "message-2" || history.Meta.Schema != service.SchemaVersion {
		t.Fatalf("History() = %+v, %v", history, err)
	}
	assertMeta(t, history.Meta, HistoryMethod)
	search, err := reader.Search(context.Background(), service.ProviderAccess(item, reader.capability(SearchMethod)), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 10})
	if err != nil || len(search.Data.Items) != 1 || search.Data.Items[0].ID != "message-2" {
		t.Fatalf("Search() = %+v, %v", search, err)
	}
	assertMeta(t, search.Meta, SearchMethod)
	got, err := reader.Get(context.Background(), service.ProviderAccess(item, reader.capability(GetMethod)), GetInput{ConversationID: "conversation-1", MessageID: "message-2"})
	if err != nil || got.Data.ID != "message-2" {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	assertMeta(t, got.Meta, GetMethod)
	if _, err := reader.Get(context.Background(), service.ProviderAccess(item, reader.capability(GetMethod)), GetInput{ConversationID: "conversation-1", MessageID: "message-4"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Get() outside window error = %v", err)
	}
	if _, err := reader.History(context.Background(), service.ProviderAccess(item, reader.capability(HistoryMethod)), HistoryInput{ConversationID: "conversation-2", Limit: 10}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("History() outside conversation error = %v", err)
	}
}

func assertMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Capability.Method != method || meta.Capability.Status != "available" {
		t.Fatalf("metadata = %+v, want verified %s metadata", meta, method)
	}
}

type fakeSource struct{}

func available(methods ...string) map[string]service.Capability {
	entries := make(map[string]service.Capability, len(methods))
	for _, method := range methods {
		entries[method] = service.Capability{Method: method, Scope: ReadScope, Status: "available"}
	}
	return entries
}

func (fakeSource) History(context.Context, HistoryQuery) ([]Message, error) {
	return []Message{{ID: "message-1", ConversationID: "conversation-1"}, {ID: "message-2", ConversationID: "conversation-1", Text: "match"}, {ID: "message-3", ConversationID: "conversation-1"}, {ID: "message-4", ConversationID: "conversation-1"}}, nil
}
func (fakeSource) Search(context.Context, HistoryQuery, string) ([]Message, error) {
	return []Message{{ID: "message-1", ConversationID: "conversation-1"}, {ID: "message-2", ConversationID: "conversation-1", Text: "match"}, {ID: "message-3", ConversationID: "conversation-1"}, {ID: "message-4", ConversationID: "conversation-1"}}, nil
}
func (fakeSource) Get(_ context.Context, _ string, id string) (Message, error) {
	return Message{ID: id, ConversationID: "conversation-1"}, nil
}
