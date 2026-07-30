package testkit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

func TestFakeSDKRecordsLifecycleAndEmitsEvent(t *testing.T) {
	sdk := &FakeSDK{}
	ctx := context.Background()
	if err := sdk.InitSDK(ctx); err != nil {
		t.Fatalf("InitSDK() error = %v", err)
	}
	if err := sdk.InitResources(ctx); err != nil {
		t.Fatalf("InitResources() error = %v", err)
	}

	gotEvents := make(chan contracts.Event, 1)
	if err := sdk.SetEventListener(func(_ context.Context, event contracts.Event) {
		gotEvents <- event
	}); err != nil {
		t.Fatalf("SetEventListener() error = %v", err)
	}
	if err := sdk.Login(ctx); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	event := contracts.Event{
		APIVersion: contracts.APIVersionV1,
		EventID:    "evt-1",
		ProfileID:  "work",
		Sequence:   1,
		Type:       string(contracts.EventMessageReceived),
		OccurredAt: time.Now().UTC(),
		Data:       json.RawMessage(`{}`),
	}
	if err := sdk.Emit(ctx, event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if got := <-gotEvents; got.EventID != event.EventID {
		t.Fatalf("emitted event ID = %q, want %q", got.EventID, event.EventID)
	}
	if err := sdk.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	wantSteps := []string{"InitSDK", "InitResources", "SetEventListener", "Login", "Shutdown"}
	if got := sdk.Steps(); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("Steps() = %v, want %v", got, wantSteps)
	}
}

func TestFakeProviderAndSessionRecordCalls(t *testing.T) {
	session := &FakeSession{TurnResults: []contracts.TurnResult{{FinalText: "done", SessionRef: "session-1"}}}
	provider := &FakeProvider{Session: session}

	started, err := provider.Start(context.Background(), contracts.StartRequest{ProfileID: "work", RunID: "run-1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := started.Turn(context.Background(), contracts.TurnRequest{RunID: "run-1", EventID: "evt-1"})
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if result.FinalText != "done" {
		t.Fatalf("Turn() final text = %q, want done", result.FinalText)
	}
	if err := started.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := started.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := provider.Starts(); len(got) != 1 || got[0].RunID != "run-1" {
		t.Fatalf("Starts() = %+v, want one run-1 start", got)
	}
	if got := session.Turns(); len(got) != 1 || got[0].EventID != "evt-1" {
		t.Fatalf("Turns() = %+v, want one evt-1 turn", got)
	}
	if session.CancelCount() != 1 || session.CloseCount() != 1 {
		t.Fatalf("lifecycle counts = cancel:%d close:%d, want 1 each", session.CancelCount(), session.CloseCount())
	}
}

func TestFakeProxyIsInMemoryAndCloses(t *testing.T) {
	response := &contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "req-1",
		OK:         true,
		Data:       json.RawMessage(`{}`),
		Meta:       &contracts.Meta{ProfileID: "work"},
	}
	proxy := &FakeProxy{Response: response}
	request := contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "req-1",
		ProfileID:  "work",
		Method:     "profile.get",
		Params:     json.RawMessage(`{}`),
	}
	if _, err := proxy.Call(context.Background(), request); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got := proxy.Calls(); len(got) != 1 || got[0].Method != "profile.get" {
		t.Fatalf("Calls() = %+v, want one profile.get call", got)
	}
	if err := proxy.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := proxy.Call(context.Background(), request); !errors.Is(err, ErrProxyClosed) {
		t.Fatalf("Call() after Close() error = %v, want ErrProxyClosed", err)
	}
}
