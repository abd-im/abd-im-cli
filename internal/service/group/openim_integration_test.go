//go:build integration

package group

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

func TestOpenIMGroupReadsIntegration(t *testing.T) {
	apiAddr := integrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := integrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := integrationEnv(t, "ABDIM_OPENIM_TOKEN")
	groupID := integrationEnv(t, "ABDIM_OPENIM_GROUP_ID")
	memberQuery := integrationEnv(t, "ABDIM_OPENIM_MEMBER_QUERY")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sdkContext := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   userID,
		Token:    token,
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	source, err := NewSDKSource(OpenIMClient{Context: func() context.Context { return sdkContext }})
	if err != nil {
		t.Fatalf("NewSDKSource() error = %v", err)
	}
	reader, err := New(source, Options{ProfileID: "integration", Capabilities: VerifiedCapabilities(open_im_sdk.GetSdkVersion())})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	owner := service.OwnerAccess(reader.capability(ListMethod))
	list, err := reader.List(ctx, owner, ListInput{Limit: 10})
	if err != nil {
		t.Fatalf("group.list error = %v", err)
	}
	assertIntegrationMeta(t, list.Meta, ListMethod)

	owner = service.OwnerAccess(reader.capability(GetMethod))
	get, err := reader.Get(ctx, owner, GetInput{GroupID: groupID})
	if err != nil || get.Data.ID != groupID {
		t.Fatalf("group.get failed")
	}
	assertIntegrationMeta(t, get.Meta, GetMethod)

	owner = service.OwnerAccess(reader.capability(SearchMethod))
	search, err := reader.Search(ctx, owner, SearchInput{Query: groupID, Limit: 10})
	if err != nil {
		t.Fatalf("group.search error = %v", err)
	}
	assertIntegrationMeta(t, search.Meta, SearchMethod)

	owner = service.OwnerAccess(reader.capability(MembersListMethod))
	members, err := reader.Members(ctx, owner, MembersInput{GroupID: groupID, Limit: 10})
	if err != nil {
		t.Fatalf("group.members.list error = %v", err)
	}
	assertIntegrationMeta(t, members.Meta, MembersListMethod)

	owner = service.OwnerAccess(reader.capability(MembersSearchMethod))
	memberSearch, err := reader.SearchMembers(ctx, owner, MembersSearchInput{GroupID: groupID, Query: memberQuery, Limit: 10})
	if err != nil {
		t.Fatalf("group.members.search error = %v", err)
	}
	assertIntegrationMeta(t, memberSearch.Meta, MembersSearchMethod)
}

func integrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}

func assertIntegrationMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Stale || meta.Capability.Method != method || meta.Capability.Status != "available" || meta.Capability.SDKVersion != open_im_sdk.GetSdkVersion() {
		t.Fatalf("%s response metadata is invalid", method)
	}
}
