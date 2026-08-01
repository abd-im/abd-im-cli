//go:build integration

package conversation

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMMarkReadIntegration(t *testing.T) {
	adapter, ctx, userID, recipientID := startMarkReadIntegration(t)
	conversationID := markReadDirectConversationID(userID, recipientID)
	for index := 0; index < 3; index++ {
		if err := adapter.SendText(ctx, fmt.Sprintf("abdim mark-read integration %d-%d", time.Now().UnixNano(), index), recipientID, ""); err != nil {
			t.Fatalf("SendText(%d) error = %v", index, err)
		}
	}
	client := messageservice.OpenIMClient{Context: adapter.Context}
	items, err := waitForMarkReadMessages(ctx, client, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := NewOpenIMMarkRead(OpenIMMarkRead{Context: adapter.Context, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	after, err := action.ResolveBoundary(ctx, conversationID, markReadMessageID(items[len(items)-3]))
	if err != nil {
		t.Fatalf("ResolveBoundary(after) error = %v", err)
	}
	upTo, err := action.ResolveBoundary(ctx, conversationID, markReadMessageID(items[len(items)-2]))
	if err != nil {
		t.Fatalf("ResolveBoundary(up_to) error = %v", err)
	}
	before, err := action.ResolveBoundary(ctx, conversationID, markReadMessageID(items[len(items)-1]))
	if err != nil {
		t.Fatalf("ResolveBoundary(before) error = %v", err)
	}
	if !(after.ServerSeq < upTo.ServerSeq && upTo.ServerSeq < before.ServerSeq) {
		t.Fatalf("server boundaries are not ordered: after=%d up_to=%d before=%d", after.ServerSeq, upTo.ServerSeq, before.ServerSeq)
	}
	if err := action.MarkRead(ctx, MarkReadRequest{ConversationID: conversationID, HasReadSeq: upTo.ServerSeq}); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}
}

func waitForMarkReadMessages(ctx context.Context, client messageservice.Client, conversationID string) ([]*sdkws.MsgData, error) {
	for {
		items, err := client.Messages(ctx, conversationID, markReadHistoryLimit)
		if err != nil {
			return nil, err
		}
		valid := make([]*sdkws.MsgData, 0, len(items))
		for _, item := range items {
			if _, err := markReadBoundary(item, conversationID); err == nil {
				valid = append(valid, item)
			}
		}
		if len(valid) >= 3 {
			sort.Slice(valid, func(first, second int) bool { return valid[first].Seq < valid[second].Seq })
			return valid, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("controlled conversation did not return three message boundaries: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func markReadMessageID(item *sdkws.MsgData) string {
	if item.ServerMsgID != "" {
		return item.ServerMsgID
	}
	return item.ClientMsgID
}

func markReadDirectConversationID(first, second string) string {
	ids := []string{first, second}
	sort.Strings(ids)
	return "si_" + strings.Join(ids, "_")
}

func startMarkReadIntegration(t *testing.T) (*abdim.Adapter, context.Context, string, string) {
	t.Helper()
	apiAddr := markReadIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	wsAddr := markReadIntegrationEnv(t, "ABDIM_OPENIM_WS_ADDR")
	userID := markReadIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := markReadIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	recipientID := markReadIntegrationEnv(t, "ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID")
	if recipientID == userID {
		t.Fatal("ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID must differ from ABDIM_OPENIM_USER_ID")
	}
	platformID, err := strconv.ParseInt(markReadIntegrationEnv(t, "ABDIM_OPENIM_PLATFORM_ID"), 10, 32)
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
		ProfileID: "mark-read-integration",
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

func markReadIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
