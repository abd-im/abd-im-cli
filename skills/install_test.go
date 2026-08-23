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

func TestInstallABDHermesDoesNotReplaceExistingSkill(t *testing.T) {
	home := t.TempDir()
	if err := InstallABDHermes(home); err != nil {
		t.Fatal(err)
	}
	if err := InstallABDHermes(home); err != nil {
		t.Fatalf("second install: %v", err)
	}

	path := filepath.Join(home, "skills", "abd-im", "SKILL.md")
	if err := os.WriteFile(path, []byte("user skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallABDHermes(home); err != nil {
		t.Fatalf("install with existing skill: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user skill\n" {
		t.Fatalf("existing skill was replaced: %q", data)
	}
}
