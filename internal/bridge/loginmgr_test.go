package bridge

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestLoginMgrOwnsOneSDKLifecycle(t *testing.T) {
	sdk := &testkit.FakeSDK{}
	manager, err := NewLoginMgr(func() contracts.SDK { return sdk }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.State() != StateReady {
		t.Fatalf("State() = %q", manager.State())
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}
	if got := sdk.Steps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK steps = %v, want %v", got, want)
	}
}

func TestLoginMgrShutsDownPartialSDKOnFailure(t *testing.T) {
	want := errors.New("connection unavailable")
	sdk := &testkit.FakeSDK{LoginErr: want}
	manager, err := NewLoginMgr(func() contracts.SDK { return sdk }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Start() error = %v", err)
	}
	if manager.State() != StateDegraded {
		t.Fatalf("State() = %q", manager.State())
	}
	if got := sdk.Steps(); got[len(got)-1] != "Shutdown" {
		t.Fatalf("SDK steps = %v", got)
	}
}
