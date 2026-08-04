package profile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestProfileReadsExposeStableMetadata(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Stale: func() bool { return true }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := reader.Profile(context.Background(), service.OwnerAccess())
	if err != nil || result.Data.ID != "work" {
		t.Fatalf("Profile() = %+v, %v", result, err)
	}
	assertMeta(t, result.Meta)
	self, err := reader.Self(context.Background(), service.OwnerAccess())
	if err != nil || self.Data.ID != "self" {
		t.Fatalf("Self() = %+v, %v", self, err)
	}
	users, err := reader.Users(context.Background(), service.OwnerAccess(), []string{"user-1"})
	if err != nil || len(users.Data) != 1 || users.Data[0].ID != "user-1" {
		t.Fatalf("Users() = %+v, %v", users, err)
	}
	daemon, err := reader.Daemon(context.Background(), service.OwnerAccess())
	if err != nil || daemon.Data.State != "ready" {
		t.Fatalf("Daemon() = %+v, %v", daemon, err)
	}
	doctor, err := reader.Doctor(context.Background(), service.OwnerAccess())
	if err != nil || !doctor.Data.OK {
		t.Fatalf("Doctor() = %+v, %v", doctor, err)
	}
}

func TestUserReadWorksThroughRunProxy(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{UserGet}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-1", "work", reader.Methods())
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string][]string{"user_ids": {"user-1"}})
	response, err := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "req-1", ProfileID: "work", Method: UserGet, Params: params, Grant: credential})
	if err != nil || !response.OK || response.Meta.Schema != service.SchemaVersion {
		t.Fatalf("proxy Call() = %+v, %v", response, err)
	}
}

func assertMeta(t *testing.T, meta service.Meta) {
	t.Helper()
	if !meta.Stale || meta.Schema != service.SchemaVersion || meta.ProfileID != "work" {
		t.Fatalf("metadata = %+v", meta)
	}
}

type fakeSource struct{}

func (fakeSource) Profile(context.Context) (Profile, error) {
	return Profile{ID: "work", Name: "work"}, nil
}
func (fakeSource) Self(context.Context) (User, error) { return User{ID: "self"}, nil }
func (fakeSource) Users(_ context.Context, ids []string) ([]User, error) {
	result := make([]User, len(ids))
	for i, id := range ids {
		result[i] = User{ID: id}
	}
	return result, nil
}
func (fakeSource) Daemon(context.Context) (DaemonStatus, error) {
	return DaemonStatus{ProfileID: "work", State: "ready", CredentialsValid: true}, nil
}
func (fakeSource) Doctor(context.Context) (DoctorReport, error) { return DoctorReport{OK: true}, nil }
