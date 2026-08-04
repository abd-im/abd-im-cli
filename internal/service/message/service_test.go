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
	reader, err := New(fakeSource{}, Options{ProfileID: "work"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{HistoryMethod, SearchMethod, GetMethod}, MessageWindow: grant.MessageWindow{ConversationID: "conversation-1", AfterMessageID: "message-1", BeforeMessageID: "message-3"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 5})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	owner := service.OwnerAccess()
	first, err := reader.History(context.Background(), owner, HistoryInput{ConversationID: "conversation-1", Limit: 2})
	if err != nil || len(first.Data.Items) != 2 || first.Data.NextCursor == "" {
		t.Fatalf("owner History() = %+v, %v", first, err)
	}
	assertMeta(t, first.Meta)
	second, err := reader.History(context.Background(), owner, HistoryInput{ConversationID: "conversation-1", Limit: 2, Cursor: first.Data.NextCursor})
	if err != nil || len(second.Data.Items) != 2 || second.Data.NextCursor != "" {
		t.Fatalf("owner History() second page = %+v, %v", second, err)
	}
	assertMeta(t, second.Meta)
	if _, err := reader.Search(context.Background(), service.OwnerAccess(), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2, Cursor: first.Data.NextCursor}); !errors.Is(err, service.ErrCursorInvalid) {
		t.Fatalf("Search() foreign cursor error = %v", err)
	}
	searchFirst, err := reader.Search(context.Background(), service.OwnerAccess(), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2})
	if err != nil || len(searchFirst.Data.Items) != 2 || searchFirst.Data.NextCursor == "" {
		t.Fatalf("owner Search() = %+v, %v", searchFirst, err)
	}
	assertMeta(t, searchFirst.Meta)
	searchSecond, err := reader.Search(context.Background(), service.OwnerAccess(), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 2, Cursor: searchFirst.Data.NextCursor})
	if err != nil || len(searchSecond.Data.Items) != 2 || searchSecond.Data.NextCursor != "" {
		t.Fatalf("owner Search() second page = %+v, %v", searchSecond, err)
	}
	assertMeta(t, searchSecond.Meta)
	history, err := reader.History(context.Background(), service.ProviderAccess(item), HistoryInput{ConversationID: "conversation-1", Limit: 10})
	if err != nil || len(history.Data.Items) != 1 || history.Data.Items[0].ID != "message-2" || history.Meta.Schema != service.SchemaVersion {
		t.Fatalf("History() = %+v, %v", history, err)
	}
	assertMeta(t, history.Meta)
	search, err := reader.Search(context.Background(), service.ProviderAccess(item), SearchInput{ConversationID: "conversation-1", Query: "match", Limit: 10})
	if err != nil || len(search.Data.Items) != 1 || search.Data.Items[0].ID != "message-2" {
		t.Fatalf("Search() = %+v, %v", search, err)
	}
	assertMeta(t, search.Meta)
	got, err := reader.Get(context.Background(), service.ProviderAccess(item), GetInput{ConversationID: "conversation-1", MessageID: "message-2"})
	if err != nil || got.Data.ID != "message-2" {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	assertMeta(t, got.Meta)
	if _, err := reader.Get(context.Background(), service.ProviderAccess(item), GetInput{ConversationID: "conversation-1", MessageID: "message-4"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Get() outside window error = %v", err)
	}
	if _, err := reader.History(context.Background(), service.ProviderAccess(item), HistoryInput{ConversationID: "conversation-2", Limit: 10}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("History() outside conversation error = %v", err)
	}
}

func TestProviderMessageReadsRequireInboundWindow(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{
		RunID: "run-provider", ProfileID: "work", Principal: "provider",
		Methods: []string{HistoryMethod, GetMethod},

		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := reader.History(context.Background(), service.ProviderAccess(item), HistoryInput{ConversationID: "conversation-1", Limit: 10})
	if !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("unwindowed provider History() = %+v, %v", history, err)
	}
	if _, err := reader.Get(context.Background(), service.ProviderAccess(item), GetInput{ConversationID: "conversation-1", MessageID: "message-4"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("unwindowed provider Get() error = %v", err)
	}
}

func assertMeta(t *testing.T, meta service.Meta) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.ProfileID != "work" {
		t.Fatalf("metadata = %+v", meta)
	}
}

type fakeSource struct{}

func (fakeSource) History(context.Context, HistoryQuery) ([]Message, error) {
	return []Message{{ID: "message-1", ConversationID: "conversation-1"}, {ID: "message-2", ConversationID: "conversation-1", Text: "match"}, {ID: "message-3", ConversationID: "conversation-1"}, {ID: "message-4", ConversationID: "conversation-1"}}, nil
}
func (fakeSource) Search(context.Context, HistoryQuery, string) ([]Message, error) {
	return []Message{{ID: "message-1", ConversationID: "conversation-1"}, {ID: "message-2", ConversationID: "conversation-1", Text: "match"}, {ID: "message-3", ConversationID: "conversation-1"}, {ID: "message-4", ConversationID: "conversation-1"}}, nil
}
func (fakeSource) Get(_ context.Context, _ string, id string) (Message, error) {
	return Message{ID: id, ConversationID: "conversation-1"}, nil
}
