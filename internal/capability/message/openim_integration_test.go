//go:build integration

package message

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMTextSendIntegration(t *testing.T) {
	adapter, ctx, _, recipientID := startMessageIntegration(t)
	text := fmt.Sprintf("abdim message.send_text integration %d", time.Now().UnixNano())
	if err := adapter.SendText(ctx, text, recipientID, ""); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
}

func TestOpenIMQuoteSendIntegration(t *testing.T) {
	adapter, ctx, userID, recipientID := startMessageIntegration(t)
	conversationID := directConversationID(userID, recipientID)
	source, err := NewOpenIMQuoteSource(messageservice.OpenIMClient{Context: adapter.Context}, adapter, userID)
	if err != nil {
		t.Fatal(err)
	}
	references, err := source.History(ctx, conversationID)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(references) == 0 {
		if err := adapter.SendText(ctx, fmt.Sprintf("abdim quote source %d", time.Now().UnixNano()), recipientID, ""); err != nil {
			t.Fatalf("SendText() source error = %v", err)
		}
		references, err = source.History(ctx, conversationID)
		if err != nil {
			t.Fatalf("History() after source send error = %v", err)
		}
	}
	if len(references) == 0 {
		t.Fatal("server returned no quoteable controlled-conversation message")
	}
	input := QuoteInput{
		Text:           fmt.Sprintf("abdim message.send_quote integration %d", time.Now().UnixNano()),
		RecipientID:    recipientID,
		ConversationID: conversationID,
		MessageID:      references[len(references)-1].ID,
	}
	if err := source.SendQuote(ctx, input); err != nil {
		t.Fatalf("SendQuote() error = %v", err)
	}
}

func TestOpenIMTextAtIntegration(t *testing.T) {
	adapter, ctx, userID, recipientID := startMessageIntegration(t)
	creator, err := groupcapability.NewOpenIMCreator(groupcapability.OpenIMCreator{Context: adapter.Context})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abdim-at-%d", time.Now().UnixNano())
	if err := creator.CreateGroup(ctx, groupcapability.Input{Name: name, MemberIDs: []string{recipientID}}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	groupID, err := integrationGroupID(ctx, groupservice.OpenIMClient{Context: adapter.Context}, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.SendAt(ctx, fmt.Sprintf("abdim message.send_at integration %d", time.Now().UnixNano()), groupID, []string{recipientID}); err != nil {
		t.Fatalf("SendAt() error = %v", err)
	}
	if userID == recipientID {
		t.Fatal("integration accounts must differ")
	}
}

func TestOpenIMLocationAndCustomIntegration(t *testing.T) {
	adapter, ctx, _, recipientID := startMessageIntegration(t)
	if err := adapter.SendLocation(ctx, "abdim integration location", 120.1, 30.2, recipientID, ""); err != nil {
		t.Fatalf("SendLocation() error = %v", err)
	}
	if err := adapter.SendCustom(ctx, "abdim integration custom", "abdim.v1", "controlled custom payload", "", recipientGroupID(t, adapter, ctx, recipientID)); err != nil {
		t.Fatalf("SendCustom() error = %v", err)
	}
}

func TestOpenIMRevokeIntegration(t *testing.T) {
	adapter, ctx, userID, recipientID := startMessageIntegration(t)
	marker := fmt.Sprintf("abdim revoke integration %d", time.Now().UnixNano())
	if err := adapter.SendText(ctx, marker, recipientID, ""); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	conversationID := directConversationID(userID, recipientID)
	client := messageservice.OpenIMClient{Context: adapter.Context}
	var messageID string
	var sequence int64
	for {
		items, err := client.Messages(ctx, conversationID, 100)
		if err != nil {
			t.Fatalf("Messages() error = %v", err)
		}
		for _, item := range items {
			if item != nil && item.SendID == userID && bytes.Contains(item.Content, []byte(marker)) {
				messageID = item.ServerMsgID
				if messageID == "" {
					messageID = item.ClientMsgID
				}
				sequence = item.Seq
			}
		}
		if messageID != "" && sequence > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("sent revoke message was not returned by server")
		case <-time.After(500 * time.Millisecond):
		}
	}
	revoker, err := NewOpenIMRevoke(OpenIMRevoke{Context: adapter.Context, Client: client}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := revoker.Revoke(ctx, RevokeInput{ConversationID: conversationID, MessageID: messageID}); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
}

func recipientGroupID(t *testing.T, adapter *abdim.Adapter, ctx context.Context, recipientID string) string {
	t.Helper()
	creator, err := groupcapability.NewOpenIMCreator(groupcapability.OpenIMCreator{Context: adapter.Context})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abdim-custom-%d", time.Now().UnixNano())
	if err := creator.CreateGroup(ctx, groupcapability.Input{Name: name, MemberIDs: []string{recipientID}}); err != nil {
		t.Fatal(err)
	}
	groupID, err := integrationGroupID(ctx, groupservice.OpenIMClient{Context: adapter.Context}, name)
	if err != nil {
		t.Fatal(err)
	}
	return groupID
}

func integrationGroupID(ctx context.Context, client groupservice.Client, name string) (string, error) {
	for {
		groups, err := client.JoinedGroups(ctx)
		if err != nil {
			return "", err
		}
		for _, group := range groups {
			if group != nil && group.GroupName == name && strings.TrimSpace(group.GroupID) != "" {
				return group.GroupID, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("created integration group was not visible to owner")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func startMessageIntegration(t *testing.T) (*abdim.Adapter, context.Context, string, string) {
	t.Helper()
	apiAddr := textSendIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	wsAddr := textSendIntegrationEnv(t, "ABDIM_OPENIM_WS_ADDR")
	userID := textSendIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := textSendIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	recipientID := textSendIntegrationEnv(t, "ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID")
	if recipientID == userID {
		t.Fatal("ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID must differ from ABDIM_OPENIM_USER_ID")
	}
	platformID, err := strconv.ParseInt(textSendIntegrationEnv(t, "ABDIM_OPENIM_PLATFORM_ID"), 10, 32)
	if err != nil || platformID <= 0 {
		t.Fatal("ABDIM_OPENIM_PLATFORM_ID must be a positive integer")
	}

	root := t.TempDir()
	if err := os.MkdirAll(root+"/sdk", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/logs", 0o700); err != nil {
		t.Fatal(err)
	}
	adapter, err := abdim.New(abdim.Config{
		ProfileID: "integration",
		UserID:    userID,
		Token:     []byte(token),
		SDKConfig: sdk_struct.IMConfig{
			SystemType:  "linux",
			PlatformID:  int32(platformID),
			ApiAddr:     apiAddr,
			WsAddr:      wsAddr,
			DataDir:     root + "/sdk",
			LogLevel:    4,
			LogFilePath: root + "/logs/sdk.log",
		},
		ConnectTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := adapter.Shutdown(shutdown); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	if err := adapter.InitSDK(ctx); err != nil {
		t.Fatalf("InitSDK() error = %v", err)
	}
	if err := adapter.InitResources(ctx); err != nil {
		t.Fatalf("InitResources() error = %v", err)
	}
	if err := adapter.SetEventListener(nil); err != nil {
		t.Fatalf("SetEventListener() error = %v", err)
	}
	if err := adapter.Login(ctx); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return adapter, ctx, userID, recipientID
}

func textSendIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
