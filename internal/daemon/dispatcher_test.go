package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
)

func TestDispatcherCallsOnlyRegisteredTypedMethod(t *testing.T) {
	var received json.RawMessage
	dispatcher, err := NewDispatcher("work", []OwnerMethod{{
		Name: "profile.get",
		Handle: func(_ context.Context, params json.RawMessage) (OwnerResult, error) {
			received = append(json.RawMessage(nil), params...)
			return OwnerResult{
				Data: map[string]string{"id": "work"},
				Meta: contracts.Meta{ProfileID: "work", Schema: "abdim.service/v1"},
			}, nil
		},
	}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	response, err := dispatcher.Handle(context.Background(), ownerRequest("profile.get", `{"detail":true}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !response.OK || response.Meta == nil || response.Meta.ProfileID != "work" || string(response.Data) != `{"id":"work"}` {
		t.Fatalf("Handle() response = %+v", response)
	}
	if string(received) != `{"detail":true}` {
		t.Fatalf("handler params = %s", received)
	}
}

func TestDispatcherRejectsInvalidProfileAndUnknownMethod(t *testing.T) {
	calls := 0
	dispatcher, err := NewDispatcher("work", []OwnerMethod{{
		Name: "profile.get",
		Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
			calls++
			return OwnerResult{}, nil
		},
	}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	wrongProfile := ownerRequest("profile.get", `{}`)
	wrongProfile.ProfileID = "other"
	for _, request := range []contracts.Request{wrongProfile, ownerRequest("daemon.shutdown", `{}`)} {
		response, err := dispatcher.Handle(context.Background(), request)
		if err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if response.OK || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
			t.Fatalf("Handle() response = %+v", response)
		}
	}
	if calls != 0 {
		t.Fatalf("registered handler calls = %d, want 0", calls)
	}
}

func TestDispatcherDoesNotExposeServiceFailuresOrInvalidResults(t *testing.T) {
	const secret = "token-marker-must-not-leak"
	dispatcher, err := NewDispatcher("work", []OwnerMethod{
		{
			Name: "profile.get",
			Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
				return OwnerResult{}, errors.New(secret)
			},
		},
		{
			Name: "profile.bad-meta",
			Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
				return OwnerResult{Data: map[string]string{"secret": secret}, Meta: contracts.Meta{ProfileID: "other"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	for _, method := range []string{"profile.get", "profile.bad-meta"} {
		response, err := dispatcher.Handle(context.Background(), ownerRequest(method, `{}`))
		if err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if response.OK || response.Error == nil || response.Error.Code != contracts.CodeInternal {
			t.Fatalf("Handle() response = %+v", response)
		}
		payload, marshalErr := json.Marshal(response)
		if marshalErr != nil || strings.Contains(string(payload), secret) {
			t.Fatalf("unsafe response = %s, marshal error = %v", payload, marshalErr)
		}
	}
}

func TestDispatcherReturnsRegisteredStableFailures(t *testing.T) {
	dispatcher, err := NewDispatcher("work", []OwnerMethod{{
		Name: "conversation.get",
		Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
			return OwnerResult{}, MethodFailure(contracts.CodeInvalidArgument, "conversation ID is required", false)
		},
	}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	response, err := dispatcher.Handle(context.Background(), ownerRequest("conversation.get", `{}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.OK || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument || response.Error.Message != "conversation ID is required" {
		t.Fatalf("Handle() response = %+v", response)
	}
}

func TestDispatcherServesTheLocalRPCContract(t *testing.T) {
	dispatcher, err := NewDispatcher("work", []OwnerMethod{{
		Name: "profile.get",
		Handle: func(context.Context, json.RawMessage) (OwnerResult, error) {
			return OwnerResult{
				Data: map[string]string{"id": "work"},
				Meta: contracts.Meta{ProfileID: "work", Schema: "abdim.service/v1"},
			}, nil
		},
	}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "daemon.sock")
	server, err := ipc.Listen(path, dispatcher.Handle)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not stop")
		}
	})

	response, err := ipc.Call(context.Background(), path, ownerRequest("profile.get", `{}`))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !response.OK || string(response.Data) != `{"id":"work"}` || response.Meta == nil || response.Meta.ProfileID != "work" {
		t.Fatalf("Call() response = %+v", response)
	}
}

func TestNewDispatcherValidatesStaticRegistry(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (OwnerResult, error) { return OwnerResult{}, nil }
	for _, input := range []struct {
		profile string
		methods []OwnerMethod
	}{
		{"", nil},
		{"work", []OwnerMethod{{Name: "profile.get"}}},
		{"work", []OwnerMethod{{Name: "profile.get", Handle: handler}, {Name: "profile.get", Handle: handler}}},
	} {
		if _, err := NewDispatcher(input.profile, input.methods); err == nil {
			t.Fatalf("NewDispatcher(%q, %+v) succeeded", input.profile, input.methods)
		}
	}
}

func ownerRequest(method, params string) contracts.Request {
	return contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "request-1",
		ProfileID:  "work",
		Method:     method,
		Params:     json.RawMessage(params),
	}
}
