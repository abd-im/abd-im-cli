// Package profile owns one daemon profile's local configuration, credential
// reference, private directories, and process lock.
package profile

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var profileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

var (
	ErrInvalidName       = errors.New("invalid profile name")
	ErrInvalidDeployment = errors.New("invalid deployment configuration")
	ErrInvalidAgent      = errors.New("invalid Agent provider")
)

const DefaultAgent = "codex"

type Identity string

const (
	IdentityUser Identity = "user"
	IdentityBot  Identity = "bot"
)

func ParseIdentity(value string) (Identity, error) {
	switch Identity(strings.TrimSpace(value)) {
	case IdentityUser:
		return IdentityUser, nil
	case IdentityBot:
		return IdentityBot, nil
	default:
		return "", errors.New("identity must be user or bot")
	}
}

// Account identifies one locally logged-in SDK context. CredentialRef is
// opaque and never contains a token or an absolute local path.
type Account struct {
	UserID        string
	CredentialRef string
}

// Profile contains the two identities owned by one daemon process.
type Profile struct {
	Name       string
	User       Account
	Bot        Account
	Agent      string
	Deployment Deployment
}

func NormalizeAgent(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultAgent
	}
	switch value {
	case "codex", "hermes", "openclaw":
		return value, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAgent, value)
	}
}

// Deployment is the shared non-secret server configuration for both SDKs.
type Deployment struct {
	APIAddr    string
	WSAddr     string
	PlatformID int32
}

// Paths describes the exclusive on-disk resources of one profile.
type Paths struct {
	ProfileID  string
	ConfigFile string
	DataDir    string
	UserSDKDir string
	BotSDKDir  string
	ControlDB  string
	LogsDir    string
	RuntimeDir string
	RunsDir    string
	Socket     string
	LockFile   string
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
		ProfileID:  profileName,
		ConfigFile: filepath.Join(configDir, "abdim", "profiles", profileName+".toml"),
		DataDir:    profileDataDir,
		UserSDKDir: filepath.Join(profileDataDir, "sdk", string(IdentityUser)),
		BotSDKDir:  filepath.Join(profileDataDir, "sdk", string(IdentityBot)),
		ControlDB:  filepath.Join(profileDataDir, "control.db"),
		LogsDir:    filepath.Join(profileDataDir, "logs"),
		RuntimeDir: profileRuntimeDir,
		RunsDir:    filepath.Join(profileRuntimeDir, "runs"),
		Socket:     filepath.Join(profileRuntimeDir, "daemon.sock"),
		LockFile:   filepath.Join(profileRuntimeDir, "daemon.lock"),
	}, nil
}

// EnsurePrivate creates the directories owned by this profile with owner-only
// permissions. Existing directories are tightened as well.
func (p Paths) EnsurePrivate() error {
	for _, dir := range []string{
		filepath.Dir(p.ConfigFile), p.DataDir, p.UserSDKDir, p.BotSDKDir,
		p.LogsDir, p.RuntimeDir, p.RunsDir,
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

// Configured reports whether all deployment inputs have been supplied.
func (d Deployment) Configured() bool {
	return strings.TrimSpace(d.APIAddr) != "" || strings.TrimSpace(d.WSAddr) != "" || d.PlatformID != 0
}

// Validate verifies complete, non-secret SDK deployment inputs.
func (d Deployment) Validate() error {
	if d.PlatformID <= 0 {
		return fmt.Errorf("%w: positive platform ID is required", ErrInvalidDeployment)
	}
	if err := validateEndpoint(d.APIAddr, "http", "https"); err != nil {
		return err
	}
	return validateEndpoint(d.WSAddr, "ws", "wss")
}

func validateEndpoint(raw string, schemes ...string) error {
	endpoint, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("%w: invalid server endpoint", ErrInvalidDeployment)
	}
	for _, scheme := range schemes {
		if endpoint.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid server endpoint scheme", ErrInvalidDeployment)
}

// Save writes a profile reference file without ever accepting credential data.
func Save(path string, profile Profile) error {
	if err := ValidateName(profile.Name); err != nil {
		return err
	}
	if err := validateAccount(IdentityUser, profile.User); err != nil {
		return err
	}
	if err := validateAccount(IdentityBot, profile.Bot); err != nil {
		return err
	}
	if profile.Deployment.Configured() {
		if err := profile.Deployment.Validate(); err != nil {
			return err
		}
	}
	agent, err := NormalizeAgent(profile.Agent)
	if err != nil {
		return err
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
	contents := "name = " + strconv.Quote(profile.Name) + "\n"
	contents += "user_id = " + strconv.Quote(profile.User.UserID) + "\n"
	contents += "user_credential_ref = " + strconv.Quote(profile.User.CredentialRef) + "\n"
	contents += "bot_id = " + strconv.Quote(profile.Bot.UserID) + "\n"
	contents += "bot_credential_ref = " + strconv.Quote(profile.Bot.CredentialRef) + "\n"
	contents += "agent = " + strconv.Quote(agent) + "\n"
	if profile.Deployment.Configured() {
		contents += "api_addr = " + strconv.Quote(profile.Deployment.APIAddr) + "\n"
		contents += "ws_addr = " + strconv.Quote(profile.Deployment.WSAddr) + "\n"
		contents += "platform_id = " + strconv.FormatInt(int64(profile.Deployment.PlatformID), 10) + "\n"
	}
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

	values := make(map[string]string, 10)
	var platformID int32
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
		if key != "name" && key != "user_id" && key != "user_credential_ref" && key != "bot_id" && key != "bot_credential_ref" && key != "agent" && key != "api_addr" && key != "ws_addr" && key != "platform_id" {
			return Profile{}, fmt.Errorf("unsupported profile field %q", key)
		}
		if _, exists := values[key]; exists {
			return Profile{}, fmt.Errorf("duplicate profile field %q", key)
		}
		if key == "platform_id" {
			value, err := strconv.ParseInt(strings.TrimSpace(rawValue), 10, 32)
			if err != nil {
				return Profile{}, fmt.Errorf("decode profile field %q: %w", key, err)
			}
			platformID = int32(value)
			values[key] = "configured"
			continue
		}
		value, err := strconv.Unquote(strings.TrimSpace(rawValue))
		if err != nil {
			return Profile{}, fmt.Errorf("decode profile field %q: %w", key, err)
		}
		values[key] = value
	}

	agent, err := NormalizeAgent(values["agent"])
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Name:       values["name"],
		User:       Account{UserID: values["user_id"], CredentialRef: values["user_credential_ref"]},
		Bot:        Account{UserID: values["bot_id"], CredentialRef: values["bot_credential_ref"]},
		Agent:      agent,
		Deployment: Deployment{APIAddr: values["api_addr"], WSAddr: values["ws_addr"], PlatformID: platformID},
	}
	if err := ValidateName(profile.Name); err != nil {
		return Profile{}, err
	}
	if err := validateAccount(IdentityUser, profile.User); err != nil {
		return Profile{}, err
	}
	if err := validateAccount(IdentityBot, profile.Bot); err != nil {
		return Profile{}, err
	}
	if profile.Deployment.Configured() {
		if err := profile.Deployment.Validate(); err != nil {
			return Profile{}, err
		}
	}
	return profile, nil
}

func validateAccount(identity Identity, account Account) error {
	if strings.TrimSpace(account.UserID) == "" {
		return fmt.Errorf("%s user ID is required", identity)
	}
	if strings.TrimSpace(account.CredentialRef) == "" || filepath.IsAbs(account.CredentialRef) {
		return fmt.Errorf("invalid %s credential reference", identity)
	}
	return nil
}
