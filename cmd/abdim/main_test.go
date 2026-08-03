package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestLegacyManualSetupCommandsAreRemoved(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "import", "--token-stdin"},
		{"profile", "configure"},
		{"daemon", "verify"},
		{"daemon", "serve"},
	} {
		var output bytes.Buffer
		if got := runWithIO(args, strings.NewReader("secret-marker"), &output, testRoots(t)); got != 2 {
			t.Fatalf("runWithIO(%v) = %d, want 2", args, got)
		}
		if strings.Contains(output.String(), "secret-marker") {
			t.Fatalf("legacy command leaked input: %s", output.String())
		}
	}
}

func TestOwnerInboundPolicyGrantsVerifiedMethods(t *testing.T) {
	policy := pairedOwnerPolicy([]proxy.Method{{Name: "group.list", Scope: "group.read", Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}}})
	decision, allowed, err := policy.Decide(context.Background(), contracts.Event{})
	if err != nil || !allowed || !decision.FullAccess || len(decision.Methods) != 1 || decision.Methods[0] != "group.list" || decision.RateBudget != 64 {
		t.Fatalf("owner policy = %+v, allowed=%t, err=%v", decision, allowed, err)
	}
}

func TestDaemonSDKConfigUsesProfilePaths(t *testing.T) {
	paths, err := profile.NewPaths("/config", "/data", "/runtime", "work")
	if err != nil {
		t.Fatal(err)
	}
	config := daemonSDKConfig(paths, profile.Deployment{UserID: "user-1", APIAddr: "https://2.example.test/api", WSAddr: "wss://2.example.test/msg_gateway", PlatformID: 7})
	if config.PlatformID != 7 || config.ApiAddr != "https://2.example.test/api" || config.WsAddr != "wss://2.example.test/msg_gateway" || config.DataDir != paths.SDKDir || config.LogFilePath != filepath.Join(paths.LogsDir, "sdk.log") {
		t.Fatalf("daemonSDKConfig() = %#v", config)
	}
}

func TestCurrentCodexHomeUsesCallerConfiguration(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	got, err := currentCodexHome()
	if err != nil || got != home {
		t.Fatalf("currentCodexHome() = %q, %v", got, err)
	}

	t.Setenv("CODEX_HOME", "relative")
	if _, err := currentCodexHome(); err == nil {
		t.Fatal("currentCodexHome() accepted a relative path")
	}
}

func TestProviderMCPBridgeRelaysOnlyConfiguredSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "provider.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		line, err := bufio.NewReader(connection).ReadString('\n')
		if err == nil && line != `{"jsonrpc":"2.0","id":1}`+"\n" {
			err = errors.New("unexpected provider MCP input")
		}
		if err == nil {
			_, err = connection.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
		}
		serverDone <- err
	}()
	var output bytes.Buffer
	if got := runWithIO([]string{"mcp", "provider", "bridge", "--socket", socket}, strings.NewReader(`{"jsonrpc":"2.0","id":1}`+"\n"), &output, testRoots(t)); got != 0 {
		t.Fatalf("bridge exit code = %d", got)
	}
	if output.String() != `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n" {
		t.Fatalf("bridge output = %q", output.String())
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("bridge server error = %v", err)
	}
	if got := runWithIO([]string{"mcp", "provider", "bridge", "--socket", "relative.sock"}, strings.NewReader(""), &output, testRoots(t)); got != 2 {
		t.Fatalf("relative socket bridge exit code = %d", got)
	}
}

func TestOwnerQueryUsesOnlyRegisteredMethodOverLocalSocket(t *testing.T) {
	roots := testRoots(t)
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	requests := make(chan contracts.Request, 1)
	server, err := ipc.Listen(paths.Socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		requests <- request
		return contracts.Response{
			APIVersion: contracts.APIVersionV1,
			RequestID:  request.RequestID,
			OK:         true,
			Data:       json.RawMessage(`{"items":[]}`),
			Meta:       &contracts.Meta{ProfileID: "work", Schema: "abdim.service/v1"},
		}, nil
	})
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

	var output bytes.Buffer
	args := []string{"--profile", "work", "group", "members", "list", "--params-stdin"}
	if got := runWithIO(args, strings.NewReader(`{"group_id":"group-1","limit":20}`), &output, roots); got != 0 {
		t.Fatalf("runWithIO() = %d, want 0; output = %s", got, output.String())
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode CLI response: %v", err)
	}
	if !response.OK || string(response.Data) != `{"items":[]}` || response.Meta == nil || response.Meta.ProfileID != "work" {
		t.Fatalf("CLI response = %+v", response)
	}
	request := <-requests
	if request.Method != "group.members.list" || string(request.Params) != `{"group_id":"group-1","limit":20}` {
		t.Fatalf("daemon request = %+v", request)
	}
}

func TestOwnerQueryRejectsUnregisteredCommandsAndDaemonPaths(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "daemon", "shutdown"}, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("unregistered command exit = %d, want 2", got)
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("unregistered command response = %s, %v", output.String(), err)
	}

	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "profile", "get"}, strings.NewReader(""), &output, roots); got != 3 {
		t.Fatalf("unavailable daemon exit = %d, want 3", got)
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != contracts.CodeDaemonUnavailable || strings.Contains(output.String(), roots.runtimeDir) {
		t.Fatalf("daemon unavailable response = %s, %v", output.String(), err)
	}
}

func TestMCPServeUsesDefaultOwnerToolRegistry(t *testing.T) {
	roots := testRoots(t)
	input := `{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "mcp", "serve"}, strings.NewReader(input), &output, roots); got != 0 {
		t.Fatalf("runWithIO() = %d, want 0; output = %s", got, output.String())
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP response: %v", err)
	}
	if len(response.Result.Tools) != 26 || response.Result.Tools[0].Name == "abdim.daemon.shutdown" {
		t.Fatalf("MCP tools = %+v", response.Result.Tools)
	}
	foundRunCancel := false
	for _, tool := range response.Result.Tools {
		if tool.Name == "abdim.run.cancel" {
			foundRunCancel = true
			break
		}
	}
	if !foundRunCancel {
		t.Fatalf("MCP tools omit owner run.cancel: %+v", response.Result.Tools)
	}
}

func testRoots(t *testing.T) commandRoots {
	t.Helper()
	root := t.TempDir()
	return commandRoots{configDir: filepath.Join(root, "config"), dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime")}
}
