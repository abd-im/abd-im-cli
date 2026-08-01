//go:build e2e && unix

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/launcher"
)

func TestProviderSeparateUIDDeploymentGate(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires a root-owned daemon process")
	}
	uid := providerDeploymentID(t, "ABDIM_E2E_PROVIDER_UID")
	gid := providerDeploymentID(t, "ABDIM_E2E_PROVIDER_GID")
	root, err := os.MkdirTemp("", "abdim-provider-isolation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "provider")
	codexHome := filepath.Join(home, ".codex")
	runRoot := filepath.Join(root, "runs")
	for _, path := range []string{home, codexHome} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			t.Fatal(err)
		}
	}
	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(authPath, int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runRoot, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runRoot, 0o711); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(root, "daemon-profile")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profileSecret := filepath.Join(profileDir, "control.db")
	if err := os.WriteFile(profileSecret, []byte("daemon-private"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(root, "daemon-runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ownerSocket := filepath.Join(runtimeDir, "daemon.sock")
	ownerListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: ownerSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerListener.Close()

	providerExecutable := providerDeploymentHelper(t, root)
	deployment, err := launcher.New(launcher.Config{UID: uid, GID: gid, Home: home, CodexHome: codexHome, CodexPath: providerExecutable, RunRoot: runRoot})
	if err != nil {
		t.Fatal(err)
	}
	workingDir, err := deployment.WorkingDir("work")
	if err != nil {
		t.Fatal(err)
	}
	nextRun := filepath.Join(workingDir, "run-next")
	if err := os.Mkdir(nextRun, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(workingDir, "run-private", "work", "helper.json")
	method := &providerMCPMethod{}
	tool, credential := providerMCPTool(t, method)
	adapter, err := codexprovider.New(codexprovider.Config{
		Executable:        deployment.CodexPath(),
		WorkingDir:        workingDir,
		Environment:       []string{providerMCPHelperEnv + "=1", "PATH=/usr/bin:/bin", "CODEX_HOME=" + deployment.CodexHome(), "ABDIM_PROVIDER_CAPTURE=" + capture, "ABDIM_PROVIDER_FORBIDDEN_PROFILE=" + profileSecret, "ABDIM_PROVIDER_FORBIDDEN_OWNER_SOCKET=" + ownerSocket, "ABDIM_PROVIDER_FORBIDDEN_NEXT_RUN=" + nextRun},
		BridgeCommand:     os.Args[0],
		Launcher:          deployment,
		InitializeTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.StartRequest{ProfileID: "work", RunID: "run-private", GrantCredential: credential, AllowedMethods: []string{"message.history"}, Proxy: tool}
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	run := filepath.Join(workingDir, request.RunID)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	assertProviderModeAndOwner(t, run, 0o711, 0, 0)
	assertProviderModeAndOwner(t, filepath.Join(run, "codex"), 0o700, uid, gid)
	assertProviderModeAndOwner(t, filepath.Join(run, "work"), 0o700, uid, gid)
	assertProviderModeAndOwner(t, filepath.Join(run, "mcp.sock"), 0o600, uid, gid)

	if result, err := session.Turn(context.Background(), contracts.TurnRequest{RunID: request.RunID, EventID: "event-1", GrantCredential: credential, Prompt: "reply"}); err != nil || result.FinalText != "final reply" {
		t.Fatalf("Turn() = %#v, %v", result, err)
	}
	helper := readProviderMCPHelper(t, capture)
	if helper.Err != "" || helper.UID != int(uid) || helper.CodexHome != filepath.Join(run, "codex") || helper.Home != filepath.Join(run, "work") || !helper.DeniedProfile || !helper.DeniedOwnerSock || !helper.DeniedNextRun {
		t.Fatalf("provider helper = %+v", helper)
	}
	if calls := method.Calls(); len(calls) != 1 || calls[0].Method != "message.history" || calls[0].Grant != credential {
		t.Fatalf("run-private proxy calls = %+v", calls)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Fatalf("run directory remains: %v", err)
	}
}

func providerDeploymentID(t *testing.T, name string) uint32 {
	t.Helper()
	value := os.Getenv(name)
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		t.Fatalf("%s must be a non-root numeric ID", name)
	}
	return uint32(parsed)
}

func providerDeploymentHelper(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(root, "provider-helper-test")
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(binary, 0, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "fake-codex")
	contents := fmt.Sprintf("#!/bin/sh\nexec %s -test.run '^TestProviderMCPHelper$' --\n", quoteProviderMCPHelper(binary))
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertProviderModeAndOwner(t *testing.T, path string, mode os.FileMode, uid, gid uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode().Perm() != mode || stat.Uid != uid || stat.Gid != gid {
		t.Fatalf("%s mode/owner = %o/%d:%d, want %o/%d:%d", path, info.Mode().Perm(), stat.Uid, stat.Gid, mode, uid, gid)
	}
}
