//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/reply"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

// TestLiveInboundReply requires a separately running daemon for the recipient.
// It is intentionally excluded from normal test runs and reads credentials only
// from the process environment of an explicit test invocation.
func TestLiveInboundReply(t *testing.T) {
	senderID := os.Getenv("ABDIM_E2E_SENDER_ID")
	senderToken := os.Getenv("ABDIM_E2E_SENDER_TOKEN")
	recipientID := os.Getenv("ABDIM_E2E_RECIPIENT_ID")
	if senderID == "" || senderToken == "" || recipientID == "" {
		t.Skip("set ABDIM_E2E_SENDER_ID, ABDIM_E2E_SENDER_TOKEN, and ABDIM_E2E_RECIPIENT_ID")
	}

	root := t.TempDir()
	if err := os.MkdirAll(root+"/sdk", 0o700); err != nil {
		t.Fatalf("create sender SDK directory: %v", err)
	}
	if err := os.MkdirAll(root+"/logs", 0o700); err != nil {
		t.Fatalf("create sender log directory: %v", err)
	}
	events := make(chan contracts.SDKEvent, 8)
	adapter, err := abdim.New(abdim.Config{
		ProfileID: "live-sender",
		UserID:    senderID,
		Token:     []byte(senderToken),
		SDKConfig: sdk_struct.IMConfig{
			PlatformID:  7,
			ApiAddr:     "https://2.alissa.xin/api",
			WsAddr:      "wss://2.alissa.xin/msg_gateway",
			DataDir:     root + "/sdk",
			LogFilePath: root + "/logs/sdk.log",
			LogLevel:    4,
		},
		ConnectTimeout: 45 * time.Second,
	})
	if err != nil {
		t.Fatalf("create sender adapter: %v", err)
	}
	if err := adapter.InitSDK(context.Background()); err != nil {
		t.Fatalf("initialize sender SDK: %v", err)
	}
	defer adapter.Shutdown(context.Background())
	if err := adapter.InitResources(context.Background()); err != nil {
		t.Fatalf("initialize sender resources: %v", err)
	}
	if err := adapter.SetEventListener(func(_ context.Context, event contracts.SDKEvent) {
		select {
		case events <- event:
		default:
		}
	}); err != nil {
		t.Fatalf("register sender event listener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := adapter.Login(ctx); err != nil {
		t.Fatalf("log in sender: %v", err)
	}
	if err := adapter.Reply(ctx, reply.Delivery{
		RecipientID: recipientID,
		Text:        "ABDIM live verification: reply with a short confirmation.",
	}); err != nil {
		t.Fatalf("send inbound test message: %v", err)
	}

	for {
		select {
		case event := <-events:
			var data struct {
				SenderID string `json:"sender_id"`
			}
			if json.Unmarshal(event.Data, &data) == nil && event.Type == string(contracts.EventMessageReceived) && data.SenderID == recipientID && strings.TrimSpace(event.MessageText) != "" {
				return
			}
		case <-ctx.Done():
			t.Fatal("did not receive an automatic reply before the deadline")
		}
	}
}
