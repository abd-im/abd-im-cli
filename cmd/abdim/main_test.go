package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
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
