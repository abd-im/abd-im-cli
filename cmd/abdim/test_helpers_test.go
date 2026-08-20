package main

import (
	"path/filepath"
	"testing"
)

func testRoots(t *testing.T) commandRoots {
	t.Helper()
	root := t.TempDir()
	return commandRoots{
		configDir:  filepath.Join(root, "config"),
		dataDir:    filepath.Join(root, "data"),
		runtimeDir: filepath.Join(root, "runtime"),
	}
}
