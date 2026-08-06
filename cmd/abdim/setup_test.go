package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/connector"
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
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Save(paths.ConfigFile, profile.Profile{
		Name: "default", CredentialRef: "file:default", InboundToolsEnabled: true,
		Deployment: profile.Deployment{UserID: "old-bot", APIAddr: "https://example.test/api", WSAddr: "wss://example.test/ws", PlatformID: 7},
	}); err != nil {
		t.Fatal(err)
	}
	const token = "token-marker-must-not-leak"
	started := false
	dependencies := setupDependencies{
		endpoints: connector.ABDEndpoints{APIAddr: "http://127.0.0.1:10002", WSAddr: "ws://127.0.0.1:10001"},
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
	}
	var output, prompts bytes.Buffer
	if got := runSetupWith(context.Background(), nil, strings.NewReader("15500000000\n\npassword\n"), &output, &prompts, roots, "default", dependencies); got != 0 {
		t.Fatalf("runSetupWith() = %d: %s", got, output.String())
	}
	if !started || output.String() != "Setup complete. abdim is running (pid 42).\n" || strings.Contains(output.String(), token) || strings.Contains(output.String(), "password") {
		t.Fatalf("setup output = %q, started=%t", output.String(), started)
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || item.Deployment.UserID != "bot-user" || item.Deployment.APIAddr != "http://127.0.0.1:10002" || item.Deployment.WSAddr != "ws://127.0.0.1:10001" || !item.InboundToolsEnabled || item.Agent != "codex" {
		t.Fatalf("profile = %#v, %v", item, err)
	}
	profileContents, _ := os.ReadFile(paths.ConfigFile)
	if strings.Contains(string(profileContents), token) || strings.Contains(string(profileContents), "pairing") || strings.Contains(string(profileContents), "owner_user_id") {
		t.Fatalf("profile leaked setup secret: %s", profileContents)
	}
}

func TestSetupAgentAcceptsOnlyAllowlistedProviderID(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "codex"},
		{[]string{"--agent", "codex"}, "codex"},
		{[]string{"--agent", "hermes"}, "hermes"},
		{[]string{"--agent", "openclaw"}, "openclaw"},
	} {
		got, _, err := setupAgent(test.args)
		if err != nil || got != test.want {
			t.Errorf("setupAgent(%v) = %q, %v", test.args, got, err)
		}
	}
	if _, _, err := setupAgent([]string{"--agent", "custom --flag"}); err == nil {
		t.Fatal("setupAgent accepted arbitrary command")
	}
}
