package profile

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service"
)

func TestProfileReadsExposeSchemaStaleAndCapability(t *testing.T) {
	source := fakeSource{}
	reader, err := New(source, Options{ProfileID: "work", Stale: func() bool { return true }, Capabilities: availableAll()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := reader.Profile(context.Background(), service.OwnerAccess(reader.capability(ProfileGet)))
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if result.Data.ID != "work" {
		t.Fatalf("Profile() = %+v", result)
	}
	assertMeta(t, result.Meta, ProfileGet)

	self, err := reader.Self(context.Background(), service.OwnerAccess(reader.capability(UserMe)))
	if err != nil || self.Data.ID != "self" {
		t.Fatalf("Self() = %+v, %v", self, err)
	}
	assertMeta(t, self.Meta, UserMe)

	users, err := reader.Users(context.Background(), service.OwnerAccess(reader.capability(UserGet)), []string{"user-1"})
	if err != nil || len(users.Data) != 1 || users.Data[0].ID != "user-1" {
		t.Fatalf("Users() = %+v, %v", users, err)
	}
	assertMeta(t, users.Meta, UserGet)

	daemon, err := reader.Daemon(context.Background(), service.OwnerAccess(reader.capability(DaemonGet)))
	if err != nil || daemon.Data.State != "ready" {
		t.Fatalf("Daemon() = %+v, %v", daemon, err)
	}
	assertMeta(t, daemon.Meta, DaemonGet)

	doctor, err := reader.Doctor(context.Background(), service.OwnerAccess(reader.capability(DoctorGet)))
	if err != nil || !doctor.Data.OK {
		t.Fatalf("Doctor() = %+v, %v", doctor, err)
	}
	assertMeta(t, doctor.Meta, DoctorGet)
}

func TestUserReadsRequireGrantTargetAndProxyCarriesMeta(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work", Capabilities: available(UserGet, "user.get.read")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	grants := grant.NewStore()
	access, credential, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{UserGet}, Scopes: []string{"user.get.read"}, TargetAllowlist: []string{"user-1"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 3})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	result, err := reader.Users(context.Background(), service.ProviderAccess(access, reader.capability(UserGet)), []string{"user-1"})
	if err != nil || len(result.Data) != 1 || result.Data[0].ID != "user-1" {
		t.Fatalf("Users() = %+v, %v", result, err)
	}
	if _, err := reader.Users(context.Background(), service.ProviderAccess(access, reader.capability(UserGet)), []string{"user-2"}); !errors.Is(err, service.ErrTargetDenied) {
		t.Fatalf("Users() outside target error = %v", err)
	}

	methods := reader.Methods()
	tool, err := proxy.New(grants, "run-1", "work", methods)
	if err != nil {
		t.Fatalf("proxy.New() error = %v", err)
	}
	params, _ := json.Marshal(map[string][]string{"user_ids": {"user-1"}})
	response, err := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "req-1", ProfileID: "work", Method: UserGet, Params: params, Grant: credential})
	if err != nil || !response.OK || response.Meta.Schema != service.SchemaVersion || response.Meta.Capability == nil || response.Meta.Capability.Status != "available" {
		t.Fatalf("proxy Call() = %+v, %v", response, err)
	}
	var users []User
	if err := json.Unmarshal(response.Data, &users); err != nil || len(users) != 1 {
		t.Fatalf("proxy data = %s, %v", response.Data, err)
	}
}

func TestUnverifiedCapabilityFailsClosed(t *testing.T) {
	reader, err := New(fakeSource{}, Options{ProfileID: "work"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := reader.Profile(context.Background(), service.OwnerAccess(reader.capability(ProfileGet))); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("Profile() unverified error = %v, want ErrCapabilityUnavailable", err)
	}
}

type fakeSource struct{}

func available(method, scope string) map[string]service.Capability {
	return map[string]service.Capability{method: {Method: method, Scope: scope, Status: "available"}}
}

func availableAll() map[string]service.Capability {
	return map[string]service.Capability{
		ProfileGet: {Method: ProfileGet, Scope: "profile.get.read", Status: "available"},
		UserMe:     {Method: UserMe, Scope: "user.me.read", Status: "available"},
		UserGet:    {Method: UserGet, Scope: "user.get.read", Status: "available"},
		DaemonGet:  {Method: DaemonGet, Scope: "daemon.status.read", Status: "available"},
		DoctorGet:  {Method: DoctorGet, Scope: "doctor.get.read", Status: "available"},
	}
}

func assertMeta(t *testing.T, meta service.Meta, method string) {
	t.Helper()
	if !meta.Stale || meta.Schema != service.SchemaVersion || meta.Capability.Method != method || meta.Capability.Status != "available" {
		t.Fatalf("metadata = %+v, want stale verified %s metadata", meta, method)
	}
}

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
