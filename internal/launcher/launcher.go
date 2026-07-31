// Package launcher starts restricted provider processes from a controlled
// deployment configuration.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Config identifies the independent account and filesystem roots for one
// provider deployment. It contains no daemon or IM credentials.
type Config struct {
	UID       uint32
	GID       uint32
	Home      string
	CodexHome string
	CodexPath string
	RunRoot   string
}

// Runner prepares a daemon-owned run directory and starts the provider with
// the deployment's restricted process identity.
type Runner interface {
	CopyCodexAuth(destination string) error
	PrepareRun(root, home, workDir string) error
	PrepareSocket(socket string) error
	Configure(*exec.Cmd) error
}

// Launcher is the Unix release implementation of Runner.
type Launcher struct {
	config Config
}

// Load reads a root-controlled deployment configuration and validates that it
// can start a process under the configured independent identity.
func Load(path string) (*Launcher, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("provider launcher config must be an absolute path")
	}
	if err := validateControlFile(path); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider launcher config: %w", err)
	}
	config, err := parseConfig(contents)
	if err != nil {
		return nil, err
	}
	return New(config)
}

// New validates the configured identity and directories. It is exposed so
// deployment systems can supply the same values without a local TOML file.
func New(config Config) (*Launcher, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if err := validateRuntime(config); err != nil {
		return nil, err
	}
	return &Launcher{config: config}, nil
}

// CodexHome returns the independently owned source Codex home. The adapter
// copies only its authentication material into each ephemeral run home.
func (l *Launcher) CodexHome() string {
	if l == nil {
		return ""
	}
	return l.config.CodexHome
}

// CodexPath returns the root-controlled Codex executable used for provider
// processes.
func (l *Launcher) CodexPath() string {
	if l == nil {
		return ""
	}
	return l.config.CodexPath
}

// WorkingDir creates the root-owned, provider-traversable parent for a
// profile's ephemeral runs.
func (l *Launcher) WorkingDir(profileID string) (string, error) {
	if l == nil || !validPathComponent(profileID) {
		return "", errors.New("provider profile ID is invalid")
	}
	path := filepath.Join(l.config.RunRoot, profileID)
	if err := prepareWorkingDir(path); err != nil {
		return "", err
	}
	return path, nil
}

func parseConfig(contents []byte) (Config, error) {
	values := make(map[string]string, 6)
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found {
			return Config{}, errors.New("invalid provider launcher config")
		}
		key = strings.TrimSpace(key)
		if key != "uid" && key != "gid" && key != "home" && key != "codex_home" && key != "codex_path" && key != "run_root" {
			return Config{}, fmt.Errorf("unsupported provider launcher field %q", key)
		}
		if _, exists := values[key]; exists {
			return Config{}, fmt.Errorf("duplicate provider launcher field %q", key)
		}
		values[key] = strings.TrimSpace(rawValue)
	}

	uid, err := parseID(values["uid"], "uid")
	if err != nil {
		return Config{}, err
	}
	gid, err := parseID(values["gid"], "gid")
	if err != nil {
		return Config{}, err
	}
	home, err := parseString(values["home"], "home")
	if err != nil {
		return Config{}, err
	}
	codexHome, err := parseString(values["codex_home"], "codex_home")
	if err != nil {
		return Config{}, err
	}
	codexPath, err := parseString(values["codex_path"], "codex_path")
	if err != nil {
		return Config{}, err
	}
	runRoot, err := parseString(values["run_root"], "run_root")
	if err != nil {
		return Config{}, err
	}
	config := Config{UID: uid, GID: gid, Home: home, CodexHome: codexHome, CodexPath: codexPath, RunRoot: runRoot}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func parseID(value, name string) (uint32, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("provider launcher %s is required", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("provider launcher %s must be a positive integer", name)
	}
	return uint32(parsed), nil
}

func parseString(value, name string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("provider launcher %s is required", name)
	}
	parsed, err := strconv.Unquote(value)
	if err != nil || strings.TrimSpace(parsed) == "" {
		return "", fmt.Errorf("provider launcher %s must be a quoted path", name)
	}
	return parsed, nil
}

func validateConfig(config Config) error {
	if config.UID == 0 || config.GID == 0 {
		return errors.New("provider launcher UID and GID must be non-root")
	}
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "home", path: config.Home},
		{name: "codex_home", path: config.CodexHome},
		{name: "codex_path", path: config.CodexPath},
		{name: "run_root", path: config.RunRoot},
	} {
		if !filepath.IsAbs(item.path) || filepath.Clean(item.path) != item.path {
			return fmt.Errorf("provider launcher %s must be an absolute clean path", item.name)
		}
	}
	if !pathContains(config.Home, config.CodexHome) {
		return errors.New("provider launcher codex_home must be inside provider home")
	}
	if pathContains(config.RunRoot, config.Home) || pathContains(config.Home, config.RunRoot) {
		return errors.New("provider launcher run_root must not overlap provider home")
	}
	return nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func validPathComponent(value string) bool {
	return value != "" && filepath.Base(value) == value && value != "." && value != ".."
}
