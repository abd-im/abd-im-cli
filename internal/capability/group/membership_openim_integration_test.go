//go:build integration

package group

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

// TestOpenIMGroupMembershipIntegration creates a disposable working group and
// verifies the fixed server source for leave, invite, remove, and join. The
// owner remains after the test because the public capability intentionally has
// no group-dismiss method.
func TestOpenIMGroupMembershipIntegration(t *testing.T) {
	apiAddr := membershipIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	ownerID := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_ID")
	ownerToken := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_TOKEN")
	memberID := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_ID")
	memberToken := membershipIntegrationEnv(t, "ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_TOKEN")
	if ownerID == memberID {
		t.Fatal("group membership fixture requires two different users")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	ownerContext := integrationGroupContext(apiAddr, ownerID, ownerToken)
	memberContext := integrationGroupContext(apiAddr, memberID, memberToken)
	creator, err := NewOpenIMCreator(OpenIMCreator{Context: ownerContext})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abdim-membership-%d", time.Now().UnixNano())
	if err := creator.CreateGroup(ctx, Input{Name: name, MemberIDs: []string{memberID}}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	groupID, err := waitForIntegrationGroup(ctx, groupservice.OpenIMClient{Context: ownerContext}, name)
	if err != nil {
		t.Fatal(err)
	}

	owner, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: ownerContext})
	if err != nil {
		t.Fatal(err)
	}
	member, err := NewOpenIMMembershipSource(OpenIMMembershipSource{Context: memberContext})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := member.CanLeaveGroup(ctx, groupID); err != nil || !allowed {
		t.Fatalf("initial CanLeaveGroup() = %t, %v", allowed, err)
	}
	if err := member.LeaveGroup(ctx, LeaveInput{GroupID: groupID}); err != nil {
		t.Fatalf("LeaveGroup() error = %v", err)
	}
	if allowed, err := owner.CanInviteMembers(ctx, groupID, []string{memberID}); err != nil || !allowed {
		t.Fatalf("CanInviteMembers() = %t, %v", allowed, err)
	}
	if err := owner.InviteMembers(ctx, MembersInput{GroupID: groupID, UserIDs: []string{memberID}, Reason: "integration"}); err != nil {
		t.Fatalf("InviteMembers() error = %v", err)
	}
	if allowed, err := owner.CanRemoveMembers(ctx, groupID, []string{memberID}); err != nil || !allowed {
		t.Fatalf("CanRemoveMembers() = %t, %v", allowed, err)
	}
	if err := owner.RemoveMembers(ctx, MembersInput{GroupID: groupID, UserIDs: []string{memberID}, Reason: "integration"}); err != nil {
		t.Fatalf("RemoveMembers() error = %v", err)
	}
	if err := member.JoinGroup(ctx, JoinInput{GroupID: groupID, Message: "integration"}); err != nil {
		t.Fatalf("JoinGroup() error = %v", err)
	}
}

func integrationGroupContext(apiAddr, userID, token string) func() context.Context {
	return func() context.Context {
		return ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
			UserID:   userID,
			Token:    token,
			IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
		})
	}
}

func waitForIntegrationGroup(ctx context.Context, client groupservice.OpenIMClient, name string) (string, error) {
	for {
		groups, err := client.JoinedGroups(ctx)
		if err != nil {
			return "", err
		}
		for _, item := range groups {
			if item != nil && item.GroupName == name && item.GroupID != "" {
				return item.GroupID, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("created group %q did not appear: %w", name, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func membershipIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Skipf("%s is required for group membership integration", name)
	}
	return value
}
