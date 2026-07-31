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

func TestNewRequiresIsolatedCompositionInputs(t *testing.T) {
	if _, err := New(Config{Environment: []string{"PATH=/bin"}}); err == nil {
		t.Fatal("New() accepted an empty working directory")
	}
	if _, err := New(Config{WorkingDir: t.TempDir()}); err == nil {
		t.Fatal("New() accepted an inherited environment")
	}
}

func newAdapter(t *testing.T, capture string, block bool) *Adapter {
	t.Helper()
	root := t.TempDir()
	script := filepath.Join(root, "fake-codex")
	contents := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run '^TestCodexHelperProcess$' --\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake Codex executable: %v", err)
	}
	environment := []string{
		"GO_WANT_FAKE_CODEX=1",
		"PATH=/usr/bin:/bin",
		"FAKE_CODEX_CAPTURE=" + capture,
	}
	if block {
		environment = append(environment, "FAKE_CODEX_BLOCK=1")
	}
	adapter, err := New(Config{Executable: script, WorkingDir: root, Environment: environment, InitializeTimeout: time.Second})
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
				_ = os.WriteFile(filepath.Join(mustGetwd(), "started"), []byte("1"), 0o600)
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

func mustGetwd() string {
	path, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	return path
}
