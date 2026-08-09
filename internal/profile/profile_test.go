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
	for _, path := range []string{filepath.Dir(paths.ConfigFile), paths.DataDir, paths.SDKDir, paths.AttachmentsDir, paths.LogsDir, paths.RuntimeDir} {
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

func TestAttachmentPathAcceptsOnlyOpaqueReferences(t *testing.T) {
	root := t.TempDir()
	paths, err := NewPaths(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "runtime"), "work")
	if err != nil {
		t.Fatal(err)
	}
	path, err := paths.AttachmentPath("a1b2c3d4e5f6g7h8")
	if err != nil {
		t.Fatalf("AttachmentPath() error = %v", err)
	}
	if path != filepath.Join(paths.AttachmentsDir, "a1b2c3d4e5f6g7h8") {
		t.Fatalf("AttachmentPath() = %q", path)
	}
	for _, reference := range []string{"", "short", "../secret", "/tmp/secret", "reference/child", `reference\\child`} {
		if _, err := paths.AttachmentPath(reference); err == nil {
			t.Errorf("AttachmentPath(%q) error = nil", reference)
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
	reference, err := store.Put(context.Background(), "work", []byte(token))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if reference != "file:work" {
		t.Fatalf("reference = %q, want file:work", reference)
	}
	item := Profile{Name: "work", CredentialRef: reference, InboundToolsEnabled: true, Agent: DefaultAgent}
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
	restored, err := store.Get(context.Background(), loaded.CredentialRef)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(restored) != token {
		t.Fatalf("Get() token = %q, want marker", restored)
	}

	info, err := os.Stat(filepath.Join(root, "data", "abdim", "credentials", "work.token"))
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions = %o, want 600", got)
	}
}

func TestLoadIgnoresRemovedPairingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	contents := "name = \"work\"\ncredential_ref = \"file:work\"\nuser_id = \"bot-user\"\napi_addr = \"https://example.test/api\"\nchat_api_addr = \"https://example.test/chat\"\nws_addr = \"wss://example.test/ws\"\nplatform_id = 7\npairing_code_hash = \"legacy\"\npairing_expires_at = 1\nowner_user_id = \"legacy-owner\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Name != "work" || loaded.Deployment.UserID != "bot-user" || loaded.InboundToolsEnabled || loaded.Agent != DefaultAgent {
		t.Fatalf("Load() = %#v, %v", loaded, err)
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
		{UserID: "user-1"},
		{UserID: "user-1", APIAddr: "https://2.example.test/api", WSAddr: "wss://2.example.test/ws", PlatformID: 7},
		{UserID: "user-1", APIAddr: "https://2.example.test/api", ChatAPIAddr: "wss://2.example.test/chat", WSAddr: "wss://2.example.test/ws", PlatformID: 7},
	} {
		if err := Save(filepath.Join(t.TempDir(), "work.toml"), Profile{Name: "work", CredentialRef: "file:work", Deployment: deployment}); !errors.Is(err, ErrInvalidDeployment) {
			t.Errorf("Save(%#v) error = %v, want ErrInvalidDeployment", deployment, err)
		}
	}
}

func TestProfileRoundTripIncludesChatAPIAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	want := Profile{
		Name:          "work",
		CredentialRef: "file:work",
		Agent:         DefaultAgent,
		Deployment: Deployment{
			UserID:      "bot-user",
			APIAddr:     "https://example.test/api",
			ChatAPIAddr: "https://example.test/chat",
			WSAddr:      "wss://example.test/ws",
			PlatformID:  7,
		},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("Load() = %#v, %v; want %#v", got, err, want)
	}
}

func TestLoadRejectsProfileMissingChatAPIAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	contents := "name = \"work\"\ncredential_ref = \"file:work\"\nuser_id = \"bot-user\"\napi_addr = \"https://example.test/api\"\nws_addr = \"wss://example.test/ws\"\nplatform_id = 7\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if !errors.Is(err, ErrInvalidDeployment) || !strings.Contains(err.Error(), "Chat API address is required") {
		t.Fatalf("Load() error = %v", err)
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
