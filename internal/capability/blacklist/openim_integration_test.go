//go:build integration

package blacklist

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMBlacklistLifecycleIntegration(t *testing.T) {
	apiAddr := blacklistIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	ownerID := blacklistIntegrationEnv(t, "ABDIM_OPENIM_BLACKLIST_OWNER_ID")
	ownerToken := blacklistIntegrationEnv(t, "ABDIM_OPENIM_BLACKLIST_OWNER_TOKEN")
	targetID := blacklistIntegrationEnv(t, "ABDIM_OPENIM_BLACKLIST_TARGET_ID")
	if ownerID == targetID {
		t.Fatal("blacklist fixture requires two different users")
	}
	source, err := NewOpenIMSource(OpenIMSource{Context: func() context.Context {
		return ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: ownerID, Token: ownerToken, IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if blocked, err := source.IsBlacklisted(ctx, targetID); err != nil {
		t.Fatalf("initial IsBlacklisted() error = %v", err)
	} else if blocked {
		if err := source.RemoveBlacklist(ctx, targetID); err != nil {
			t.Fatalf("reset existing blacklist: %v", err)
		}
	}
	if err := source.AddBlacklist(ctx, targetID); err != nil {
		t.Fatalf("AddBlacklist() error = %v", err)
	}
	if blocked, err := source.IsBlacklisted(ctx, targetID); err != nil || !blocked {
		t.Fatalf("added blacklist = %t, %v", blocked, err)
	}
	if err := source.RemoveBlacklist(ctx, targetID); err != nil {
		t.Fatalf("RemoveBlacklist() error = %v", err)
	}
	if blocked, err := source.IsBlacklisted(ctx, targetID); err != nil || blocked {
		t.Fatalf("removed blacklist = %t, %v", blocked, err)
	}
}

func blacklistIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
