package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsCreatePrivateProfileLayout(t *testing.T) {
	root := t.TempDir()
	paths, err := NewPaths(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "runtime"), "work")
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		t.Fatalf("EnsurePrivate() error = %v", err)
	}
	if paths.ControlDB != filepath.Join(root, "data", "abdim", "profiles", "work", "control.db") {
		t.Fatalf("ControlDB = %q", paths.ControlDB)
	}
	for _, path := range []string{filepath.Dir(paths.ConfigFile), paths.DataDir, paths.UserSDKDir, paths.BotSDKDir, paths.LogsDir, paths.RuntimeDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%q permissions = %o, want 700", path, got)
		}
	}
	for _, invalid := range []string{"", "../work", "work/name", ".work"} {
		if _, err := NewPaths(root, root, root, invalid); !errors.Is(err, ErrInvalidName) {
			t.Errorf("NewPaths(%q) error = %v, want ErrInvalidName", invalid, err)
		}
	}
}

func TestFileStoreKeepsTokenOutOfProfile(t *testing.T) {
	const token = "test-token-marker-4d2a0d"
	root := t.TempDir()
	paths, err := NewPaths(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "runtime"), "work")
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	store, err := NewFileStore(filepath.Join(root, "data"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	reference, err := store.Put(context.Background(), "work-user", []byte(token))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if reference != "file:work-user" {
		t.Fatalf("reference = %q, want file:work-user", reference)
	}
	item := validProfile()
	item.User.CredentialRef = reference
	if err := Save(paths.ConfigFile, item); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	contents, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read profile file: %v", err)
	}
	if strings.Contains(string(contents), token) {
		t.Fatalf("profile file retained token marker: %q", contents)
	}
	loaded, err := Load(paths.ConfigFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != item {
		t.Fatalf("Load() = %#v, want %#v", loaded, item)
	}
	restored, err := store.Get(context.Background(), loaded.User.CredentialRef)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(restored) != token {
		t.Fatalf("Get() token = %q, want marker", restored)
	}

	info, err := os.Stat(filepath.Join(root, "data", "abdim", "credentials", "work-user.token"))
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions = %o, want 600", got)
	}
}

func TestLoadRejectsLegacySingleIdentityProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	contents := "name = \"work\"\ncredential_ref = \"file:work\"\nuser_id = \"bot-user\"\napi_addr = \"https://example.test/api\"\nchat_api_addr = \"https://example.test/chat\"\nws_addr = \"wss://example.test/ws\"\nplatform_id = 7\npairing_code_hash = \"legacy\"\npairing_expires_at = 1\nowner_user_id = \"legacy-owner\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a legacy single-identity profile")
	}
}

func TestNormalizeAgentAcceptsOnlyFixedProviders(t *testing.T) {
	for _, value := range []string{"codex", "hermes", "openclaw"} {
		if got, err := NormalizeAgent(value); err != nil || got != value {
			t.Errorf("NormalizeAgent(%q) = %q, %v", value, got, err)
		}
	}
	if got, err := NormalizeAgent(""); err != nil || got != DefaultAgent {
		t.Fatalf("NormalizeAgent(empty) = %q, %v", got, err)
	}
	for _, value := range []string{"codex --dangerously-bypass", "/tmp/agent", "other"} {
		if _, err := NormalizeAgent(value); !errors.Is(err, ErrInvalidAgent) {
			t.Errorf("NormalizeAgent(%q) error = %v, want ErrInvalidAgent", value, err)
		}
	}
}

func TestSaveRejectsPartialOrInvalidDeployment(t *testing.T) {
	for _, deployment := range []Deployment{
		{APIAddr: "https://2.example.test/api"},
		{APIAddr: "https://2.example.test/api", WSAddr: "wss://2.example.test/ws"},
		{APIAddr: "wss://2.example.test/api", WSAddr: "wss://2.example.test/ws", PlatformID: 7},
	} {
		item := validProfile()
		item.Deployment = deployment
		if err := Save(filepath.Join(t.TempDir(), "work.toml"), item); !errors.Is(err, ErrInvalidDeployment) {
			t.Errorf("Save(%#v) error = %v, want ErrInvalidDeployment", deployment, err)
		}
	}
}

func TestProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	want := validProfile()
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("Load() = %#v, %v; want %#v", got, err, want)
	}
}

func TestParseIdentity(t *testing.T) {
	for _, identity := range []Identity{IdentityUser, IdentityBot} {
		got, err := ParseIdentity(string(identity))
		if err != nil || got != identity {
			t.Fatalf("ParseIdentity(%q) = %q, %v", identity, got, err)
		}
	}
	if _, err := ParseIdentity("owner"); err == nil {
		t.Fatal("ParseIdentity() accepted an unknown identity")
	}
}

func validProfile() Profile {
	return Profile{
		Name:  "work",
		User:  Account{UserID: "owner", CredentialRef: "file:work-user"},
		Bot:   Account{UserID: "bot", CredentialRef: "file:work-bot"},
		Agent: DefaultAgent,
		Deployment: Deployment{
			APIAddr:    "https://example.test/api",
			WSAddr:     "wss://example.test/ws",
			PlatformID: 7,
		},
	}
}

func TestLockRejectsSecondWorkerForSameProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "abdim", "work", "daemon.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })
	if _, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second AcquireLock() error = %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() after release error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}
