//go:build integration

package conversation

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbconversation "github.com/openimsdk/protocol/conversation"
)

func TestOpenIMConversationSettingsIntegration(t *testing.T) {
	for _, name := range []string{
		"ABDIM_OPENIM_API_ADDR",
		"ABDIM_OPENIM_WS_ADDR",
		"ABDIM_OPENIM_USER_ID",
		"ABDIM_OPENIM_TOKEN",
		"ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID",
		"ABDIM_OPENIM_PLATFORM_ID",
	} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			t.Skipf("%s is required for controlled OpenIM integration", name)
		}
	}

	adapter, ctx, userID, recipientID := startMarkReadIntegration(t)
	conversationID := markReadDirectConversationID(userID, recipientID)
	if err := adapter.SendText(ctx, "abdim conversation settings integration", recipientID, ""); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}

	client := conversationservice.OpenIMClient{Context: adapter.Context}
	settings, err := NewOpenIMSettings(OpenIMSettings{Context: adapter.Context, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waitForConversationSetting(ctx, client, conversationID, func(*pbconversation.Conversation) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetPinned(ctx, SetPinnedInput{ConversationID: conversationID, Pinned: true}); err != nil {
		t.Fatalf("SetPinned() error = %v", err)
	}
	if _, err := waitForConversationSetting(ctx, client, conversationID, func(item *pbconversation.Conversation) bool { return item.IsPinned }); err != nil {
		t.Fatalf("pinned setting was not returned by the server: %v", err)
	}
	if err := settings.SetReceiveOption(ctx, SetReceiveOptionInput{ConversationID: conversationID, Option: ReceiveOptionReceiveNoNotify}); err != nil {
		t.Fatalf("SetReceiveOption() error = %v", err)
	}
	if _, err := waitForConversationSetting(ctx, client, conversationID, func(item *pbconversation.Conversation) bool {
		return item.RecvMsgOpt == pbconstant.ReceiveNotNotifyMessage
	}); err != nil {
		t.Fatalf("receive option was not returned by the server: %v", err)
	}

	// Restore the controlled account's normal conversation settings.
	if err := settings.SetReceiveOption(context.Background(), SetReceiveOptionInput{ConversationID: conversationID, Option: ReceiveOptionReceive}); err != nil {
		t.Errorf("restore receive option: %v", err)
	}
	if err := settings.SetPinned(context.Background(), SetPinnedInput{ConversationID: conversationID, Pinned: false}); err != nil {
		t.Errorf("restore pinned setting: %v", err)
	}
}

func waitForConversationSetting(ctx context.Context, client conversationservice.Client, conversationID string, matches func(*pbconversation.Conversation) bool) (*pbconversation.Conversation, error) {
	for {
		items, err := client.Conversations(ctx, []string{conversationID})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item != nil && item.ConversationID == conversationID && matches(item) {
				return item, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
