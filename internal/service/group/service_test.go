package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/service"
)

// The service is exercised through its source boundary; OpenIM API mapping is
// covered separately so this test needs no local SDK database.
func TestGroupAndMemberReadsExposeCapabilityAndRespectScope(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }, Capabilities: available(MembersListMethod)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner := service.OwnerAccess(reader.capability(MembersListMethod))
	members, err := reader.Members(context.Background(), owner, MembersInput{GroupID: "group-1", Limit: 10})
	if err != nil || len(members.Data.Items) != 2 || members.Meta.Capability.Status != "available" || !members.Meta.Stale {
		t.Fatalf("Members() = %+v, %v", members, err)
	}

	grants := grant.NewStore()
	item, _, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{MembersListMethod}, Scopes: []string{ReadScope}, TargetAllowlists: map[string][]string{MembersListMethod: {grant.GroupTarget("group-1")}}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := reader.Members(context.Background(), service.ProviderAccess(item, reader.capability(MembersListMethod)), MembersInput{GroupID: "group-2", Limit: 10}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Members() outside scope error = %v", err)
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

func (fakeSource) List(context.Context) ([]Group, error)           { return []Group{{ID: "group-1"}}, nil }
func (fakeSource) Get(_ context.Context, id string) (Group, error) { return Group{ID: id}, nil }
func (fakeSource) Search(context.Context, string) ([]Group, error) {
	return []Group{{ID: "group-1"}}, nil
}
func (fakeSource) Members(_ context.Context, groupID string) ([]Member, error) {
	return []Member{{GroupID: groupID, UserID: "user-1"}, {GroupID: groupID, UserID: "user-2"}}, nil
}
func (fakeSource) SearchMembers(_ context.Context, groupID, _ string) ([]Member, error) {
	return []Member{{GroupID: groupID, UserID: "user-1"}}, nil
}
