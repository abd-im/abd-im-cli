package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestInboundToolsCommandPersistsStoppedProfile(t *testing.T) {
	roots := testRoots(t)
	paths := saveInboundToolsTestProfile(t, roots, false)
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "inbound", "tools", "enable"}, strings.NewReader(""), &output, roots); got != 0 {
		t.Fatalf("enable exit code = %d: %s", got, output.String())
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || !item.InboundToolsEnabled {
		t.Fatalf("enabled profile = %#v, %v", item, err)
	}
	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "inbound", "tools", "status"}, strings.NewReader(""), &output, roots); got != 0 || !strings.Contains(output.String(), "enabled for all direct-message senders") {
		t.Fatalf("status exit code = %d, output = %q", got, output.String())
	}
	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "inbound", "tools", "disable"}, strings.NewReader(""), &output, roots); got != 0 {
		t.Fatalf("disable exit code = %d: %s", got, output.String())
	}
	item, err = profile.Load(paths.ConfigFile)
	if err != nil || item.InboundToolsEnabled {
		t.Fatalf("disabled profile = %#v, %v", item, err)
	}
}

func TestInboundToolsCommandRestartsRunningDaemon(t *testing.T) {
	roots := testRoots(t)
	paths := saveInboundToolsTestProfile(t, roots, false)
	stopped, started := false, false
	dependencies := inboundToolsDependencies{
		status: func(context.Context, profile.Paths) (daemonProcessStatus, error) {
			return daemonProcessStatus{State: "ready", PID: 41}, nil
		},
		stop: func(context.Context, commandRoots, string) (bool, error) {
			stopped = true
			return true, nil
		},
		start: func(context.Context, commandRoots, string) (daemonProcessStatus, bool, error) {
			started = true
			return daemonProcessStatus{State: "ready", PID: 42}, true, nil
		},
	}
	var output bytes.Buffer
	if got := runInboundToolsWith(context.Background(), []string{"enable"}, &output, roots, "work", dependencies); got != 0 {
		t.Fatalf("enable exit code = %d: %s", got, output.String())
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || !item.InboundToolsEnabled || !stopped || !started || !strings.Contains(output.String(), "restarted (pid 42)") {
		t.Fatalf("profile=%#v err=%v stopped=%t started=%t output=%q", item, err, stopped, started, output.String())
	}
}

func TestInboundToolsCommandRequiresConfiguredProfile(t *testing.T) {
	var output bytes.Buffer
	dependencies := inboundToolsDependencies{
		status: func(context.Context, profile.Paths) (daemonProcessStatus, error) {
			return daemonProcessStatus{}, errors.New("unused")
		},
	}
	if got := runInboundToolsWith(context.Background(), []string{"enable"}, &output, testRoots(t), "work", dependencies); got != 1 || !strings.Contains(output.String(), "run 'abdim setup'") {
		t.Fatalf("exit code = %d, output = %q", got, output.String())
	}
}

func TestInboundToolsCommandRollsBackSettingWhenStopFails(t *testing.T) {
	roots := testRoots(t)
	paths := saveInboundToolsTestProfile(t, roots, true)
	dependencies := inboundToolsDependencies{
		status: func(context.Context, profile.Paths) (daemonProcessStatus, error) {
			return daemonProcessStatus{State: "ready", PID: 41}, nil
		},
		stop: func(context.Context, commandRoots, string) (bool, error) {
			return false, errors.New("stop failed")
		},
	}
	var output bytes.Buffer
	if got := runInboundToolsWith(context.Background(), []string{"disable"}, &output, roots, "work", dependencies); got != 1 {
		t.Fatalf("disable exit code = %d, output = %q", got, output.String())
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || !item.InboundToolsEnabled {
		t.Fatalf("rolled back profile = %#v, %v", item, err)
	}
}

func saveInboundToolsTestProfile(t *testing.T, roots commandRoots, enabled bool) profile.Paths {
	t.Helper()
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	if err != nil {
		t.Fatal(err)
	}
	item := profile.Profile{
		Name: "work", CredentialRef: "file:work", InboundToolsEnabled: enabled,
		Deployment: profile.Deployment{UserID: "bot-user", APIAddr: "https://example.test/api", ChatAPIAddr: "https://example.test/chat", WSAddr: "wss://example.test/ws", PlatformID: 7},
	}
	if err := profile.Save(paths.ConfigFile, item); err != nil {
		t.Fatal(err)
	}
	return paths
}
