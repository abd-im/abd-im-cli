package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestFramesRoundTripAndRejectInvalidLengths(t *testing.T) {
	var stream bytes.Buffer
	if err := WriteFrame(&stream, []byte(`{"request_id":"req-1"}`)); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	payload, err := ReadFrame(&stream)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if got, want := string(payload), `{"request_id":"req-1"}`; got != want {
		t.Fatalf("frame payload = %q, want %q", got, want)
	}
	if err := WriteFrame(&stream, nil); err == nil {
		t.Fatal("WriteFrame(nil) error = nil, want invalid size")
	}
}

func TestUnixSocketIsOwnerOnlyAndUsesV1Contracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "daemon.sock")
	server, err := Listen(path, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		return contracts.Response{
			APIVersion: contracts.APIVersionV1,
			RequestID:  request.RequestID,
			OK:         true,
			Data:       json.RawMessage(`{"method":"` + request.Method + `"}`),
			Meta:       &contracts.Meta{ProfileID: request.ProfileID},
		}, nil
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
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

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want owner-only socket", info.Mode())
	}
	request := contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: "req-1", ProfileID: "work", Method: "profile.get", Params: json.RawMessage(`{}`)}
	response, err := Call(context.Background(), path, request)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !response.OK || response.Meta.ProfileID != "work" {
		t.Fatalf("Call() response = %+v", response)
	}
}

func TestServerReturnsStableInvalidArgumentError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	server, err := Listen(path, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: request.RequestID, OK: true, Data: json.RawMessage(`{}`), Meta: &contracts.Meta{ProfileID: request.ProfileID}}, nil
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() { _ = server.Close() })

	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer connection.Close()
	if err := WriteFrame(connection, []byte(`{"api_version":"v1","request_id":"req-1"}`)); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	payload, err := ReadFrame(connection)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	var response contracts.Response
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("error response = %+v, want INVALID_ARGUMENT", response)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("invalid-request response violates v1 contract: %v", err)
	}
}
