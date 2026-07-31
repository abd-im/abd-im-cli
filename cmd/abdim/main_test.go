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

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestAuthImportUsesStdinAndEmitsTokenFreeJSON(t *testing.T) {
	const token = "test-token-marker-4d2a0d"
	root := t.TempDir()
	var output bytes.Buffer
	args := []string{"--profile", "work", "auth", "import", "--token-stdin", "--allow-plaintext-credentials"}
	if got := runWithIO(args, strings.NewReader(token+"\n"), &output, commandRoots{configDir: filepath.Join(root, "config"), dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime")}); got != 0 {
		t.Fatalf("runWithIO() = %d, want 0; output = %s", got, output.String())
	}
	if strings.Contains(output.String(), token) {
		t.Fatalf("CLI output leaked token marker: %q", output.String())
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("CLI output is not JSON: %v", err)
	}
	if !response.OK || response.Meta == nil || response.Meta.ProfileID != "work" {
		t.Fatalf("CLI response = %+v", response)
	}
	profileFile := filepath.Join(root, "config", "abdim", "profiles", "work.toml")
	contents, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatalf("read profile file: %v", err)
	}
	if strings.Contains(string(contents), token) {
		t.Fatalf("profile file leaked token marker: %q", contents)
	}
}

func TestRunRejectsTokenFlagsAndRequiresExplicitFallback(t *testing.T) {
	var output bytes.Buffer
	if got := runWithIO([]string{"auth", "import", "--token=secret"}, strings.NewReader("token"), &output, commandRoots{configDir: t.TempDir(), dataDir: t.TempDir(), runtimeDir: t.TempDir()}); got != 2 {
		t.Fatalf("token flag exit code = %d, want 2", got)
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatalf("CLI output leaked argv token: %q", output.String())
	}
	output.Reset()
	if got := runWithIO([]string{"auth", "import", "--token-stdin"}, strings.NewReader("token"), &output, commandRoots{configDir: t.TempDir(), dataDir: t.TempDir(), runtimeDir: t.TempDir()}); got != 2 {
		t.Fatalf("missing fallback opt-in exit code = %d, want 2", got)
	}
	output.Reset()
	if got := runWithIO([]string{"auth", "import", "--token-stdin", "--allow-plaintext-credentials"}, strings.NewReader(""), &output, commandRoots{configDir: t.TempDir(), dataDir: t.TempDir(), runtimeDir: t.TempDir()}); got != 2 {
		t.Fatalf("empty stdin exit code = %d, want 2", got)
	}
}

func TestAuthImportRejectsTokenArgument(t *testing.T) {
	var output bytes.Buffer
	if got := runWithIO([]string{"auth", "import", "--token-stdin", "token-as-argument"}, strings.NewReader(""), &output, commandRoots{configDir: t.TempDir(), dataDir: t.TempDir(), runtimeDir: t.TempDir()}); got != 2 {
		t.Fatalf("token argument exit code = %d, want 2", got)
	}
	if strings.Contains(output.String(), "token-as-argument") || !strings.Contains(output.String(), "only from stdin") {
		t.Fatalf("unexpected response = %s", output.String())
	}
}

func TestProfileConfigurePersistsDeploymentWithoutExposingCredentials(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "auth", "import", "--token-stdin", "--allow-plaintext-credentials"}, strings.NewReader("test-token\n"), &output, roots); got != 0 {
		t.Fatalf("auth import exit = %d: %s", got, output.String())
	}
	output.Reset()
	args := []string{"--profile", "work", "profile", "configure", "--user-id", "user-1", "--api-addr", "https://2.example.test/api", "--ws-addr", "wss://2.example.test/msg_gateway", "--platform-id", "7"}
	if got := runWithIO(args, strings.NewReader(""), &output, roots); got != 0 {
		t.Fatalf("profile configure exit = %d: %s", got, output.String())
	}
	if strings.Contains(output.String(), "user-1") || strings.Contains(output.String(), "test-token") {
		t.Fatalf("profile configure output leaked deployment or token: %s", output.String())
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	if err != nil {
		t.Fatal(err)
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if item.Deployment.UserID != "user-1" || item.Deployment.PlatformID != 7 || item.Deployment.APIAddr != "https://2.example.test/api" || item.Deployment.WSAddr != "wss://2.example.test/msg_gateway" {
		t.Fatalf("profile deployment = %#v", item.Deployment)
	}
}

func TestProfileConfigureRequiresImportedProfile(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	args := []string{"--profile", "newwork", "profile", "configure", "--user-id", "user-2", "--api-addr", "https://2.example.test/api", "--ws-addr", "wss://2.example.test/msg_gateway", "--platform-id", "7"}
	if got := runWithIO(args, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("profile configure exit = %d, want 2; output = %s", got, output.String())
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument || !strings.Contains(response.Error.Message, "import a token first") {
		t.Fatalf("profile configure response = %s, %v", output.String(), err)
	}
}

func TestDaemonVerifyRequiresConfiguredProfile(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"daemon", "verify"}, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("missing fallback exit = %d, want 2", got)
	}
	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "auth", "import", "--token-stdin", "--allow-plaintext-credentials"}, strings.NewReader("test-token\n"), &output, roots); got != 0 {
		t.Fatalf("auth import exit = %d: %s", got, output.String())
	}
	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "daemon", "verify", "--allow-plaintext-credentials"}, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("unconfigured profile exit = %d, want 2", got)
	}
	if !strings.Contains(output.String(), "deployment is not configured") {
		t.Fatalf("unconfigured profile response = %s", output.String())
	}
}

func TestDaemonServeRequiresExplicitDevelopmentAcknowledgements(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"daemon", "serve"}, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("missing daemon serve flags exit = %d, want 2", got)
	}
	if !strings.Contains(output.String(), "--allow-plaintext-credentials") {
		t.Fatalf("missing credential acknowledgement response = %s", output.String())
	}

	output.Reset()
	args := []string{"daemon", "serve", "--allow-plaintext-credentials", "--allow-all-inbound", "--codex-home", t.TempDir()}
	if got := runWithIO(args, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("missing same-user acknowledgement exit = %d, want 2", got)
	}
	if !strings.Contains(output.String(), "--allow-unsafe-same-user-provider") {
		t.Fatalf("missing provider acknowledgement response = %s", output.String())
	}
}

func TestDaemonServeRejectsCodexHomeInsideProfilePaths(t *testing.T) {
	roots := testRoots(t)
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateCodexHome(paths.DataDir, paths); err == nil || !strings.Contains(err.Error(), "must not overlap") {
		t.Fatalf("validateCodexHome() error = %v", err)
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
	if len(response.Result.Tools) != 22 || response.Result.Tools[0].Name == "abdim.daemon.shutdown" {
		t.Fatalf("MCP tools = %+v", response.Result.Tools)
	}
}

func testRoots(t *testing.T) commandRoots {
	t.Helper()
	root := t.TempDir()
	return commandRoots{configDir: filepath.Join(root, "config"), dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime")}
}
