//go:build unix

package launcher

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigRequiresCompleteControlledFields(t *testing.T) {
	config, err := parseConfig([]byte("uid = 123\ngid = 456\nhome = \"/var/lib/abdim-provider\"\ncodex_home = \"/var/lib/abdim-provider/.codex\"\ncodex_path = \"/usr/local/bin/codex\"\nrun_root = \"/run/abdim-provider\"\n"))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if config.UID != 123 || config.GID != 456 || config.CodexHome != "/var/lib/abdim-provider/.codex" {
		t.Fatalf("parseConfig() = %#v", config)
	}
	if _, err := parseConfig([]byte("uid = 1\ngid = 1\nhome = \"/provider\"\ncodex_home = \"/elsewhere\"\ncodex_path = \"/usr/local/bin/codex\"\nrun_root = \"/run/provider\"\n")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("parseConfig() accepted a Codex home outside provider home: %v", err)
	}
}

func TestLoadRejectsUncontrolledConfigPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.toml")
	if err := os.WriteFile(path, []byte("uid = 1\ngid = 1\nhome = \"/provider\"\ncodex_home = \"/provider/.codex\"\ncodex_path = \"/usr/local/bin/codex\"\nrun_root = \"/run/provider\"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestConfigureClearsInheritedProcessIdentity(t *testing.T) {
	value := &Launcher{config: Config{UID: 1234, GID: 5678}}
	command := exec.Command("true")
	if err := value.Configure(command); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if command.SysProcAttr == nil || command.SysProcAttr.Credential == nil {
		t.Fatal("Configure() did not set a process credential")
	}
	credential := command.SysProcAttr.Credential
	if credential.Uid != 1234 || credential.Gid != 5678 || len(credential.Groups) != 1 || credential.Groups[0] != 5678 {
		t.Fatalf("provider credential = %#v", credential)
	}
}

func TestPrepareSocketRestoresProviderTraversal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a daemon-owned test socket")
	}
	root := t.TempDir()
	socket := filepath.Join(root, "mcp.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	value := &Launcher{config: Config{UID: 65534, GID: 65534}}
	if err := value.PrepareSocket(socket); err != nil {
		t.Fatalf("PrepareSocket() error = %v", err)
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0o600 || ownerID(info) != 65534 || groupID(info) != 65534 {
		t.Fatalf("provider socket = %v, %v", info, err)
	}
	parent, err := os.Stat(root)
	if err != nil || parent.Mode().Perm() != 0o711 {
		t.Fatalf("provider socket parent = %v, %v", parent, err)
	}
}
