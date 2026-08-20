package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestRuntimeOwnsTwoSDKsUnderOneProcessLock(t *testing.T) {
	inbound, _, _, _, closeInbound := newInboundHarness(t)
	defer closeInbound()
	root := t.TempDir()
	user, bot := &testkit.FakeSDK{}, &testkit.FakeSDK{}
	runtime, err := NewRuntime(RuntimeConfig{
		UserSDKFactory: func() contracts.SDK { return user }, BotSDKFactory: func() contracts.SDK { return bot },
		LockFile: filepath.Join(root, "daemon.lock"), SocketPath: filepath.Join(root, "daemon.sock"), Inbound: inbound,
		Handler: func(context.Context, contracts.Request) (contracts.Response, error) { return contracts.Response{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.State() != bridge.StateReady {
		t.Fatalf("State() = %q", runtime.State())
	}
	if _, err := profile.AcquireLock(filepath.Join(root, "daemon.lock")); !errors.Is(err, profile.ErrLocked) {
		t.Fatalf("second process lock error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}
	if !reflect.DeepEqual(user.Steps(), want) || !reflect.DeepEqual(bot.Steps(), want) {
		t.Fatalf("SDK lifecycles = user:%v bot:%v", user.Steps(), bot.Steps())
	}
	lock, err := profile.AcquireLock(filepath.Join(root, "daemon.lock"))
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	_ = lock.Release()
}
