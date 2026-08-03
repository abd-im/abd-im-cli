package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestStatusUsesDaemonTypedServiceAndHandlesUnconfiguredState(t *testing.T) {
	roots := shortLifecycleRoots(t)
	var output bytes.Buffer
	if got := runStatus(context.Background(), nil, &output, roots, "work"); got != 0 || !strings.Contains(output.String(), "not configured") {
		t.Fatalf("unconfigured status = %d, %q", got, output.String())
	}

	paths, _ := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	server, err := ipc.Listen(paths.Socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		data, _ := json.Marshal(daemonProcessStatus{State: "ready", PID: 42})
		return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: request.RequestID, OK: true, Data: data, Meta: &contracts.Meta{ProfileID: "work"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		_ = server.Close()
		<-done
	}()
	output.Reset()
	if got := runStatus(context.Background(), nil, &output, roots, "work"); got != 0 || output.String() != "abdim is ready (pid 42).\n" {
		t.Fatalf("running status = %d, %q", got, output.String())
	}
}

func TestStopSignalsOnlyPIDReturnedByOwnerSocket(t *testing.T) {
	command := exec.Command("sleep", "30")
	if err := command.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	roots := shortLifecycleRoots(t)
	paths, _ := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	server, err := ipc.Listen(paths.Socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		data, _ := json.Marshal(daemonProcessStatus{State: "ready", PID: command.Process.Pid})
		return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: request.RequestID, OK: true, Data: data, Meta: &contracts.Meta{ProfileID: "work"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	go func() {
		_ = command.Wait()
		cancel()
		_ = server.Close()
	}()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	stopped, err := stopDaemon(stopCtx, roots, "work")
	if err != nil || !stopped {
		t.Fatalf("stopDaemon() = %t, %v", stopped, err)
	}
	<-done
}

func shortLifecycleRoots(t *testing.T) commandRoots {
	t.Helper()
	root, err := os.MkdirTemp("", "ad-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return commandRoots{configDir: filepath.Join(root, "c"), dataDir: filepath.Join(root, "d"), runtimeDir: filepath.Join(root, "r")}
}
