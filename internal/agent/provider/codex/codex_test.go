package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/launcher"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestAdapterRunsFixedAppServerTurnAndDeclinesApprovals(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "prompt.txt")
	adapter := newAdapter(t, capture, false)
	session, err := adapter.Start(context.Background(), startRequest())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	result, err := session.Turn(context.Background(), contracts.TurnRequest{RunID: "run-1", EventID: "event-1", GrantCredential: "grant-1", Prompt: "inbound body marker"})
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if result.FinalText != "final reply" || result.SessionRef != "thread-1" {
		t.Fatalf("Turn() result = %#v", result)
	}
	prompt, err := os.ReadFile(capture)
	if err != nil || string(prompt) != "inbound body marker" {
		t.Fatalf("Codex prompt = %q, %v", prompt, err)
	}
}

func TestAdapterCancellationReapsBlockedServer(t *testing.T) {
	adapter := newAdapter(t, "", true)
	value, err := adapter.Start(context.Background(), startRequest())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer value.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := value.Turn(context.Background(), contracts.TurnRequest{RunID: "run-1", EventID: "event-1", GrantCredential: "grant-1", Prompt: "wait"})
		done <- err
	}()
	if err := waitForFile(filepath.Join(adapter.config.WorkingDir, "started"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := value.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Turn() succeeded after Cancel()")
		}
	case <-time.After(time.Second):
		t.Fatal("Turn() did not return after Cancel()")
	}
}

func TestAdapterCreatesRunPrivateMCPConfiguration(t *testing.T) {
	adapter := newAdapter(t, "", false)
	if err := os.WriteFile(filepath.Join(adapter.codexHome, "config.toml"), []byte("[mcp_servers.owner]\ncommand = 'must-not-inherit'\n"), 0o600); err != nil {
		t.Fatalf("write source Codex config: %v", err)
	}
	request := startRequest()
	request.AllowedMethods = []string{"message.history", "daemon.shutdown"}
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runRoot := filepath.Join(adapter.config.WorkingDir, request.RunID)
	configPath := filepath.Join(runRoot, "codex", "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read run config: %v", err)
	}
	if strings.Contains(string(config), "must-not-inherit") || !strings.Contains(string(config), "[mcp_servers.abdim]") || !strings.Contains(string(config), "enabled_tools = [\"abdim.message.history\"]") {
		t.Fatalf("run MCP config = %s", config)
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run config mode = %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Join(runRoot, "mcp.sock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run MCP socket mode = %v, %v", info.Mode(), err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(runRoot); !os.IsNotExist(err) {
		t.Fatalf("run directory still exists after close: %v", err)
	}
}

func TestAdapterDelegatesRestrictedRunAssetsToLauncher(t *testing.T) {
	runner := &recordingRunner{}
	adapter := newAdapterWithRunner(t, "", false, runner)
	request := startRequest()
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	runRoot := filepath.Join(adapter.config.WorkingDir, request.RunID)
	if runner.root != runRoot || runner.home != filepath.Join(runRoot, "codex") || runner.workDir != filepath.Join(runRoot, "work") || runner.socket != filepath.Join(runRoot, "mcp.sock") || runner.commandDir != runner.workDir {
		t.Fatalf("launcher calls = %#v", runner)
	}
}

func TestNewRequiresIsolatedCompositionInputs(t *testing.T) {
	if _, err := New(Config{Environment: []string{"PATH=/bin"}, BridgeCommand: os.Args[0]}); err == nil {
		t.Fatal("New() accepted an empty working directory")
	}
	if _, err := New(Config{WorkingDir: t.TempDir(), BridgeCommand: os.Args[0]}); err == nil {
		t.Fatal("New() accepted an inherited environment")
	}
	home := t.TempDir()
	if _, err := New(Config{Executable: "/bin/true", WorkingDir: t.TempDir(), Environment: []string{"PATH=/bin", "CODEX_HOME=" + home}, BridgeCommand: os.Args[0]}); err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("New() accepted no isolated launcher: %v", err)
	}
}

func newAdapter(t *testing.T, capture string, block bool) *Adapter {
	return newAdapterWithRunner(t, capture, block, testRunner{})
}

func newAdapterWithRunner(t *testing.T, capture string, block bool, runner launcher.Runner) *Adapter {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
		t.Fatalf("write Codex credentials: %v", err)
	}
	script := filepath.Join(root, "fake-codex")
	contents := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run '^TestCodexHelperProcess$' --\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake Codex executable: %v", err)
	}
	environment := []string{
		"GO_WANT_FAKE_CODEX=1",
		"PATH=/usr/bin:/bin",
		"CODEX_HOME=" + home,
		"FAKE_CODEX_CAPTURE=" + capture,
		"FAKE_CODEX_STARTED=" + filepath.Join(root, "started"),
	}
	if block {
		environment = append(environment, "FAKE_CODEX_BLOCK=1")
	}
	adapter, err := New(Config{Executable: script, WorkingDir: root, Environment: environment, BridgeCommand: os.Args[0], Launcher: runner, InitializeTimeout: time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
}

type testRunner struct{}

func (testRunner) CopyCodexAuth(destination string) error {
	return os.WriteFile(destination, []byte(`{"tokens":{"access_token":"test"}}`), 0o600)
}

func (testRunner) PrepareRun(string, string, string) error { return nil }

func (testRunner) PrepareSocket(string) error { return nil }

func (testRunner) Configure(*exec.Cmd) error { return nil }

type recordingRunner struct {
	root       string
	home       string
	workDir    string
	socket     string
	commandDir string
}

func (r *recordingRunner) CopyCodexAuth(destination string) error {
	return os.WriteFile(destination, []byte(`{"tokens":{"access_token":"test"}}`), 0o600)
}

func (r *recordingRunner) PrepareRun(root, home, workDir string) error {
	r.root, r.home, r.workDir = root, home, workDir
	return nil
}

func (r *recordingRunner) PrepareSocket(socket string) error {
	r.socket = socket
	return nil
}

func (r *recordingRunner) Configure(command *exec.Cmd) error {
	r.commandDir = command.Dir
	return nil
}

func startRequest() contracts.StartRequest {
	return contracts.StartRequest{ProfileID: "work", RunID: "run-1", GrantCredential: "grant-1", Proxy: &testkit.FakeProxy{}}
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("fake Codex server did not start a turn")
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FAKE_CODEX") != "1" {
		return
	}
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(reader.Bytes(), &request) != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			writeHelperResponse(request.ID, map[string]any{"ok": true})
		case "thread/start":
			writeHelperResponse(request.ID, map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "turn/start":
			var params struct {
				Input []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if len(params.Input) != 0 {
				_ = os.WriteFile(os.Getenv("FAKE_CODEX_CAPTURE"), []byte(params.Input[0].Text), 0o600)
			}
			writeHelperResponse(request.ID, map[string]any{})
			if os.Getenv("FAKE_CODEX_BLOCK") == "1" {
				_ = os.WriteFile(os.Getenv("FAKE_CODEX_STARTED"), []byte("1"), 0o600)
				continue
			}
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"type": "agentMessage", "text": "intermediate reply", "phase": "commentary"}})
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"type": "agentMessage", "text": "final reply", "phase": "final_answer"}})
			writeHelperNotification("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]string{"status": "completed"}})
		case "item/commandExecution/requestApproval":
			// This request is emitted by the parent adapter, not the fake server.
		}
	}
	os.Exit(0)
}

func writeHelperResponse(id int, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func writeHelperNotification(method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
