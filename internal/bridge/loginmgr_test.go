package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/profile"
	"github.com/abd-im-cli/abdim-cli/internal/testkit"
)

func TestLoginMgrInitializesNewSDKInRequiredOrder(t *testing.T) {
	sdk := &testkit.FakeSDK{}
	var factoryCalls int
	events := make(chan contracts.Event, 1)
	manager, err := NewLoginMgr(func() contracts.SDK {
		factoryCalls++
		return sdk
	}, filepath.Join(t.TempDir(), "work.lock"), func(_ context.Context, event contracts.Event) {
		events <- event
	})
	if err != nil {
		t.Fatalf("NewLoginMgr() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if got, want := sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK steps = %v, want %v", got, want)
	}
	if manager.State() != StateReady {
		t.Fatalf("State() = %q, want ready", manager.State())
	}

	event := contracts.Event{
		APIVersion: contracts.APIVersionV1,
		EventID:    "evt-1",
		ProfileID:  "work",
		Sequence:   1,
		Type:       string(contracts.EventMessageReceived),
		OccurredAt: time.Now(),
		Data:       json.RawMessage(`{}`),
	}
	if err := sdk.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if got := <-events; got.EventID != event.EventID {
		t.Fatalf("event ID = %q, want %q", got.EventID, event.EventID)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if manager.State() != StateStopped {
		t.Fatalf("State() = %q, want stopped", manager.State())
	}
	if got, want := sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK steps after shutdown = %v, want %v", got, want)
	}
}

func TestLoginMgrReportsLockedProfile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "work.lock")
	lock, err := profile.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	manager, err := NewLoginMgr(func() contracts.SDK { return &testkit.FakeSDK{} }, lockPath, nil)
	if err != nil {
		t.Fatalf("NewLoginMgr() error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, profile.ErrLocked) {
		t.Fatalf("Start() error = %v, want ErrLocked", err)
	}
	if manager.State() != StateLocked {
		t.Fatalf("State() = %q, want locked", manager.State())
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() after lock conflict error = %v", err)
	}
}

func TestLoginMgrDegradesAndReleasesLockOnFailure(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "work.lock")
	want := errors.New("connection unavailable")
	sdk := &testkit.FakeSDK{LoginErr: want}
	manager, err := NewLoginMgr(func() contracts.SDK { return sdk }, lockPath, nil)
	if err != nil {
		t.Fatalf("NewLoginMgr() error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Start() error = %v, want %v", err, want)
	}
	if manager.State() != StateDegraded || !errors.Is(manager.Err(), want) {
		t.Fatalf("state/error = %q/%v, want degraded/%v", manager.State(), manager.Err(), want)
	}
	if got, want := sdk.Steps(), []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK steps after failed Login = %v, want %v", got, want)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	lock, err := profile.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("lock was not released after failure: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}
