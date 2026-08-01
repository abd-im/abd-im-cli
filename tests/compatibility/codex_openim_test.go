package compatibility

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/mcp/stdio"
	"github.com/abd-im/abd-im-cli/internal/testkit"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
)

func TestSingleCodexOpenIMCompatibilityMatrix(t *testing.T) {
	combination := supportedCombination(t)
	gate, err := capability.NewEvidenceGate([]capability.Compatibility{combination})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := gate.Manifest(combination, []capability.Entry{
		{Method: "message.history", Scope: "message.read", Status: capability.Available},
		{Method: "conversation.unread", Scope: "conversation.read", Status: capability.NotValidated, Reason: "server source is unavailable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Allows("message.history", "message.read") {
		t.Fatal("supported matrix did not retain verified method")
	}
	if manifest.Allows("conversation.unread", "conversation.read") {
		t.Fatal("matrix upgraded an unvalidated method")
	}

	adapter := compatibilityAdapter(t)
	session, err := adapter.Start(context.Background(), contracts.StartRequest{
		ProfileID:       "compatibility",
		RunID:           "compatibility-run",
		GrantCredential: "compatibility-grant",
		AllowedMethods:  []string{"message.history"},
		Proxy:           &testkit.FakeProxy{},
	})
	if err != nil {
		t.Fatalf("start supported Codex adapter: %v", err)
	}
	defer session.Close(context.Background())

	result, err := session.Turn(context.Background(), contracts.TurnRequest{
		RunID:           "compatibility-run",
		EventID:         "compatibility-event",
		GrantCredential: "compatibility-grant",
		Prompt:          "compatibility probe",
	})
	if err != nil || result.FinalText != "compatibility reply" || result.SessionRef != "compatibility-thread" {
		t.Fatalf("supported Codex turn = %#v, %v", result, err)
	}
}

func TestStaticManifestCannotBypassCompatibilityEvidence(t *testing.T) {
	combination := supportedCombination(t)
	gate, err := capability.NewEvidenceGate([]capability.Compatibility{combination})
	if err != nil {
		t.Fatal(err)
	}
	combination.ServerAPI = "openim-api/v4"
	manifest, err := gate.Manifest(combination, []capability.Entry{{Method: "message.history", Scope: "message.read", Status: capability.Available}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Allows("message.history", "message.read") {
		t.Fatal("unsupported server API exposed a static available method")
	}
	entry, ok := manifest.Entry("message.history")
	if !ok || entry.Status != capability.NotValidated {
		t.Fatalf("unverified manifest entry = %#v, exists=%v", entry, ok)
	}
}

func supportedCombination(t *testing.T) capability.Compatibility {
	t.Helper()
	combination := capability.SingleCodexOpenIMCompatibility
	if got := open_im_sdk.GetSdkVersion(); got != combination.SDKVersion {
		t.Fatalf("pinned OpenIM SDK version = %q, want %q; record new compatibility evidence before changing it", got, combination.SDKVersion)
	}
	if stdio.ProtocolVersion != combination.MCPProtocol {
		t.Fatalf("provider MCP protocol = %q, want %q; record new compatibility evidence before changing it", stdio.ProtocolVersion, combination.MCPProtocol)
	}
	return combination
}

func compatibilityAdapter(t *testing.T) *codex.Adapter {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
		t.Fatalf("write Codex credentials: %v", err)
	}
	script := filepath.Join(root, "fake-codex")
	contents := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run '^TestCompatibilityCodexProcess$' --\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake Codex executable: %v", err)
	}
	bridgeCommand, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := codex.New(codex.Config{
		Executable:        script,
		WorkingDir:        root,
		SourceCodexHome:   home,
		Environment:       []string{"GO_WANT_COMPATIBILITY_CODEX=1", "PATH=/usr/bin:/bin"},
		BridgeCommand:     bridgeCommand,
		InitializeTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new Codex adapter: %v", err)
	}
	return adapter
}

func TestCompatibilityCodexProcess(t *testing.T) {
	if os.Getenv("GO_WANT_COMPATIBILITY_CODEX") != "1" {
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
			writeCompatibilityResponse(request.ID, map[string]any{"ok": true})
		case "thread/start":
			writeCompatibilityResponse(request.ID, map[string]any{"thread": map[string]string{"id": "compatibility-thread"}})
		case "turn/start":
			writeCompatibilityResponse(request.ID, map[string]any{"turn": map[string]string{"id": "compatibility-turn"}})
			writeCompatibilityNotification("item/completed", map[string]any{"threadId": "compatibility-thread", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "compatibility reply"}})
			writeCompatibilityNotification("turn/completed", map[string]any{"threadId": "compatibility-thread", "turn": map[string]string{"status": "completed"}})
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatalf("read fake Codex input: %v", err)
	}
	os.Exit(0)
}

func writeCompatibilityResponse(id int, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func writeCompatibilityNotification(method string, params any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, _ = os.Stdout.Write(append(payload, '\n'))
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
