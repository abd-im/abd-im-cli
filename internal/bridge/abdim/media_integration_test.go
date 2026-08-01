//go:build integration

package abdim

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestAdapterMediaUploadIntegration(t *testing.T) {
	adapter, ctx, recipientID := startMediaIntegration(t)
	imageBytes := mediaIntegrationImage(t)
	if err := adapter.SendImage(ctx, messagecapability.MediaPayload{Reader: bytes.NewReader(imageBytes), FileName: "abdim.png"}, recipientID, ""); err != nil {
		t.Fatalf("SendImage() error = %v", err)
	}
	if err := adapter.SendFile(ctx, messagecapability.MediaPayload{Reader: bytes.NewReader([]byte("abdim media file")), FileName: "abdim.txt"}, recipientID, ""); err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	if err := adapter.SendSound(ctx, messagecapability.MediaPayload{Reader: bytes.NewReader([]byte("abdim media sound")), FileName: "abdim.ogg"}, 2, recipientID, ""); err != nil {
		t.Fatalf("SendSound() error = %v", err)
	}
	if err := adapter.SendVideo(ctx, messagecapability.MediaPayload{Reader: bytes.NewReader([]byte("abdim media video")), FileName: "abdim.mp4"}, messagecapability.MediaPayload{Reader: bytes.NewReader(imageBytes), FileName: "abdim-thumbnail.png"}, 2, recipientID, ""); err != nil {
		t.Fatalf("SendVideo() error = %v", err)
	}
}

func startMediaIntegration(t *testing.T) (*Adapter, context.Context, string) {
	t.Helper()
	apiAddr := mediaIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	wsAddr := mediaIntegrationEnv(t, "ABDIM_OPENIM_WS_ADDR")
	userID := mediaIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := mediaIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	recipientID := mediaIntegrationEnv(t, "ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID")
	if recipientID == userID {
		t.Fatal("ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID must differ from ABDIM_OPENIM_USER_ID")
	}
	platformID, err := strconv.ParseInt(mediaIntegrationEnv(t, "ABDIM_OPENIM_PLATFORM_ID"), 10, 32)
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
	adapter, err := New(Config{
		ProfileID: "media-integration",
		UserID:    userID,
		Token:     []byte(token),
		SDKConfig: sdk_struct.IMConfig{
			SystemType: "linux", PlatformID: int32(platformID), ApiAddr: apiAddr, WsAddr: wsAddr,
			DataDir: root + "/sdk", LogLevel: 4, LogFilePath: root + "/logs/sdk.log",
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	return adapter, ctx, recipientID
}

func mediaIntegrationImage(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	picture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	picture.SetNRGBA(0, 0, color.NRGBA{R: 32, G: 96, B: 192, A: 255})
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func mediaIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
