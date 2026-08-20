package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallABD(t *testing.T) {
	workspace := t.TempDir()
	if err := InstallABD(workspace); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, ".agents", "skills", "abd-im", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: abd-im") {
		t.Fatalf("installed SKILL.md = %q", data)
	}
}
