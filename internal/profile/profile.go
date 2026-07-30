// Package profile owns one daemon profile's local configuration, credential
// reference, private directories, and process lock.
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var profileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

var ErrInvalidName = errors.New("invalid profile name")

// Profile contains public profile metadata. CredentialRef is opaque and never
// contains a token or an absolute local path.
type Profile struct {
	Name          string
	CredentialRef string
}

// Paths describes the exclusive on-disk resources of one profile.
type Paths struct {
	ConfigFile     string
	DataDir        string
	SDKDir         string
	ControlDB      string
	AttachmentsDir string
	LogsDir        string
	RuntimeDir     string
	Socket         string
	Descriptor     string
	LockFile       string
}

// NewPaths creates the layout defined by the product specification.
func NewPaths(configDir, dataDir, runtimeDir, profileName string) (Paths, error) {
	if err := ValidateName(profileName); err != nil {
		return Paths{}, err
	}
	if strings.TrimSpace(configDir) == "" || strings.TrimSpace(dataDir) == "" || strings.TrimSpace(runtimeDir) == "" {
		return Paths{}, errors.New("config, data, and runtime directories are required")
	}

	profileDataDir := filepath.Join(dataDir, "abdim", "profiles", profileName)
	profileRuntimeDir := filepath.Join(runtimeDir, "abdim", profileName)
	return Paths{
		ConfigFile:     filepath.Join(configDir, "abdim", "profiles", profileName+".toml"),
		DataDir:        profileDataDir,
		SDKDir:         filepath.Join(profileDataDir, "sdk"),
		ControlDB:      filepath.Join(profileDataDir, "control.db"),
		AttachmentsDir: filepath.Join(profileDataDir, "attachments"),
		LogsDir:        filepath.Join(profileDataDir, "logs"),
		RuntimeDir:     profileRuntimeDir,
		Socket:         filepath.Join(profileRuntimeDir, "daemon.sock"),
		Descriptor:     filepath.Join(profileRuntimeDir, "descriptor.json"),
		LockFile:       filepath.Join(profileRuntimeDir, "daemon.lock"),
	}, nil
}

// EnsurePrivate creates the directories owned by this profile with owner-only
// permissions. Existing directories are tightened as well.
func (p Paths) EnsurePrivate() error {
	for _, dir := range []string{
		filepath.Dir(p.ConfigFile), p.DataDir, p.SDKDir, p.AttachmentsDir,
		p.LogsDir, p.RuntimeDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create private directory %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure private directory %q: %w", dir, err)
		}
	}
	return nil
}

// ValidateName prevents a profile from escaping its dedicated directory.
func ValidateName(name string) error {
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// Save writes a profile reference file without ever accepting credential data.
func Save(path string, profile Profile) error {
	if err := ValidateName(profile.Name); err != nil {
		return err
	}
	if strings.TrimSpace(profile.CredentialRef) == "" {
		return errors.New("credential reference is required")
	}
	if filepath.IsAbs(profile.CredentialRef) {
		return errors.New("credential reference must not be an absolute path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("secure profile directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".profile-*.toml")
	if err != nil {
		return fmt.Errorf("create profile file: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure profile file: %w", err)
	}
	contents := "name = " + strconv.Quote(profile.Name) + "\ncredential_ref = " + strconv.Quote(profile.CredentialRef) + "\n"
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write profile file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close profile file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace profile file: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// Load reads the narrow profile TOML subset written by Save.
func Load(path string) (Profile, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read profile file: %w", err)
	}

	values := make(map[string]string, 2)
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found {
			return Profile{}, errors.New("invalid profile TOML")
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "credential_ref" {
			return Profile{}, fmt.Errorf("unsupported profile field %q", key)
		}
		value, err := strconv.Unquote(strings.TrimSpace(rawValue))
		if err != nil {
			return Profile{}, fmt.Errorf("decode profile field %q: %w", key, err)
		}
		if _, exists := values[key]; exists {
			return Profile{}, fmt.Errorf("duplicate profile field %q", key)
		}
		values[key] = value
	}

	profile := Profile{Name: values["name"], CredentialRef: values["credential_ref"]}
	if err := ValidateName(profile.Name); err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(profile.CredentialRef) == "" || filepath.IsAbs(profile.CredentialRef) {
		return Profile{}, errors.New("invalid credential reference")
	}
	return profile, nil
}
