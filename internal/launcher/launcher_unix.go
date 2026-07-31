//go:build unix

package launcher

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func validateControlFile(path string) error {
	if err := requireTraversableDirectory(filepath.Dir(path), 0, 0); err != nil {
		return fmt.Errorf("provider launcher config parent: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect provider launcher config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || ownerID(info) != 0 {
		return errors.New("provider launcher config must be a root-owned non-writable regular file")
	}
	return nil
}

func validateRuntime(config Config) error {
	if os.Geteuid() != 0 {
		return errors.New("isolated provider launcher requires a root-owned daemon process")
	}
	if err := requireTraversableDirectory(filepath.Dir(config.Home), 0, 0); err != nil {
		return fmt.Errorf("provider home parent: %w", err)
	}
	if err := requireDirectory(config.Home, config.UID, config.GID); err != nil {
		return fmt.Errorf("provider home: %w", err)
	}
	if err := requireDirectory(config.CodexHome, config.UID, config.GID); err != nil {
		return fmt.Errorf("provider Codex home: %w", err)
	}
	if err := requireTraversableDirectory(filepath.Dir(config.CodexPath), 0, 0); err != nil {
		return fmt.Errorf("provider Codex executable parent: %w", err)
	}
	if err := requireRegularFile(config.CodexPath, 0, 0); err != nil {
		return fmt.Errorf("provider Codex executable: %w", err)
	}
	if err := requireTraversableDirectory(filepath.Dir(config.RunRoot), 0, 0); err != nil {
		return fmt.Errorf("provider run root parent: %w", err)
	}
	if err := requireTraversableDirectory(config.RunRoot, 0, 0); err != nil {
		return fmt.Errorf("provider run root: %w", err)
	}
	return nil
}

func prepareWorkingDir(path string) error {
	if err := os.Mkdir(path, 0o711); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create provider run directory: %w", err)
	}
	if err := requireDirectory(path, 0, 0); err != nil {
		return fmt.Errorf("provider run directory: %w", err)
	}
	return os.Chmod(path, 0o711)
}

func (l *Launcher) PrepareRun(root, home, workDir string) error {
	if l == nil || filepath.Dir(home) != root || filepath.Dir(workDir) != root {
		return errors.New("provider run paths are invalid")
	}
	if err := requireDirectory(root, 0, 0); err != nil {
		return fmt.Errorf("provider run root: %w", err)
	}
	for _, path := range []string{home, workDir} {
		if err := chownTree(path, l.config.UID, l.config.GID); err != nil {
			return err
		}
	}
	return os.Chmod(root, 0o711)
}

// CopyCodexAuth copies the provider-owned authentication file without
// following provider-controlled path links. The daemon never reads another
// file from the provider home as root.
func (l *Launcher) CopyCodexAuth(destination string) error {
	if l == nil || !filepath.IsAbs(destination) {
		return errors.New("provider authentication destination is invalid")
	}
	directory, err := syscall.Open(l.config.CodexHome, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("open provider Codex home")
	}
	defer syscall.Close(directory)
	var directoryStat syscall.Stat_t
	if err := syscall.Fstat(directory, &directoryStat); err != nil || directoryStat.Mode&syscall.S_IFMT != syscall.S_IFDIR || directoryStat.Uid != l.config.UID || directoryStat.Gid != l.config.GID {
		return errors.New("provider Codex home is not owned by the configured provider")
	}
	fileDescriptor, err := syscall.Openat(directory, "auth.json", syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("open provider Codex authentication")
	}
	file := os.NewFile(uintptr(fileDescriptor), "provider auth.json")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || ownerID(info) != l.config.UID || groupID(info) != l.config.GID {
		return errors.New("provider Codex authentication is not an owned regular file")
	}
	payload, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(payload) > 1<<20 {
		return errors.New("read provider Codex authentication")
	}
	if err := os.WriteFile(destination, payload, 0o600); err != nil {
		return fmt.Errorf("write provider Codex authentication: %w", err)
	}
	return os.Chmod(destination, 0o600)
}

func (l *Launcher) PrepareSocket(socket string) error {
	if l == nil || filepath.Dir(socket) == "." {
		return errors.New("provider socket path is invalid")
	}
	if err := requireDirectory(filepath.Dir(socket), 0, 0); err != nil {
		return fmt.Errorf("provider socket parent: %w", err)
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return fmt.Errorf("inspect provider socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 || ownerID(info) != 0 {
		return errors.New("provider socket must be a daemon-owned Unix socket")
	}
	if err := os.Chown(socket, int(l.config.UID), int(l.config.GID)); err != nil {
		return fmt.Errorf("assign provider socket: %w", err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	return os.Chmod(filepath.Dir(socket), 0o711)
}

func (l *Launcher) Configure(command *exec.Cmd) error {
	if l == nil || command == nil {
		return errors.New("provider command is required")
	}
	attributes := command.SysProcAttr
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
	}
	attributes.Credential = &syscall.Credential{
		Uid:    l.config.UID,
		Gid:    l.config.GID,
		Groups: []uint32{l.config.GID},
	}
	command.SysProcAttr = attributes
	return nil
}

func requireDirectory(path string, uid, gid uint32) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || ownerID(info) != uid || groupID(info) != gid || info.Mode().Perm()&0o022 != 0 {
		return errors.New("must be an owner-controlled directory")
	}
	return nil
}

func requireTraversableDirectory(path string, uid, gid uint32) error {
	if err := requireDirectory(path, uid, gid); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o001 == 0 {
		return errors.New("must permit provider traversal")
	}
	return nil
}

func requireRegularFile(path string, uid, gid uint32) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || ownerID(info) != uid || groupID(info) != gid || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o001 == 0 {
		return errors.New("must be an owner-controlled regular file")
	}
	return nil
}

func chownTree(root string, uid, gid uint32) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("provider run contains a symlink")
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			return err
		}
		if path == root {
			return os.Chmod(path, 0o700)
		}
		if info.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
}

func ownerID(info os.FileInfo) uint32 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ^uint32(0)
	}
	return stat.Uid
}

func groupID(info os.FileInfo) uint32 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ^uint32(0)
	}
	return stat.Gid
}
