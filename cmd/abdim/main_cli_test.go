package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestCLISelectsSDKIdentity(t *testing.T) {
	t.Setenv("ABDIM_PROFILE", "from-env")
	roots := testRoots(t)
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "from-env")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan contracts.Request, 2)
	server, err := ipc.Listen(paths.Socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		requests <- request
		return contracts.Response{
			APIVersion: contracts.APIVersionV1, RequestID: request.RequestID, OK: true,
			Data: json.RawMessage(`{"id":"self"}`), Meta: &contracts.Meta{ProfileID: "from-env"},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-done
	})

	for _, test := range []struct {
		args []string
		as   string
	}{
		{args: []string{"user", "me"}, as: "bot"},
		{args: []string{"--as", "user", "user", "me"}, as: "user"},
	} {
		var output bytes.Buffer
		if code := runWithIO(test.args, strings.NewReader(""), &output, roots); code != 0 {
			t.Fatalf("runWithIO(%v) = %d: %s", test.args, code, output.String())
		}
		request := <-requests
		if request.As != test.as || request.ProfileID != "from-env" || request.Method != "user.me" || string(request.Params) != "{}" {
			t.Fatalf("request = %#v", request)
		}
	}
}

func TestCLIHasNoDynamicCommandCatalog(t *testing.T) {
	var output bytes.Buffer
	if code := runWithIO([]string{"commands"}, strings.NewReader(""), &output, testRoots(t)); code == 0 {
		t.Fatalf("commands unexpectedly succeeded: %s", output.String())
	}
	if !strings.Contains(output.String(), "unsupported command") {
		t.Fatalf("commands output = %s", output.String())
	}
	for _, entry := range agentEnvironment("codex", "work") {
		if strings.HasPrefix(entry, "ABDIM_AGENT_") {
			t.Fatalf("private Agent environment remains: %q", entry)
		}
	}
}

func TestAgentEnvironmentPrependsCurrentBinaryDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := "PATH=" + filepath.Dir(executable) + string(os.PathListSeparator)
	for _, entry := range agentEnvironment("hermes", "work") {
		if strings.HasPrefix(entry, "PATH=") {
			if !strings.HasPrefix(entry, want) {
				t.Fatalf("Agent PATH = %q, want prefix %q", entry, want)
			}
			return
		}
	}
	t.Fatal("Agent environment has no PATH")
}
