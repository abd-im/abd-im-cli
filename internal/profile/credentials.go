package profile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrPlaintextDisabled = errors.New("plaintext credential fallback is not enabled")
	ErrInvalidToken      = errors.New("invalid token input")
)

// CredentialStore resolves an opaque credential reference. Implementations of
// system keyrings and the explicit file fallback satisfy this boundary.
type CredentialStore interface {
	Put(context.Context, string, []byte) (string, error)
	Get(context.Context, string) ([]byte, error)
}

// FileStore is an explicit owner-only plaintext fallback for environments
// without a system credential store. It is never enabled implicitly.
type FileStore struct {
	dir     string
	enabled bool
}

// NewFileStore returns a disabled store unless allowPlaintext is explicit.
func NewFileStore(dataDir string, allowPlaintext bool) (*FileStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("data directory is required")
	}
	return &FileStore{dir: filepath.Join(dataDir, "abdim", "credentials"), enabled: allowPlaintext}, nil
}

func (s *FileStore) Put(_ context.Context, profileName string, token []byte) (string, error) {
	if err := s.allowed(profileName); err != nil {
		return "", err
	}
	if len(token) == 0 {
		return "", fmt.Errorf("%w: token is required", ErrInvalidToken)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return "", fmt.Errorf("secure credential directory: %w", err)
	}

	path := s.path(profileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("open credential file: %w", err)
	}
	if _, err := file.Write(token); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close credential file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure credential file: %w", err)
	}
	return "file:" + profileName, nil
}

func (s *FileStore) Get(_ context.Context, reference string) ([]byte, error) {
	if !s.enabled {
		return nil, ErrPlaintextDisabled
	}
	profileName, found := strings.CutPrefix(reference, "file:")
	if !found {
		return nil, errors.New("unsupported credential reference")
	}
	if err := ValidateName(profileName); err != nil {
		return nil, err
	}
	token, err := os.ReadFile(s.path(profileName))
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	return token, nil
}

func (s *FileStore) allowed(profileName string) error {
	if !s.enabled {
		return ErrPlaintextDisabled
	}
	return ValidateName(profileName)
}

func (s *FileStore) path(profileName string) string {
	return filepath.Join(s.dir, profileName+".token")
}

// ImportToken reads a token from stdin-like input and persists only through the
// configured credential store. The token is never placed in a command argument
// or returned to the caller.
func ImportToken(ctx context.Context, input io.Reader, store CredentialStore, profile Profile) (Profile, error) {
	if input == nil || store == nil {
		return Profile{}, errors.New("token input and credential store are required")
	}
	if err := ValidateName(profile.Name); err != nil {
		return Profile{}, err
	}
	const maxTokenBytes = 64 * 1024
	scanner := bufio.NewScanner(io.LimitReader(input, maxTokenBytes+1))
	scanner.Buffer(make([]byte, 1024), maxTokenBytes+1)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Profile{}, fmt.Errorf("read token input: %w", err)
		}
		return Profile{}, fmt.Errorf("%w: token is required", ErrInvalidToken)
	}
	if len(scanner.Bytes()) > maxTokenBytes {
		return Profile{}, fmt.Errorf("%w: token input is too large", ErrInvalidToken)
	}
	token := []byte(strings.TrimSpace(scanner.Text()))
	if len(token) == 0 {
		return Profile{}, fmt.Errorf("%w: token is required", ErrInvalidToken)
	}
	reference, err := store.Put(ctx, profile.Name, token)
	if err != nil {
		return Profile{}, fmt.Errorf("store token: %w", err)
	}
	profile.CredentialRef = reference
	return profile, nil
}
