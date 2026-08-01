//go:build integration

package group

import (
	"context"
	"fmt"
	"testing"
	"time"

	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
)

// TestOpenIMGroupAdministrationIntegration creates a disposable working
// group and verifies every fixed administration action with two controlled
// accounts. The final ownership transfer intentionally leaves the group
// available for ordinary server retention cleanup.
func TestOpenIMGroupAdministrationIntegration(t *testing.T) {
	apiAddr := membershipIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	ownerID := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_ID")
	ownerToken := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_TOKEN")
	memberID := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_ID")
	if ownerID == memberID {
		t.Fatal("group administration fixture requires two different users")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	ownerContext := integrationGroupContext(apiAddr, ownerID, ownerToken)
	creator, err := NewOpenIMCreator(OpenIMCreator{Context: ownerContext})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abdim-administration-%d", time.Now().UnixNano())
	if err := creator.CreateGroup(ctx, Input{Name: name, MemberIDs: []string{memberID}}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	groupID, err := waitForIntegrationGroup(ctx, groupservice.OpenIMClient{Context: ownerContext}, name)
	if err != nil {
		t.Fatal(err)
	}

	owner, err := NewOpenIMAdministrationSource(OpenIMAdministrationSource{Context: ownerContext})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := owner.CanSetInfo(ctx, groupID); err != nil || !allowed {
		t.Fatalf("CanSetInfo() = %t, %v", allowed, err)
	}
	updatedName := name + "-updated"
	if err := owner.SetInfo(ctx, GroupInfoUpdate{GroupID: groupID, Name: &updatedName}); err != nil {
		t.Fatalf("SetInfo() error = %v", err)
	}
	if allowed, err := owner.CanSetMute(ctx, groupID); err != nil || !allowed {
		t.Fatalf("CanSetMute() = %t, %v", allowed, err)
	}
	if err := owner.SetMute(ctx, GroupMuteUpdate{GroupID: groupID, Muted: true}); err != nil {
		t.Fatalf("SetMute(true) error = %v", err)
	}
	if err := owner.SetMute(ctx, GroupMuteUpdate{GroupID: groupID}); err != nil {
		t.Fatalf("SetMute(false) error = %v", err)
	}
	if allowed, err := owner.CanSetMemberMute(ctx, groupID, memberID); err != nil || !allowed {
		t.Fatalf("CanSetMemberMute() = %t, %v", allowed, err)
	}
	if err := owner.SetMemberMute(ctx, GroupMemberMuteUpdate{GroupID: groupID, UserID: memberID, Muted: true, DurationSeconds: 60}); err != nil {
		t.Fatalf("SetMemberMute(true) error = %v", err)
	}
	if err := owner.SetMemberMute(ctx, GroupMemberMuteUpdate{GroupID: groupID, UserID: memberID}); err != nil {
		t.Fatalf("SetMemberMute(false) error = %v", err)
	}
	if allowed, err := owner.CanTransferOwner(ctx, groupID, memberID); err != nil || !allowed {
		t.Fatalf("CanTransferOwner() = %t, %v", allowed, err)
	}
	if err := owner.TransferOwner(ctx, GroupOwnerTransfer{GroupID: groupID, NewOwnerUserID: memberID}); err != nil {
		t.Fatalf("TransferOwner() error = %v", err)
	}
}
