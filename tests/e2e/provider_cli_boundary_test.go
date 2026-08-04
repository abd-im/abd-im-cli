package e2e

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

	"github.com/abd-im/abd-im-cli/internal/agent/access"
	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	acpprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/acp"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
)

const providerCLIHelperEnv = "ABDIM_PROVIDER_CLI_HELPER"

func TestProviderRunPrivateCLIBoundaryE2E(t *testing.T) {
	root := t.TempDir()
	capture := filepath.Join(root, "helper.json")
	method := &providerCLIMethod{}
	runProxy, credential := providerCLIProxy(t, method)
	cliCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := acpprovider.New(acpprovider.Config{
		Executable:        providerCLIHelperCommand(t, root),
		WorkingDir:        root,
		Environment:       []string{providerCLIHelperEnv + "=1", "PATH=/usr/bin:/bin", "ABDIM_PROVIDER_CAPTURE=" + capture},
		CLICommand:        cliCommand,
		InitializeTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.StartRequest{
		ProfileID:       "work",
		RunID:           "run-private",
		GrantCredential: credential,
		AllowedMethods:  []string{"message.history"},
		Proxy:           runProxy,
	}
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runRoot := filepath.Join(root, request.RunID)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	if info, err := os.Stat(filepath.Join(runRoot, "work", ".abdim.sock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run-private socket = %v, %v", info, err)
	}
	result, err := session.Turn(context.Background(), contracts.TurnRequest{RunID: request.RunID, EventID: "event-1", GrantCredential: credential, Prompt: "reply"})
	if err != nil || result.FinalText != "final reply" {
		t.Fatalf("Turn() = %#v, %v", result, err)
	}
	helper := readProviderCLIHelper(t, capture)
	if helper.Err != "" || helper.WorkDir != filepath.Join(runRoot, "work") || helper.Socket != filepath.Join(runRoot, "work", ".abdim.sock") || helper.ProfileID != "work" || helper.RunID != "run-private" {
		t.Fatalf("provider helper = %+v", helper)
	}
	if calls := method.Calls(); len(calls) != 1 || calls[0].Method != "message.history" || calls[0].Grant != credential {
		t.Fatalf("run-private proxy calls = %+v", calls)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(runRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run-private directory remains: %v", err)
	}
}

func providerCLIProxy(t *testing.T, method *providerCLIMethod) (*proxy.Proxy, string) {
	t.Helper()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID: "run-private", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.history"},

		ExpiresAt: time.Now().Add(time.Minute), RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runProxy, err := proxy.New(grants, "run-private", "work", []proxy.Method{{
		Name:   "message.history",
		Handle: method.Handle,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return runProxy, credential
}

type providerCLIMethod struct {
	mu    sync.Mutex
	calls []contracts.Request
}

func (m *providerCLIMethod) Handle(_ context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	m.mu.Lock()
	m.calls = append(m.calls, request)
	m.mu.Unlock()
	return json.RawMessage(`{"items":[]}`), nil
}

func (m *providerCLIMethod) Calls() []contracts.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contracts.Request(nil), m.calls...)
}

func providerCLIHelperCommand(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "fake-agent")
	contents := "#!/bin/sh\nexec " + quoteProviderCLIHelper(os.Args[0]) + " -test.run '^TestProviderCLIHelper$' --\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteProviderCLIHelper(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type providerCLIHelperResult struct {
	WorkDir   string `json:"work_dir"`
	Socket    string `json:"socket"`
	ProfileID string `json:"profile_id"`
	RunID     string `json:"run_id"`
	Err       string `json:"err,omitempty"`
}

func readProviderCLIHelper(t *testing.T, path string) providerCLIHelperResult {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider helper capture: %v", err)
	}
	var result providerCLIHelperResult
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("decode provider helper capture: %v", err)
	}
	return result
}

func TestProviderCLIHelper(t *testing.T) {
	if os.Getenv(providerCLIHelperEnv) != "1" {
		return
	}
	workDir, _ := os.Getwd()
	values, _, contextErr := access.FromEnvironment(os.Getenv)
	result := providerCLIHelperResult{WorkDir: workDir, Socket: values.Socket, ProfileID: values.ProfileID, RunID: values.RunID}
	if contextErr != nil {
		result.Err = contextErr.Error()
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
			writeProviderCLIHelperResponse(request.ID, map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"close": map[string]any{}}},
				"agentInfo":         map[string]string{"name": "fake-acp-v1", "version": "1.0.0"}, "authMethods": []any{},
			})
		case "session/new":
			writeProviderCLIHelperResponse(request.ID, map[string]string{"sessionId": "session-1"})
		case "session/prompt":
			if result.Err == "" {
				result.Err = providerCLIHelperCall(values)
			}
			contents, _ := json.Marshal(result)
			_ = os.WriteFile(os.Getenv("ABDIM_PROVIDER_CAPTURE"), contents, 0o600)
			writeProviderCLIHelperNotification("session/update", map[string]any{
				"sessionId": "session-1",
				"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "final reply"}},
			})
			writeProviderCLIHelperResponse(request.ID, map[string]string{"stopReason": "end_turn"})
		case "session/close":
			writeProviderCLIHelperResponse(request.ID, map[string]any{})
		}
	}
}

func providerCLIHelperCall(values access.Context) string {
	response, err := ipc.Call(context.Background(), values.Socket, contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: "agent-cli", ProfileID: values.ProfileID,
		Method: "message.history", Params: json.RawMessage(`{"conversation_id":"conversation-1","limit":1}`), Grant: values.Grant,
	})
	if err != nil {
		return err.Error()
	}
	if !response.OK {
		return response.Error.Message
	}
	return ""
}

func writeProviderCLIHelperResponse(id int, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func writeProviderCLIHelperNotification(method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
