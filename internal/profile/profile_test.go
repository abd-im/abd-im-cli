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

func TestImportTokenReadsOnlyInputAndStoresOpaqueReference(t *testing.T) {
	const token = "test-token-marker-4d2a0d"
	root := t.TempDir()
	paths, err := NewPaths(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "runtime"), "work")
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	store, err := NewFileStore(filepath.Join(root, "data"), true)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	profile, err := ImportToken(context.Background(), strings.NewReader(token+"\n"), store, Profile{Name: "work"})
	if err != nil {
		t.Fatalf("ImportToken() error = %v", err)
	}
	if profile.CredentialRef != "file:work" {
		t.Fatalf("CredentialRef = %q, want file:work", profile.CredentialRef)
	}
	if err := Save(paths.ConfigFile, profile); err != nil {
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
	if loaded != profile {
		t.Fatalf("Load() = %#v, want %#v", loaded, profile)
	}
	restored, err := store.Get(context.Background(), loaded.CredentialRef)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(restored) != token {
		t.Fatalf("Get() token = %q, want marker", restored)
	}

	disabled, err := NewFileStore(filepath.Join(root, "other-data"), false)
	if err != nil {
		t.Fatalf("NewFileStore(disabled) error = %v", err)
	}
	if _, err := ImportToken(context.Background(), strings.NewReader(token), disabled, Profile{Name: "work"}); !errors.Is(err, ErrPlaintextDisabled) {
		t.Fatalf("disabled ImportToken() error = %v, want ErrPlaintextDisabled", err)
	}

	info, err := os.Stat(filepath.Join(root, "data", "abdim", "credentials", "work.token"))
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions = %o, want 600", got)
	}
}

func TestImportTokenReadsOneLine(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(filepath.Join(root, "data"), true)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	item, err := ImportToken(context.Background(), strings.NewReader("first-token\nignored-input"), store, Profile{Name: "work"})
	if err != nil {
		t.Fatalf("ImportToken() error = %v", err)
	}
	token, err := store.Get(context.Background(), item.CredentialRef)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(token) != "first-token" {
		t.Fatalf("stored token = %q, want first line", token)
	}
}

func TestConfigurePersistsNonSecretDeployment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.toml")
	if err := Save(path, Profile{Name: "work", CredentialRef: "file:work"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	deployment := Deployment{
		UserID:     "user-1",
		APIAddr:    "https://2.example.test/api",
		WSAddr:     "wss://2.example.test/msg_gateway",
		PlatformID: 7,
	}
	configured, err := Configure(path, deployment)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if configured.Deployment != deployment || configured.CredentialRef != "file:work" {
		t.Fatalf("Configure() = %#v", configured)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != configured {
		t.Fatalf("Load() = %#v, want %#v", loaded, configured)
	}
}

func TestSaveRejectsPartialOrInvalidDeployment(t *testing.T) {
	for _, deployment := range []Deployment{
		{UserID: "user-1"},
		{UserID: "user-1", APIAddr: "wss://2.example.test/api", WSAddr: "wss://2.example.test/ws", PlatformID: 7},
	} {
		if err := Save(filepath.Join(t.TempDir(), "work.toml"), Profile{Name: "work", CredentialRef: "file:work", Deployment: deployment}); !errors.Is(err, ErrInvalidDeployment) {
			t.Errorf("Save(%#v) error = %v, want ErrInvalidDeployment", deployment, err)
		}
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
