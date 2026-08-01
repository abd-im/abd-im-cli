//go:build integration

package message

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMTextSendIntegration(t *testing.T) {
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
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := adapter.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}
	defer shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
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
	text := fmt.Sprintf("abdim message.send_text integration %d", time.Now().UnixNano())
	if err := adapter.SendText(ctx, text, recipientID, ""); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
}

func textSendIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
