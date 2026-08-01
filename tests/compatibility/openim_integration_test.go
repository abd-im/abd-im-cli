//go:build integration

package compatibility

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/capability"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMServerMatchesSingleCodexCompatibilityMatrix(t *testing.T) {
	combination := supportedCombination(t)
	apiAddr := compatibilityIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := compatibilityIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := compatibilityIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sdkContext := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID: userID,
		Token:  token,
		IMConfig: &sdk_struct.IMConfig{
			ApiAddr: apiAddr,
		},
	})
	client := profileservice.OpenIMClient{Context: func() context.Context { return sdkContext }}
	users, err := client.Users(ctx, []string{userID})
	if err != nil || len(users) != 1 || users[0].ID != userID {
		t.Fatalf("OpenIM user compatibility probe = %#v, %v", users, err)
	}

	gate, err := capability.NewEvidenceGate([]capability.Compatibility{combination})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := gate.Manifest(combination, []capability.Entry{{Method: "user.get", Scope: "user.get.read", Status: capability.Available}})
	if err != nil || !manifest.Allows("user.get", "user.get.read") {
		t.Fatalf("verified OpenIM server evidence = %v, %v", manifest, err)
	}
}

func compatibilityIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for compatibility integration tests", name)
	}
	return value
}
