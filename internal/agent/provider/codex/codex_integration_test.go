//go:build integration

package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestRealCodexAppServer(t *testing.T) {
	if os.Getenv("ABDIM_REAL_CODEX") != "1" {
		t.Skip("set ABDIM_REAL_CODEX=1 to run the real Codex integration")
	}
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("codex is unavailable on PATH")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if !filepath.IsAbs(codexHome) {
		t.Fatal("CODEX_HOME must be absolute")
	}
	adapter, err := New(Config{
		Executable:      executable,
		WorkingDir:      t.TempDir(),
		SourceCodexHome: codexHome,
		Environment:     []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=dumb", "NO_BROWSER=1"},
		CLICommand:      buildRealCLI(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	grants := grant.NewStore()
	_, credential, err := grants.Issue(grant.Policy{
		RunID: "real-codex", ProfileID: "real-codex", Principal: "integration",
		Methods: []string{"message.history"}, ExpiresAt: time.Now().Add(2 * time.Minute), RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	runProxy, err := proxy.New(grants, "real-codex", "real-codex", []proxy.Method{{
		Name: "message.history",
		Handle: func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error) {
			called.Store(true)
			return json.RawMessage(`{"items":[]}`), nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := adapter.Start(ctx, contracts.StartRequest{
		ProfileID: "real-codex", RunID: "real-codex", GrantCredential: credential,
		AllowedMethods: []string{"message.history"}, Proxy: runProxy,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := session.Close(closeCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	var mu sync.Mutex
	var updates []string
	result, err := session.Turn(ctx, contracts.TurnRequest{
		RunID: "real-codex", EventID: "real-codex-event",
		Prompt: "Run `\"$ABDIM_CLI\" commands`. Then run `printf '%s' '{\"conversation_id\":\"conversation-1\",\"limit\":1}' | \"$ABDIM_CLI\" message history --params-stdin`. If the second command returns ok=true with an empty items array, reply with exactly ABDIM_CLI_REAL_OK and nothing else.",
		Output: func(_ context.Context, output contracts.TurnOutput) error {
			mu.Lock()
			updates = append(updates, output.Text)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	mu.Lock()
	latest := ""
	if len(updates) != 0 {
		latest = updates[len(updates)-1]
	}
	mu.Unlock()
	if strings.TrimSpace(result.FinalText) != "ABDIM_CLI_REAL_OK" || strings.TrimSpace(latest) != "ABDIM_CLI_REAL_OK" || !called.Load() {
		t.Fatalf("Codex response: final=%q latest=%q called=%v", result.FinalText, latest, called.Load())
	}
}

func buildRealCLI(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../.."))
	path := filepath.Join(t.TempDir(), "abdim")
	command := exec.Command("go", "build", "-o", path, "./cmd/abdim")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build abdim: %v: %s", err, output)
	}
	return path
}
