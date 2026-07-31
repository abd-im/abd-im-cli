//go:build integration

package message

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/service"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMMessageReadsIntegration(t *testing.T) {
	apiAddr := messageIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := messageIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := messageIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	conversationID := messageIntegrationEnv(t, "ABDIM_OPENIM_CONVERSATION_ID")
	messageID := messageIntegrationEnv(t, "ABDIM_OPENIM_MESSAGE_ID")
	query := messageIntegrationEnv(t, "ABDIM_OPENIM_MESSAGE_QUERY")
	afterMessageID := messageIntegrationEnv(t, "ABDIM_OPENIM_AFTER_MESSAGE_ID")

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

	owner := service.OwnerAccess(reader.capability(HistoryMethod))
	history, err := reader.History(ctx, owner, HistoryInput{ConversationID: conversationID, Limit: 100})
	if err != nil || len(history.Data.Items) == 0 {
		t.Fatalf("message.history = %#v, %v", history, err)
	}
	assertMessageIntegrationMeta(t, history.Meta, HistoryMethod)
	if first, err := reader.History(ctx, owner, HistoryInput{ConversationID: conversationID, Limit: 1}); err != nil {
		t.Fatalf("message.history cursor first page = %v", err)
	} else if first.Data.NextCursor != "" {
		second, err := reader.History(ctx, owner, HistoryInput{ConversationID: conversationID, Limit: 1, Cursor: first.Data.NextCursor})
		if err != nil {
			t.Fatalf("message.history cursor second page = %v", err)
		}
		assertMessageIntegrationMeta(t, second.Meta, HistoryMethod)
	}

	owner = service.OwnerAccess(reader.capability(SearchMethod))
	search, err := reader.Search(ctx, owner, SearchInput{ConversationID: conversationID, Query: query, Limit: 100})
	if err != nil || len(search.Data.Items) == 0 {
		t.Fatalf("message.search = %#v, %v", search, err)
	}
	assertMessageIntegrationMeta(t, search.Meta, SearchMethod)

	owner = service.OwnerAccess(reader.capability(GetMethod))
	get, err := reader.Get(ctx, owner, GetInput{ConversationID: conversationID, MessageID: messageID})
	if err != nil || get.Data.ID != messageID {
		t.Fatalf("message.get = %#v, %v", get, err)
	}
	assertMessageIntegrationMeta(t, get.Meta, GetMethod)

	item, _, err := grant.NewStore().Issue(grant.Policy{
		RunID:           "run-integration",
		ProfileID:       "integration",
		Principal:       "provider",
		Methods:         []string{HistoryMethod},
		Scopes:          []string{ReadScope},
		TargetAllowlist: []string{conversationID},
		MessageWindow:   grant.MessageWindow{ConversationID: conversationID, AfterMessageID: afterMessageID},
		ExpiresAt:       time.Now().Add(time.Hour),
		RateBudget:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := reader.History(ctx, service.ProviderAccess(item, reader.capability(HistoryMethod)), HistoryInput{ConversationID: conversationID, Limit: 100})
	if err != nil {
		t.Fatalf("provider message.history = %v", err)
	}
	assertMessageIntegrationMeta(t, provider.Meta, HistoryMethod)
	assertAfterBoundary(t, history.Data.Items, provider.Data.Items, afterMessageID)
}

func messageIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}

func assertMessageIntegrationMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Stale || meta.Capability.Method != method || meta.Capability.Status != "available" || meta.Capability.SDKVersion != open_im_sdk.GetSdkVersion() {
		t.Fatalf("%s response metadata is invalid", method)
	}
}

func assertAfterBoundary(t *testing.T, history, result []Message, boundary string) {
	t.Helper()
	index := -1
	for i, item := range history {
		if item.ID == boundary {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("message window boundary %q was not returned by the server source", boundary)
	}
	want := history[index+1:]
	if len(result) != len(want) {
		t.Fatalf("provider result length = %d, want %d", len(result), len(want))
	}
	for i := range want {
		if result[i].ID != want[i].ID {
			t.Fatalf("provider result %d = %q, want %q", i, result[i].ID, want[i].ID)
		}
	}
}
