//go:build integration

package social

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMSocialReadsIntegration(t *testing.T) {
	apiAddr := socialIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := socialIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := socialIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	friendID := socialIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_USER_ID")
	friendQuery := socialIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_QUERY")
	blackID := socialIntegrationEnv(t, "ABDIM_OPENIM_BLACKLIST_USER_ID")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sdkContext := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   userID,
		Token:    token,
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	source, err := NewSDKSource(OpenIMClient{Context: func() context.Context { return sdkContext }})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := New(source, Options{ProfileID: "integration", Capabilities: VerifiedCapabilities(open_im_sdk.GetSdkVersion())})
	if err != nil {
		t.Fatal(err)
	}

	owner := service.OwnerAccess(reader.capability(FriendListMethod))
	friends, err := reader.Friends(ctx, owner, ListInput{Limit: 100})
	if err != nil || !containsFriend(friends.Data.Items, friendID) {
		t.Fatalf("friend.list = %#v, %v", friends, err)
	}
	assertSocialIntegrationMeta(t, friends.Meta, FriendListMethod)

	owner = service.OwnerAccess(reader.capability(FriendGetMethod))
	friend, err := reader.Friend(ctx, owner, GetInput{UserID: friendID})
	if err != nil || friend.Data.UserID != friendID {
		t.Fatalf("friend.get = %#v, %v", friend, err)
	}
	assertSocialIntegrationMeta(t, friend.Meta, FriendGetMethod)

	owner = service.OwnerAccess(reader.capability(FriendSearchMethod))
	search, err := reader.SearchFriends(ctx, owner, SearchInput{Query: friendQuery, Limit: 100})
	if err != nil || len(search.Data.Items) == 0 {
		t.Fatalf("friend.search = %#v, %v", search, err)
	}
	assertSocialIntegrationMeta(t, search.Meta, FriendSearchMethod)

	owner = service.OwnerAccess(reader.capability(BlackListMethod))
	blacklist, err := reader.Blacklist(ctx, owner, ListInput{Limit: 100})
	if err != nil || !containsBlack(blacklist.Data.Items, blackID) {
		t.Fatalf("blacklist.list = %#v, %v", blacklist, err)
	}
	assertSocialIntegrationMeta(t, blacklist.Meta, BlackListMethod)

	owner = service.OwnerAccess(reader.capability(BlackGetMethod))
	black, err := reader.Black(ctx, owner, GetInput{UserID: blackID})
	if err != nil || black.Data.UserID != blackID {
		t.Fatalf("blacklist.get = %#v, %v", black, err)
	}
	assertSocialIntegrationMeta(t, black.Meta, BlackGetMethod)
}

func socialIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}

func assertSocialIntegrationMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Stale || meta.Capability.Method != method || meta.Capability.Scope != scope(method) || meta.Capability.Status != "available" || meta.Capability.SDKVersion != open_im_sdk.GetSdkVersion() {
		t.Fatalf("%s response metadata is invalid", method)
	}
}

func containsFriend(items []Friend, userID string) bool {
	for _, item := range items {
		if item.UserID == userID {
			return true
		}
	}
	return false
}

func containsBlack(items []BlacklistEntry, userID string) bool {
	for _, item := range items {
		if item.UserID == userID {
			return true
		}
	}
	return false
}
