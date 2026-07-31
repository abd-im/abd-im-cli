package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestListUsesOpaqueCursorAndFiltersProviderTargets(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }, Capabilities: available(ListMethod, GetMethod, SearchMethod, UnreadMethod)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner := service.OwnerAccess(reader.capability(ListMethod))
	first, err := reader.List(context.Background(), owner, ListInput{Limit: 2})
	if err != nil || len(first.Data.Items) != 2 || first.Data.NextCursor == "" {
		t.Fatalf("List() = %+v, %v", first, err)
	}
	assertMeta(t, first.Meta, ListMethod)
	second, err := reader.List(context.Background(), owner, ListInput{Limit: 2, Cursor: first.Data.NextCursor})
	if err != nil || len(second.Data.Items) != 1 || second.Data.Items[0].ID != "conversation-3" {
		t.Fatalf("second List() = %+v, %v", second, err)
	}
	assertMeta(t, second.Meta, ListMethod)
	if _, err := reader.Search(context.Background(), owner, SearchInput{Query: "team", Limit: 2, Cursor: first.Data.NextCursor}); !errors.Is(err, service.ErrCursorInvalid) {
		t.Fatalf("Search() foreign cursor error = %v", err)
	}
	conversation, err := reader.Get(context.Background(), service.OwnerAccess(reader.capability(GetMethod)), GetInput{ConversationID: "conversation-1"})
	if err != nil || conversation.Data.ID != "conversation-1" {
		t.Fatalf("Get() = %+v, %v", conversation, err)
	}
	assertMeta(t, conversation.Meta, GetMethod)
	search, err := reader.Search(context.Background(), service.OwnerAccess(reader.capability(SearchMethod)), SearchInput{Query: "team", Limit: 2})
	if err != nil || len(search.Data.Items) != 1 || search.Data.Items[0].ID != "conversation-2" {
		t.Fatalf("Search() = %+v, %v", search, err)
	}
	assertMeta(t, search.Meta, SearchMethod)
	unread, err := reader.Unread(context.Background(), service.OwnerAccess(reader.capability(UnreadMethod)))
	if err != nil || unread.Data != 2 {
		t.Fatalf("Unread() = %+v, %v", unread, err)
	}
	assertMeta(t, unread.Meta, UnreadMethod)

	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{ListMethod, GetMethod}, Scopes: []string{ReadScope}, TargetAllowlist: []string{"conversation-2"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	limited, err := reader.List(context.Background(), service.ProviderAccess(item, reader.capability(ListMethod)), ListInput{Limit: 10})
	if err != nil || len(limited.Data.Items) != 1 || limited.Data.Items[0].ID != "conversation-2" {
		t.Fatalf("limited List() = %+v, %v", limited, err)
	}
	if _, err := reader.Get(context.Background(), service.ProviderAccess(item, reader.capability(GetMethod)), GetInput{ConversationID: "conversation-1"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Get() outside grant error = %v", err)
	}
}

func assertMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if !meta.Stale || meta.Schema != service.SchemaVersion || meta.Capability.Method != method || meta.Capability.Status != "available" {
		t.Fatalf("metadata = %+v, want stale verified %s metadata", meta, method)
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

func (fakeSource) List(context.Context) ([]Conversation, error) {
	return []Conversation{{ID: "conversation-1"}, {ID: "conversation-2"}, {ID: "conversation-3"}}, nil
}
func (fakeSource) Get(_ context.Context, id string) (Conversation, error) {
	return Conversation{ID: id}, nil
}
func (fakeSource) Search(context.Context, string) ([]Conversation, error) {
	return []Conversation{{ID: "conversation-2"}}, nil
}
func (fakeSource) Unread(context.Context) (int, error) { return 2, nil }
