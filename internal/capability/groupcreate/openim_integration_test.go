//go:build integration

package groupcreate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMGroupCreateIntegration(t *testing.T) {
	apiAddr := groupCreateIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := groupCreateIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := groupCreateIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")
	memberID := groupCreateIntegrationEnv(t, "ABDIM_OPENIM_GROUP_CREATE_MEMBER_ID")
	if memberID == userID {
		t.Fatal("ABDIM_OPENIM_GROUP_CREATE_MEMBER_ID must differ from ABDIM_OPENIM_USER_ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sdkContext := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   userID,
		Token:    token,
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	creator, err := NewOpenIMCreator(OpenIMCreator{Context: func() context.Context { return sdkContext }})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateGroup(ctx, Input{Name: fmt.Sprintf("abdim-e2e-%d", time.Now().UnixNano()), MemberIDs: []string{memberID}}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
}

func groupCreateIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
