//go:build integration

package conversation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMConversationReadsIntegration(t *testing.T) {
	apiAddr := conversationIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := conversationIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := conversationIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")

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

	owner := service.OwnerAccess(reader.capability(ListMethod))
	list, err := reader.List(ctx, owner, ListInput{Limit: 1})
	if err != nil || len(list.Data.Items) == 0 {
		t.Fatalf("conversation.list = %#v, %v", list, err)
	}
	assertConversationIntegrationMeta(t, list.Meta, ListMethod)
	if list.Data.NextCursor != "" {
		second, err := reader.List(ctx, owner, ListInput{Limit: 1, Cursor: list.Data.NextCursor})
		if err != nil {
			t.Fatalf("conversation.list cursor = %v", err)
		}
		assertConversationIntegrationMeta(t, second.Meta, ListMethod)
	}

	conversationID := list.Data.Items[0].ID
	owner = service.OwnerAccess(reader.capability(GetMethod))
	get, err := reader.Get(ctx, owner, GetInput{ConversationID: conversationID})
	if err != nil || get.Data.ID != conversationID {
		t.Fatalf("conversation.get = %#v, %v", get, err)
	}
	assertConversationIntegrationMeta(t, get.Meta, GetMethod)

	owner = service.OwnerAccess(reader.capability(SearchMethod))
	search, err := reader.Search(ctx, owner, SearchInput{Query: conversationID, Limit: 1})
	if err != nil || len(search.Data.Items) == 0 || search.Data.Items[0].ID != conversationID {
		t.Fatalf("conversation.search = %#v, %v", search, err)
	}
	assertConversationIntegrationMeta(t, search.Meta, SearchMethod)

	if _, err := reader.Unread(ctx, service.OwnerAccess(service.Capability{})); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("conversation.unread error = %v", err)
	}
}

func conversationIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}

func assertConversationIntegrationMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Stale || meta.Capability.Method != method || meta.Capability.Status != "available" || meta.Capability.SDKVersion != open_im_sdk.GetSdkVersion() {
		t.Fatalf("%s response metadata is invalid", method)
	}
}
