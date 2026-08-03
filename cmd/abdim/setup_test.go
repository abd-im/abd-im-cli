package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestPromptAccountDefaultsPhoneAreaCodeAndKeepsPasswordOutOfPrompts(t *testing.T) {
	var prompts bytes.Buffer
	account, areaCode, password, err := promptAccount(strings.NewReader("15500000000\n\nsecret-marker\n"), &prompts)
	if err != nil || account != "15500000000" || areaCode != "+86" || password != "secret-marker" {
		t.Fatalf("promptAccount() = %q, %q, %q, %v", account, areaCode, password, err)
	}
	if strings.Contains(prompts.String(), password) {
		t.Fatalf("prompts leaked password: %q", prompts.String())
	}
}

func TestRunSetupPersistsTokenFreeProfileAndStartsDaemon(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
	// setup only checks PATH resolution; the fake lifecycle does not execute it.
	codex := filepath.Join(strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0], "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	roots := testRoots(t)
	const token = "token-marker-must-not-leak"
	started := false
	dependencies := setupDependencies{
		login: func(_ context.Context, account, areaCode, password string) (string, string, error) {
			if account != "15500000000" || areaCode != "+86" || password != "password" {
				t.Fatalf("login input = %q, %q, %q", account, areaCode, password)
			}
			return "bot-user", token, nil
		},
		stop: func(context.Context, commandRoots, string) (bool, error) { return false, nil },
		start: func(context.Context, commandRoots, string) (daemonProcessStatus, bool, error) {
			started = true
			return daemonProcessStatus{State: "ready", PID: 42}, true, nil
		},
		now:    func() time.Time { return time.Unix(1000, 0) },
		random: bytes.NewReader([]byte{0xa1, 0xb2, 0xc3, 0xd4}),
	}
	var output, prompts bytes.Buffer
	if got := runSetupWith(context.Background(), nil, strings.NewReader("15500000000\n\npassword\n"), &output, &prompts, roots, "default", dependencies); got != 0 {
		t.Fatalf("runSetupWith() = %d: %s", got, output.String())
	}
	if !started || !strings.Contains(output.String(), "pair A1B2C3D4") || strings.Contains(output.String(), token) || strings.Contains(output.String(), "password") {
		t.Fatalf("setup output = %q, started=%t", output.String(), started)
	}
	paths, _ := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "default")
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || item.Deployment.UserID != "bot-user" || !item.Pairing.Pending(time.Unix(1001, 0)) {
		t.Fatalf("profile = %#v, %v", item, err)
	}
	profileContents, _ := os.ReadFile(paths.ConfigFile)
	if strings.Contains(string(profileContents), token) || strings.Contains(string(profileContents), "A1B2C3D4") {
		t.Fatalf("profile leaked setup secret: %s", profileContents)
	}
}
