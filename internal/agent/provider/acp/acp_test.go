package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestAdapterRunsV1PromptAndStreamsAgentText(t *testing.T) {
	adapter, capture := newTestAdapter(t, "normal")
	session, err := adapter.Start(context.Background(), startRequest(true))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	var mu sync.Mutex
	var updates []string
	var activities []contracts.TurnActivity
	result, err := session.Turn(context.Background(), contracts.TurnRequest{
		RunID: "run-1", EventID: "event-1", Prompt: "hello",
		Output: func(_ context.Context, output contracts.TurnOutput) error {
			mu.Lock()
			defer mu.Unlock()
			updates = append(updates, output.Text)
			return nil
		},
		Activity: func(_ context.Context, activity contracts.TurnActivity) error {
			mu.Lock()
			defer mu.Unlock()
			activities = append(activities, activity)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if result.FinalText != "hello world" || result.SessionRef != "session-1" {
		t.Fatalf("Turn() result = %#v", result)
	}
	mu.Lock()
	gotUpdates := append([]string(nil), updates...)
	mu.Unlock()
	wantUpdates := []string{"hel", "hello", "hello world"}
	if strings.Join(gotUpdates, "|") != strings.Join(wantUpdates, "|") {
		t.Fatalf("output updates = %#v, want %#v", gotUpdates, wantUpdates)
	}
	if len(activities) != 2 || activities[0].Kind != "tool.started" || activities[0].CallID != "call-1" ||
		activities[0].Name != "terminal" || activities[1].Kind != "tool.completed" || activities[1].Status != "completed" {
		t.Fatalf("activity updates = %#v", activities)
	}
	payload, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read session capture: %v", err)
	}
	var params struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(payload, &params) != nil || !filepath.IsAbs(params.CWD) {
		t.Fatalf("session/new params = %s", payload)
	}
}

func TestAdapterLoadsStoredSessionAndReportsMissingSession(t *testing.T) {
	adapter, _ := newTestAdapter(t, "normal")
	request := startRequest(false)
	request.SessionRef = "session-1"
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start(load) error = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	adapter, _ = newTestAdapter(t, "missing-session")
	request.SessionRef = "session-missing"
	if _, err := adapter.Start(context.Background(), request); !errors.Is(err, contracts.ErrSessionNotFound) {
		t.Fatalf("Start(missing) error = %v, want ErrSessionNotFound", err)
	}
}

func TestAdapterRejectsNonV1NegotiatedVersion(t *testing.T) {
	adapter, _ := newTestAdapter(t, "v2")
	_, err := adapter.Start(context.Background(), startRequest(false))
	if !errors.Is(err, ErrProtocolUnsupported) {
		t.Fatalf("Start() error = %v, want ErrProtocolUnsupported", err)
	}
}

func TestAdapterInitializationFailsWithoutValidStdoutResponse(t *testing.T) {
	adapter, _ := newTestAdapter(t, "invalid-stdout")
	_, err := adapter.Start(context.Background(), startRequest(false))
	if err == nil {
		t.Fatal("Start() accepted invalid Agent stdout")
	}
}

func TestAdapterFailsWhenAgentExitsDuringPrompt(t *testing.T) {
	adapter, _ := newTestAdapter(t, "exit-on-prompt")
	session, err := adapter.Start(context.Background(), startRequest(false))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	_, err = session.Turn(context.Background(), contracts.TurnRequest{
		RunID: "run-1", EventID: "event-1", Prompt: "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt failed") {
		t.Fatalf("Turn() error = %v, want prompt failure", err)
	}
}

func TestAdapterCancellationWaitsForIdleCancelled(t *testing.T) {
	adapter, _ := newTestAdapter(t, "cancel")
	session, err := adapter.Start(context.Background(), startRequest(false))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := session.Turn(context.Background(), contracts.TurnRequest{RunID: "run-1", EventID: "event-1", Prompt: "wait"})
		done <- err
	}()
	if err := waitForFile(filepath.Join(adapter.config.WorkingDir, "cancel-ready"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := session.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("Turn() error = %v, want cancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Turn() did not finish after cancelled idle state")
	}
}

func TestAdapterPropagatesOutputSinkFailure(t *testing.T) {
	adapter, _ := newTestAdapter(t, "normal")
	session, err := adapter.Start(context.Background(), startRequest(false))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Close(context.Background())
	want := errors.New("stream unavailable")
	_, err = session.Turn(context.Background(), contracts.TurnRequest{
		RunID: "run-1", EventID: "event-1", Prompt: "hello",
		Output: func(context.Context, contracts.TurnOutput) error { return want },
	})
	if !errors.Is(err, want) {
		t.Fatalf("Turn() error = %v, want sink failure", err)
	}
}

func newTestAdapter(t *testing.T, scenario string) (*Adapter, string) {
	t.Helper()
	root := t.TempDir()
	script := filepath.Join(root, "fake-acp")
	contents := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run '^TestACPHelperProcess$' --\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake ACP executable: %v", err)
	}
	capture := filepath.Join(root, "session-new.json")
	adapter, err := New(Config{
		Executable: script,
		WorkingDir: filepath.Join(root, "runs"),
		CLICommand: os.Args[0],
		Environment: []string{
			"GO_WANT_FAKE_ACP=1",
			"FAKE_ACP_SCENARIO=" + scenario,
			"FAKE_ACP_CAPTURE=" + capture,
			"FAKE_ACP_CANCEL_READY=" + filepath.Join(root, "runs", "cancel-ready"),
			"PATH=/usr/bin:/bin",
		},
		InitializeTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter, capture
}

func startRequest(withTools bool) contracts.StartRequest {
	return contracts.StartRequest{ProfileID: "work", RunID: "run-1"}
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("timed out waiting for fake ACP Agent")
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FAKE_ACP") != "1" {
		return
	}
	scenario := os.Getenv("FAKE_ACP_SCENARIO")
	sessionID := "session-1"
	promptID := 0
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		line := reader.Bytes()
		var message struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if json.Unmarshal(line, &message) != nil {
			continue
		}
		if message.Method == "" && message.ID == 99 {
			if message.Result.Outcome.Outcome != "selected" || message.Result.Outcome.OptionID != "allow" {
				os.Exit(3)
			}
			writeHelperNotification("session/update", map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"}})
			writeHelperNotification("session/update", map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "lo"}}})
			writeHelperNotification("session/update", map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": " world"}}})
			writeHelperResponse(promptID, map[string]string{"stopReason": "end_turn"})
			continue
		}
		switch message.Method {
		case "initialize":
			if scenario == "invalid-stdout" {
				_, _ = os.Stdout.WriteString("not-json\n")
				continue
			}
			version := 1
			if scenario == "v2" {
				version = 2
			}
			writeHelperResponse(message.ID, map[string]any{
				"protocolVersion": version,
				"agentCapabilities": map[string]any{
					"loadSession":         true,
					"sessionCapabilities": map[string]any{"close": map[string]any{}},
				},
				"agentInfo":   map[string]string{"name": "fake-acp-v1", "version": "1.0.0"},
				"authMethods": []any{},
			})
		case "session/new":
			_ = os.WriteFile(os.Getenv("FAKE_ACP_CAPTURE"), message.Params, 0o600)
			writeHelperResponse(message.ID, map[string]string{"sessionId": sessionID})
		case "session/load":
			if scenario == "missing-session" {
				writeHelperError(message.ID, -32002, "Resource not found")
				continue
			}
			writeHelperResponse(message.ID, map[string]any{})
		case "session/prompt":
			promptID = message.ID
			if scenario == "exit-on-prompt" {
				os.Exit(0)
			}
			if scenario == "cancel" {
				_ = os.WriteFile(os.Getenv("FAKE_ACP_CANCEL_READY"), []byte("1"), 0o600)
				continue
			}
			writeHelperNotification("session/update", map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "hel"}}})
			writeHelperNotification("session/update", map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Check status", "kind": "execute", "status": "in_progress"}})
			writeHelperRequest(99, "session/request_permission", map[string]any{
				"sessionId": sessionID,
				"toolCall":  map[string]any{"toolCallId": "call-1"},
				"options": []map[string]string{
					{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
					{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
				},
			})
		case "session/cancel":
			writeHelperResponse(promptID, map[string]string{"stopReason": "cancelled"})
		case "session/close":
			writeHelperResponse(message.ID, map[string]any{})
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

func writeHelperRequest(id int, method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
