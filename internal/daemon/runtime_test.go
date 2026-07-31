package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestRuntimeServesOnlyAfterSDKIsReadyAndShutsDownInOrder(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.store.Close()
	dispatcher, err := NewDispatcher("work", []OwnerMethod{{
		Name: "profile.get",
		Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
			return OwnerResult{Data: map[string]string{"id": "work"}, Meta: contracts.Meta{ProfileID: "work"}}, nil
		},
	}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	root := t.TempDir()
	socket := filepath.Join(root, "runtime", "daemon.sock")
	sdk := &testkit.FakeSDK{}
	runtime, err := NewRuntime(RuntimeConfig{
		SDKFactory: func() contracts.SDK { return sdk },
		LockFile:   filepath.Join(root, "runtime", "daemon.lock"),
		SocketPath: socket,
		Inbound:    harness.inbound,
		Handler:    dispatcher.Handle,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx) }()
	waitForSocket(t, socket)
	response, err := ipc.Call(context.Background(), socket, ownerRequest("profile.get", `{}`))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !response.OK || string(response.Data) != `{"id":"work"}` || runtime.State() != bridge.StateReady {
		t.Fatalf("runtime response/state = %+v/%q", response, runtime.State())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
	if got, want := sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK lifecycle = %v, want %v", got, want)
	}
}

func TestRuntimeClosesSDKWhenSocketCannotBeCreated(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.store.Close()
	root := t.TempDir()
	sdk := &testkit.FakeSDK{}
	runtime, err := NewRuntime(RuntimeConfig{
		SDKFactory: func() contracts.SDK { return sdk },
		LockFile:   filepath.Join(root, "daemon.lock"),
		SocketPath: root,
		Inbound:    harness.inbound,
		Handler: func(context.Context, contracts.Request) (contracts.Response, error) {
			return contracts.Response{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("Start() accepted a directory as the socket path")
	}
	if runtime.State() != bridge.StateStopped {
		t.Fatalf("State() = %q, want stopped", runtime.State())
	}
	if got, want := sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK lifecycle = %v, want %v", got, want)
	}
	if _, err := harness.inbound.Process(context.Background(), inboundEvent("after-start-failure", "conversation-1", "message-1")); !errors.Is(err, ErrStopped) {
		t.Fatalf("inbound remains active after failed runtime start: %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestNewRuntimeRequiresComposedDependencies(t *testing.T) {
	if _, err := NewRuntime(RuntimeConfig{}); err == nil {
		t.Fatal("NewRuntime() accepted empty configuration")
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("socket %q was not created", path)
}
