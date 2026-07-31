package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestJSONAndJSONLOutputUseSharedEnvelope(t *testing.T) {
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: "req-1", OK: true, Data: json.RawMessage(`{"items":[]}`), Meta: &contracts.Meta{ProfileID: "work"}}
	for _, output := range []Output{OutputJSON, OutputJSONL} {
		var buffer bytes.Buffer
		if err := WriteResponse(&buffer, output, response); err != nil {
			t.Fatalf("WriteResponse(%q) error = %v", output, err)
		}
		var got contracts.Response
		if err := json.Unmarshal(buffer.Bytes(), &got); err != nil {
			t.Fatalf("WriteResponse(%q) wrote invalid JSON: %v", output, err)
		}
		if got.RequestID != "req-1" || !got.OK {
			t.Fatalf("WriteResponse(%q) = %+v", output, got)
		}
	}
	if got := ExitCode(ErrorResponse("req-1", contracts.CodeDaemonNotReady, nil)); got != 3 {
		t.Fatalf("ExitCode(DAEMON_NOT_READY) = %d, want 3", got)
	}
}

func TestAuthImportPersistsReferenceButNeverToken(t *testing.T) {
	const token = "test-token-marker-4d2a0d"
	root := t.TempDir()
	response, err := ImportToken(context.Background(), strings.NewReader(token), AuthImportOptions{
		ProfileName: "work", ConfigDir: root + "/config", DataDir: root + "/data", RuntimeDir: root + "/runtime", AllowPlaintext: true, RequestID: "req-1",
	})
	if err != nil {
		t.Fatalf("ImportToken() error = %v", err)
	}
	if !response.OK || strings.Contains(string(response.Data), token) {
		t.Fatalf("ImportToken() response = %+v", response)
	}
	if _, err := ImportToken(context.Background(), strings.NewReader(token), AuthImportOptions{ProfileName: "work", ConfigDir: root, DataDir: root, RuntimeDir: root, RequestID: "req-2"}); err == nil {
		t.Fatal("ImportToken() without plaintext opt-in error = nil")
	}
}

func TestAuthImportPreservesExistingDeployment(t *testing.T) {
	root := t.TempDir()
	options := AuthImportOptions{
		ProfileName: "work", ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "runtime"), AllowPlaintext: true, RequestID: "req-1",
	}
	if _, err := ImportToken(context.Background(), strings.NewReader("first-token"), options); err != nil {
		t.Fatalf("first ImportToken() error = %v", err)
	}
	paths, err := profile.NewPaths(options.ConfigDir, options.DataDir, options.RuntimeDir, options.ProfileName)
	if err != nil {
		t.Fatal(err)
	}
	want := profile.Deployment{UserID: "user-1", APIAddr: "https://2.example.test/api", WSAddr: "wss://2.example.test/msg_gateway", PlatformID: 7}
	if _, err := profile.Configure(paths.ConfigFile, want); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if _, err := ImportToken(context.Background(), strings.NewReader("replacement-token"), options); err != nil {
		t.Fatalf("replacement ImportToken() error = %v", err)
	}
	got, err := profile.Load(paths.ConfigFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Deployment != want {
		t.Fatalf("deployment after import = %#v, want %#v", got.Deployment, want)
	}
}
