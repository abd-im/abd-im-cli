package social

import (
	"context"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestSocialReads(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	owner := service.OwnerAccess()
	friends, err := reader.Friends(context.Background(), owner, ListInput{Limit: 10})
	if err != nil || len(friends.Data.Items) != 2 || friends.Data.Items[0].UserID != "user-1" {
		t.Fatalf("Friends() = %+v, %v", friends, err)
	}
	friend, err := reader.Friend(context.Background(), owner, GetInput{UserID: "user-2"})
	if err != nil || friend.Data.UserID != "user-2" {
		t.Fatalf("Friend() = %+v, %v", friend, err)
	}
	search, err := reader.SearchFriends(context.Background(), owner, SearchInput{Query: "user", Limit: 10})
	if err != nil || len(search.Data.Items) != 1 {
		t.Fatalf("SearchFriends() = %+v, %v", search, err)
	}
	blacklist, err := reader.Blacklist(context.Background(), owner, ListInput{Limit: 10})
	if err != nil || len(blacklist.Data.Items) != 2 {
		t.Fatalf("Blacklist() = %+v, %v", blacklist, err)
	}
	black, err := reader.Black(context.Background(), owner, GetInput{UserID: "user-2"})
	if err != nil || black.Data.UserID != "user-2" {
		t.Fatalf("Black() = %+v, %v", black, err)
	}
	if !black.Meta.Stale || black.Meta.Schema != service.SchemaVersion {
		t.Fatalf("metadata = %+v", black.Meta)
	}
}

type fakeSource struct{}

func (fakeSource) Friends(context.Context) ([]Friend, error) {
	return []Friend{{UserID: "user-1"}, {UserID: "user-2"}}, nil
}
func (fakeSource) Friend(_ context.Context, id string) (Friend, error) {
	return Friend{UserID: id}, nil
}
func (fakeSource) SearchFriends(context.Context, string) ([]Friend, error) {
	return []Friend{{UserID: "user-2"}}, nil
}
func (fakeSource) Blacklist(context.Context) ([]BlacklistEntry, error) {
	return []BlacklistEntry{{UserID: "user-1"}, {UserID: "user-2"}}, nil
}
func (fakeSource) Black(_ context.Context, id string) (BlacklistEntry, error) {
	return BlacklistEntry{UserID: id}, nil
}
