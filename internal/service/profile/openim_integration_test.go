//go:build integration

package profile

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func TestOpenIMProfileReadsIntegration(t *testing.T) {
	apiAddr := profileIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	userID := profileIntegrationEnv(t, "ABDIM_OPENIM_USER_ID")
	token := profileIntegrationEnv(t, "ABDIM_OPENIM_TOKEN")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sdkContext := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   userID,
		Token:    token,
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	source, err := NewOpenIMSource(OpenIMSourceConfig{
		Profile: Profile{ID: "integration", Name: "integration", SDKVersion: open_im_sdk.GetSdkVersion()},
		SelfID:  userID,
		Client:  OpenIMClient{Context: func() context.Context { return sdkContext }},
		Daemon: func() DaemonStatus {
			return DaemonStatus{ProfileID: "integration", State: "ready", SDKVersion: open_im_sdk.GetSdkVersion(), CredentialsValid: true}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := New(source, Options{ProfileID: "integration"})
	if err != nil {
		t.Fatal(err)
	}

	profile, err := reader.Profile(ctx)
	if err != nil || profile.Data.ID != "integration" {
		t.Fatalf("profile.get = %#v, %v", profile, err)
	}
	assertProfileIntegrationMeta(t, profile.Meta, ProfileGet)
	self, err := reader.Self(ctx)
	if err != nil || self.Data.ID != userID {
		t.Fatalf("user.me = %#v, %v", self, err)
	}
	assertProfileIntegrationMeta(t, self.Meta, UserMe)
	users, err := reader.Users(ctx, []string{userID})
	if err != nil || len(users.Data) != 1 || users.Data[0].ID != userID {
		t.Fatalf("user.get = %#v, %v", users, err)
	}
	assertProfileIntegrationMeta(t, users.Meta, UserGet)
	daemon, err := reader.Daemon(ctx)
	if err != nil || daemon.Data.State != "ready" {
		t.Fatalf("daemon.status = %#v, %v", daemon, err)
	}
	assertProfileIntegrationMeta(t, daemon.Meta, DaemonGet)
	doctor, err := reader.Doctor(ctx)
	if err != nil || !doctor.Data.OK {
		t.Fatalf("doctor.get = %#v, %v", doctor, err)
	}
	assertProfileIntegrationMeta(t, doctor.Meta, DoctorGet)
}

func profileIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}

func assertProfileIntegrationMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if meta.Schema != service.SchemaVersion || meta.Stale || meta.ProfileID != "integration" {
		t.Fatalf("%s response metadata is invalid", method)
	}
}
