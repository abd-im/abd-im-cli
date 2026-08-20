package group

import (
	"context"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestGroupAndMemberReads(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	members, err := reader.Members(context.Background(), MembersInput{GroupID: "group-1", Limit: 10})
	if err != nil || len(members.Data.Items) != 2 || !members.Meta.Stale || members.Meta.Schema != service.SchemaVersion {
		t.Fatalf("Members() = %+v, %v", members, err)
	}
	group, err := reader.Get(context.Background(), GetInput{GroupID: "group-1"})
	if err != nil || group.Data.ID != "group-1" {
		t.Fatalf("Get() = %+v, %v", group, err)
	}
}

type fakeSource struct{}

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
