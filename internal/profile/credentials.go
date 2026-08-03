package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidToken = errors.New("invalid token input")
)

// CredentialStore resolves an opaque credential reference without exposing it
// through profile configuration.
type CredentialStore interface {
	Put(context.Context, string, []byte) (string, error)
	Get(context.Context, string) ([]byte, error)
}

// FileStore keeps the IM token in the current user's private data directory.
type FileStore struct {
	dir string
}

// NewFileStore stores the short-lived IM token in an owner-only file for the
// single supported local deployment model.
func NewFileStore(dataDir string) (*FileStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("data directory is required")
	}
	return &FileStore{dir: filepath.Join(dataDir, "abdim", "credentials")}, nil
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
	return ValidateName(profileName)
}

func (s *FileStore) path(profileName string) string {
	return filepath.Join(s.dir, profileName+".token")
}
