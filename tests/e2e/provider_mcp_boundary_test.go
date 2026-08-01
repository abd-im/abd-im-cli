package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/launcher"
)

const providerMCPHelperEnv = "ABDIM_PROVIDER_MCP_HELPER"

const (
	providerSourceTokenMarker   = "provider-source-token-marker"
	providerSourceMessageMarker = "provider-source-message-marker"
)

func TestProviderRunPrivateMCPBoundaryE2E(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source-codex")
	if err := os.Mkdir(sourceHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte("[mcp_servers.owner]\ncommand = 'must-not-inherit'\n# "+providerSourceTokenMarker+"\n# "+providerSourceMessageMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(root, "helper.json")
	method := &providerMCPMethod{}
	tool, credential := providerMCPTool(t, method)
	adapter, err := codexprovider.New(codexprovider.Config{
		Executable:        providerMCPHelperCommand(t, root),
		WorkingDir:        root,
		Environment:       []string{providerMCPHelperEnv + "=1", "PATH=/usr/bin:/bin", "CODEX_HOME=" + sourceHome, "ABDIM_PROVIDER_CAPTURE=" + capture},
		BridgeCommand:     os.Args[0],
		Launcher:          e2eProviderRunner{},
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
		Proxy:           tool,
	}
	session, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runRoot := filepath.Join(root, request.RunID)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	config, err := os.ReadFile(filepath.Join(runRoot, "codex", "config.toml"))
	if err != nil || strings.Contains(string(config), "must-not-inherit") || strings.Contains(string(config), providerSourceTokenMarker) || strings.Contains(string(config), providerSourceMessageMarker) || !strings.Contains(string(config), "enabled_tools = [\"abdim.message.history\"]") {
		t.Fatalf("run-private config = %q, %v", config, err)
	}
	if info, err := os.Stat(filepath.Join(runRoot, "mcp.sock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run-private socket = %v, %v", info, err)
	}
	result, err := session.Turn(context.Background(), contracts.TurnRequest{RunID: request.RunID, EventID: "event-1", GrantCredential: credential, Prompt: "reply"})
	if err != nil || result.FinalText != "final reply" {
		t.Fatalf("Turn() = %#v, %v", result, err)
	}
	if helper := readProviderMCPHelper(t, capture); helper.Err != "" || helper.CodexHome != filepath.Join(runRoot, "codex") || helper.Home != filepath.Join(runRoot, "work") || helper.Socket != filepath.Join(runRoot, "mcp.sock") {
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

func providerMCPTool(t *testing.T, method *providerMCPMethod) (*proxy.Proxy, string) {
	t.Helper()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID:           "run-private",
		ProfileID:       "work",
		Principal:       "provider",
		Methods:         []string{"message.history"},
		Scopes:          []string{"message.read"},
		TargetAllowlist: []string{"conversation-1"},
		ExpiresAt:       time.Now().Add(time.Minute),
		RateBudget:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := proxy.New(grants, "run-private", "work", []proxy.Method{{
		Name:    "message.history",
		Scope:   "message.read",
		Allowed: func() bool { return true },
		Targets: func(raw json.RawMessage) ([]string, error) {
			var input struct {
				ConversationID string `json:"conversation_id"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			return []string{input.ConversationID}, nil
		},
		Handle: method.Handle,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return tool, credential
}

type providerMCPMethod struct {
	mu    sync.Mutex
	calls []contracts.Request
}

func (m *providerMCPMethod) Handle(_ context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	m.mu.Lock()
	m.calls = append(m.calls, request)
	m.mu.Unlock()
	return json.RawMessage(`{"items":[]}`), nil
}

func (m *providerMCPMethod) Calls() []contracts.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contracts.Request(nil), m.calls...)
}

type e2eProviderRunner struct{}

func (e2eProviderRunner) CopyCodexAuth(destination string) error {
	return os.WriteFile(destination, []byte(`{"tokens":{"access_token":"test"}}`), 0o600)
}
func (e2eProviderRunner) PrepareRun(string, string, string) error { return nil }
func (e2eProviderRunner) PrepareSocket(string) error              { return nil }
func (e2eProviderRunner) Configure(*exec.Cmd) error               { return nil }

var _ launcher.Runner = e2eProviderRunner{}

func providerMCPHelperCommand(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "fake-codex")
	contents := "#!/bin/sh\nexec " + quoteProviderMCPHelper(os.Args[0]) + " -test.run '^TestProviderMCPHelper$' --\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteProviderMCPHelper(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type providerMCPHelperResult struct {
	UID             int    `json:"uid"`
	Home            string `json:"home"`
	CodexHome       string `json:"codex_home"`
	Socket          string `json:"socket"`
	DeniedProfile   bool   `json:"denied_profile"`
	DeniedOwnerSock bool   `json:"denied_owner_socket"`
	DeniedNextRun   bool   `json:"denied_next_run"`
	Err             string `json:"err,omitempty"`
}

func readProviderMCPHelper(t *testing.T, path string) providerMCPHelperResult {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider helper capture: %v", err)
	}
	var result providerMCPHelperResult
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("decode provider helper capture: %v", err)
	}
	return result
}

func TestProviderMCPHelper(t *testing.T) {
	if os.Getenv(providerMCPHelperEnv) != "1" {
		return
	}
	result := providerMCPHelperResult{UID: os.Geteuid(), Home: os.Getenv("HOME"), CodexHome: os.Getenv("CODEX_HOME")}
	result.Socket = filepath.Join(filepath.Dir(result.CodexHome), "mcp.sock")
	if path := os.Getenv("ABDIM_PROVIDER_FORBIDDEN_PROFILE"); path != "" {
		_, err := os.ReadFile(path)
		result.DeniedProfile = err != nil
	}
	if path := os.Getenv("ABDIM_PROVIDER_FORBIDDEN_OWNER_SOCKET"); path != "" {
		connection, err := net.DialTimeout("unix", path, time.Second)
		if connection != nil {
			_ = connection.Close()
		}
		result.DeniedOwnerSock = err != nil
	}
	if path := os.Getenv("ABDIM_PROVIDER_FORBIDDEN_NEXT_RUN"); path != "" {
		_, err := os.ReadDir(path)
		result.DeniedNextRun = err != nil
	}

	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(reader.Bytes(), &request) != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			writeProviderMCPHelperResponse(request.ID, map[string]any{"ok": true})
		case "thread/start":
			writeProviderMCPHelperResponse(request.ID, map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "turn/start":
			result.Err = providerMCPHelperCall(result.Socket)
			contents, _ := json.Marshal(result)
			_ = os.WriteFile(os.Getenv("ABDIM_PROVIDER_CAPTURE"), contents, 0o600)
			writeProviderMCPHelperResponse(request.ID, map[string]any{})
			writeProviderMCPHelperNotification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]string{"type": "agentMessage", "text": "final reply", "phase": "final_answer"}})
			writeProviderMCPHelperNotification("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]string{"status": "completed"}})
		}
	}
}

func providerMCPHelperCall(socket string) string {
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err.Error()
	}
	defer connection.Close()
	encoder := json.NewEncoder(connection)
	decoder := bufio.NewReader(connection)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2026-07-28", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "e2e", "version": "v1"}}}); err != nil {
		return err.Error()
	}
	if _, err := decoder.ReadBytes('\n'); err != nil {
		return err.Error()
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "abdim.message.history", "arguments": map[string]any{"conversation_id": "conversation-1", "limit": 1}}}); err != nil {
		return err.Error()
	}
	if _, err := decoder.ReadBytes('\n'); err != nil {
		return err.Error()
	}
	return ""
}

func writeProviderMCPHelperResponse(id int, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func writeProviderMCPHelperNotification(method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
