package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestConversationReadsUseOpaqueCursors(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	owner := service.OwnerAccess()
	first, err := reader.List(context.Background(), owner, ListInput{Limit: 2})
	if err != nil || len(first.Data.Items) != 2 || first.Data.NextCursor == "" {
		t.Fatalf("List() = %+v, %v", first, err)
	}
	second, err := reader.List(context.Background(), owner, ListInput{Limit: 2, Cursor: first.Data.NextCursor})
	if err != nil || len(second.Data.Items) != 1 || second.Data.Items[0].ID != "conversation-3" {
		t.Fatalf("second List() = %+v, %v", second, err)
	}
	if _, err := reader.Search(context.Background(), owner, SearchInput{Query: "team", Limit: 2, Cursor: first.Data.NextCursor}); !errors.Is(err, service.ErrCursorInvalid) {
		t.Fatalf("Search() foreign cursor error = %v", err)
	}
	conversation, err := reader.Get(context.Background(), owner, GetInput{ConversationID: "conversation-1"})
	if err != nil || conversation.Data.ID != "conversation-1" {
		t.Fatalf("Get() = %+v, %v", conversation, err)
	}
	search, err := reader.Search(context.Background(), owner, SearchInput{Query: "team", Limit: 2})
	if err != nil || len(search.Data.Items) != 1 || search.Data.Items[0].ID != "conversation-2" {
		t.Fatalf("Search() = %+v, %v", search, err)
	}
	if !search.Meta.Stale || search.Meta.Schema != service.SchemaVersion {
		t.Fatalf("metadata = %+v", search.Meta)
	}
}

type fakeSource struct{}

func (fakeSource) List(context.Context) ([]Conversation, error) {
	return []Conversation{{ID: "conversation-1"}, {ID: "conversation-2"}, {ID: "conversation-3"}}, nil
}
func (fakeSource) Get(_ context.Context, id string) (Conversation, error) {
	return Conversation{ID: id}, nil
}
func (fakeSource) Search(context.Context, string) ([]Conversation, error) {
	return []Conversation{{ID: "conversation-2"}}, nil
}
