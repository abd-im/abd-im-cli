package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestAdapterRunsFixedAppServerTurn(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "prompt.txt")
	adapter := newAdapter(t, capture, false)
	session, err := adapter.Start(context.Background(), startRequest())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	var outputs []string
	var activities []contracts.TurnActivity
	result, err := session.Turn(context.Background(), contracts.TurnRequest{
		RunID: "run-1", EventID: "event-1", GrantCredential: "grant-1", Prompt: "inbound body marker",
		Output: func(_ context.Context, output contracts.TurnOutput) error {
			outputs = append(outputs, output.Text)
			return nil
		},
		Activity: func(_ context.Context, activity contracts.TurnActivity) error {
			activities = append(activities, activity)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if result.FinalText != "final reply" || result.SessionRef != "thread-1" {
		t.Fatalf("Turn() result = %#v", result)
	}
	if len(outputs) != 2 || outputs[0] != "final" || outputs[1] != "final reply" {
		t.Fatalf("Turn() outputs = %#v", outputs)
	}
	if len(activities) != 4 || activities[0].Kind != "activity.summary" || activities[0].Summary != "intermediate reply" ||
		activities[1].Kind != "activity.summary" || activities[1].Summary != "checked the available weather sources" ||
		activities[2].Kind != "tool.started" || activities[2].CallID != "command-1" || activities[2].Name != "shell" ||
		activities[2].Summary != "git status" || activities[3].Kind != "tool.completed" || activities[3].Status != "completed" ||
		activities[3].Summary != "git status" {
		t.Fatalf("Turn() activities = %#v", activities)
	}
	prompt, err := os.ReadFile(capture)
	if err != nil || string(prompt) != "inbound body marker" {
		t.Fatalf("Codex prompt = %q, %v", prompt, err)
	}
}

func TestAdapterResumesStoredThreadAndReportsMissingThread(t *testing.T) {
	adapter := newAdapter(t, "", false)
	request := startRequest()
	request.SessionRef = "thread-1"
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start(resume) error = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	request.RunID = "run-2"
	request.SessionRef = "thread-missing"
	if _, err := adapter.Start(context.Background(), request); !errors.Is(err, contracts.ErrSessionNotFound) {
		t.Fatalf("Start(missing) error = %v, want ErrSessionNotFound", err)
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

func TestAdapterEndsTurnOnTerminalAppServerError(t *testing.T) {
	adapter := newAdapter(t, "", false)
	adapter.config.Environment = append(adapter.config.Environment, "FAKE_CODEX_TERMINAL_ERROR=1")
	session, err := adapter.Start(context.Background(), startRequest())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = session.Turn(ctx, contracts.TurnRequest{RunID: "run-1", EventID: "event-1", GrantCredential: "grant-1", Prompt: "fail"})
	if err == nil || err.Error() != "Codex turn did not complete: stream disconnected" {
		t.Fatalf("Turn() error = %v, want terminal app-server error", err)
	}
}

func TestConfiguredThreadSettingsApplyToStartAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("model = \"gpt-test\"\nmodel_provider = \"selected-provider\"\n\n[model_providers.selected-provider]\nbase_url = \"https://api.example.test/v1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := configuredThreadSettings(path)
	if err != nil {
		t.Fatalf("configuredThreadSettings() error = %v", err)
	}
	params := threadParams("/workspace", settings)
	if model, _ := params["model"].(*string); model == nil || *model != "gpt-test" {
		t.Fatalf("thread model = %#v", params["model"])
	}
	if provider, _ := params["modelProvider"].(*string); provider == nil || *provider != "selected-provider" {
		t.Fatalf("thread model provider = %#v", params["modelProvider"])
	}
}

func TestSessionKeepsTurnOpenForRetryingAppServerError(t *testing.T) {
	turn := &turnState{done: make(chan struct{})}
	session := &session{threadID: "thread-1", turn: turn}
	session.handleNotification("error", json.RawMessage(`{"threadId":"thread-1","willRetry":true,"error":{"message":"stream disconnected"}}`))
	select {
	case <-turn.done:
		t.Fatal("retrying app-server error ended the turn")
	default:
	}
}

func TestAdapterCreatesRunPrivateCLIConfiguration(t *testing.T) {
	adapter := newAdapter(t, "", false)
	if err := os.WriteFile(filepath.Join(adapter.sourceCodexHome, "config.toml"), []byte("model = 'gpt-test'\nmodel_provider = 'OpenIM'\n\n[model_providers.OpenIM]\nbase_url = 'https://api.example.test/v1'\nsupports_websockets = true\n\n[projects.'/workspace']\nhook = 'must-not-inherit'\n"), 0o600); err != nil {
		t.Fatalf("write source Codex config: %v", err)
	}
	request := startRequest()
	request.AllowedMethods = []string{"message.history", "daemon.shutdown"}
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runRoot := filepath.Join(adapter.config.WorkingDir, request.RunID)
	configPath := filepath.Join(adapter.stateDir, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read run config: %v", err)
	}
	if strings.Contains(string(config), "must-not-inherit") || !strings.Contains(string(config), "model_provider = 'OpenIM'") || !strings.Contains(string(config), "base_url = 'https://api.example.test/v1'") || strings.Contains(string(config), "supports_websockets = true") || !strings.Contains(string(config), "supports_websockets = false") || !strings.Contains(string(config), "[shell_environment_policy]") || !strings.Contains(string(config), "ABDIM_AGENT_GRANT") {
		t.Fatalf("run CLI config = %s", config)
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run config mode = %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Join(runRoot, "work", ".abdim.sock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run CLI socket mode = %v, %v", info.Mode(), err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(runRoot); !os.IsNotExist(err) {
		t.Fatalf("run directory still exists after close: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("persistent Codex state was removed after close: %v", err)
	}
}

func TestNewRequiresCurrentUserCompositionInputs(t *testing.T) {
	if _, err := New(Config{Environment: []string{"PATH=/bin"}, CLICommand: os.Args[0]}); err == nil {
		t.Fatal("New() accepted an empty working directory")
	}
	if _, err := New(Config{Executable: "/bin/true", WorkingDir: t.TempDir(), SourceCodexHome: t.TempDir(), CLICommand: os.Args[0]}); err == nil {
		t.Fatal("New() accepted an inherited environment")
	}
	if _, err := New(Config{Executable: "/bin/true", WorkingDir: t.TempDir(), Environment: []string{"PATH=/bin"}, CLICommand: os.Args[0]}); err == nil || !strings.Contains(err.Error(), "source CODEX_HOME") {
		t.Fatalf("New() accepted no source CODEX_HOME: %v", err)
	}
}

func TestAdapterPreservesAppServerStartError(t *testing.T) {
	adapter := newAdapter(t, "", false)
	script := filepath.Join(t.TempDir(), "broken-codex")
	if err := os.WriteFile(script, []byte("#!/missing/interpreter\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	adapter.config.Executable = script
	if _, err := adapter.Start(context.Background(), startRequest()); err == nil || !strings.Contains(err.Error(), "start Codex app-server:") {
		t.Fatalf("Start() error = %v, want app-server start cause", err)
	}
}

func TestPermissionApprovalOnlyEchoesSupportedPermissions(t *testing.T) {
	result := permissionApproval(json.RawMessage(`{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/tmp"]},"other":true}}`))
	permissions, ok := result["permissions"].(map[string]any)
	if !ok || len(permissions) != 2 || permissions["network"] == nil || permissions["fileSystem"] == nil || result["scope"] != "turn" {
		t.Fatalf("permissionApproval() = %#v", result)
	}
}

func TestPermissionSummaryOnlyNamesGrantedPermissionKinds(t *testing.T) {
	if got := permissionSummary(json.RawMessage(`{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/tmp"]},"other":true}}`)); got != "Network access, File access" {
		t.Fatalf("permissionSummary() = %q", got)
	}
}

func newAdapter(t *testing.T, capture string, block bool) *Adapter {
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
		"FAKE_CODEX_CAPTURE=" + capture,
		"FAKE_CODEX_STARTED=" + filepath.Join(root, "started"),
	}
	if block {
		environment = append(environment, "FAKE_CODEX_BLOCK=1")
	}
	adapter, err := New(Config{Executable: script, WorkingDir: root, SourceCodexHome: home, Environment: environment, CLICommand: os.Args[0], InitializeTimeout: time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
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
		case "thread/resume":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.ThreadID == "thread-missing" {
				writeHelperError(request.ID, -32600, "no rollout found for thread id "+params.ThreadID)
				continue
			}
			writeHelperResponse(request.ID, map[string]any{"thread": map[string]string{"id": params.ThreadID}})
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
			if os.Getenv("FAKE_CODEX_TERMINAL_ERROR") == "1" {
				writeHelperNotification("error", map[string]any{"threadId": "thread-1", "willRetry": false, "error": map[string]string{"message": "stream disconnected"}})
				continue
			}
			if os.Getenv("FAKE_CODEX_BLOCK") == "1" {
				_ = os.WriteFile(os.Getenv("FAKE_CODEX_STARTED"), []byte("1"), 0o600)
				continue
			}
			writeHelperNotification("item/started", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "commentary-1", "type": "agentMessage", "text": "", "phase": "commentary"}})
			writeHelperNotification("item/agentMessage/delta", map[string]any{"threadId": "thread-1", "itemId": "commentary-1", "delta": "internal commentary"})
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "commentary-1", "type": "agentMessage", "text": "intermediate reply", "phase": "commentary"}})
			writeHelperNotification("item/reasoning/summaryTextDelta", map[string]any{"threadId": "thread-1", "itemId": "reasoning-1", "summaryIndex": 0, "delta": "checked the available "})
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "reasoning-1", "type": "reasoning", "summary": []string{"checked the available", "weather sources"}, "content": []string{"private reasoning"}}})
			writeHelperNotification("item/started", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "command-1", "type": "commandExecution", "command": "git status"}})
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "command-1", "type": "commandExecution", "status": "completed", "command": "git status"}})
			writeHelperNotification("item/started", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "final-1", "type": "agentMessage", "text": "", "phase": "final_answer"}})
			writeHelperNotification("item/agentMessage/delta", map[string]any{"threadId": "thread-1", "itemId": "final-1", "delta": "final"})
			writeHelperNotification("item/agentMessage/delta", map[string]any{"threadId": "thread-1", "itemId": "final-1", "delta": " reply"})
			writeHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]any{"id": "final-1", "type": "agentMessage", "text": "final reply", "phase": "final_answer"}})
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

func writeHelperError(id, code int, message string) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func writeHelperNotification(method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
