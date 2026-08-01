package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestSocialReadsFilterListsAndCheckIndividualTargets(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }, Capabilities: available(FriendListMethod, FriendGetMethod, FriendSearchMethod, BlackListMethod, BlackGetMethod)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{FriendListMethod, FriendGetMethod, FriendSearchMethod, BlackListMethod, BlackGetMethod}, Scopes: []string{FriendReadScope, BlackReadScope}, TargetAllowlists: map[string][]string{FriendListMethod: {grant.UserTarget("user-2")}, FriendGetMethod: {grant.UserTarget("user-2")}, FriendSearchMethod: {grant.UserTarget("user-2")}, BlackListMethod: {grant.UserTarget("user-2")}, BlackGetMethod: {grant.UserTarget("user-2")}}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 10})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	friends, err := reader.Friends(context.Background(), service.ProviderAccess(item, reader.capability(FriendListMethod)), ListInput{Limit: 10})
	if err != nil || len(friends.Data.Items) != 1 || friends.Data.Items[0].UserID != "user-2" || friends.Meta.Capability.Scope != FriendReadScope {
		t.Fatalf("Friends() = %+v, %v", friends, err)
	}
	assertMeta(t, friends.Meta, FriendListMethod)
	friend, err := reader.Friend(context.Background(), service.ProviderAccess(item, reader.capability(FriendGetMethod)), GetInput{UserID: "user-2"})
	if err != nil || friend.Data.UserID != "user-2" {
		t.Fatalf("Friend() = %+v, %v", friend, err)
	}
	assertMeta(t, friend.Meta, FriendGetMethod)
	search, err := reader.SearchFriends(context.Background(), service.ProviderAccess(item, reader.capability(FriendSearchMethod)), SearchInput{Query: "user", Limit: 10})
	if err != nil || len(search.Data.Items) != 1 || search.Data.Items[0].UserID != "user-2" {
		t.Fatalf("SearchFriends() = %+v, %v", search, err)
	}
	assertMeta(t, search.Meta, FriendSearchMethod)
	blacklist, err := reader.Blacklist(context.Background(), service.ProviderAccess(item, reader.capability(BlackListMethod)), ListInput{Limit: 10})
	if err != nil || len(blacklist.Data.Items) != 1 || blacklist.Data.Items[0].UserID != "user-2" || blacklist.Meta.Capability.Scope != BlackReadScope {
		t.Fatalf("Blacklist() = %+v, %v", blacklist, err)
	}
	assertMeta(t, blacklist.Meta, BlackListMethod)
	black, err := reader.Black(context.Background(), service.ProviderAccess(item, reader.capability(BlackGetMethod)), GetInput{UserID: "user-2"})
	if err != nil || black.Data.UserID != "user-2" {
		t.Fatalf("Black() = %+v, %v", black, err)
	}
	assertMeta(t, black.Meta, BlackGetMethod)
	if _, err := reader.Friend(context.Background(), service.ProviderAccess(item, reader.capability(FriendGetMethod)), GetInput{UserID: "user-1"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Friend() outside scope error = %v", err)
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
		entries[method] = service.Capability{Method: method, Scope: scope(method), Status: "available"}
	}
	return entries
}

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
