// Package skills embeds the static Agent skills shipped with abdim.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed abd-im
var content embed.FS

// InstallABD installs the bundled ABD IM skill in one Agent workspace.
func InstallABD(workspace string) error {
	return installABD(filepath.Join(workspace, ".agents", "skills"))
}

// InstallABDHermes installs the bundled skill in a Hermes home without replacing an existing skill.
func InstallABDHermes(home string) error {
	target := filepath.Join(home, "skills", "abd-im")
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect Hermes ABD IM skill: %w", err)
	}
	return installABD(filepath.Join(home, "skills"))
}

func installABD(skillsDir string) error {
	return fs.WalkDir(content, "abd-im", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(skillsDir, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read bundled skill: %w", err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write bundled skill: %w", err)
		}
		return nil
	})
}
