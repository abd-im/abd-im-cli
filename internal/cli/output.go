// Package cli provides daemon-facing command behavior without owning transport
// or SDK initialization.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

// Output selects a structured CLI rendering format.
type Output string

const (
	OutputJSON  Output = "json"
	OutputJSONL Output = "jsonl"
)

// WriteResponse renders one RPC envelope without mixing progress output into
// stdout. JSONL is one envelope per line, as required for watch streams.
func WriteResponse(writer io.Writer, output Output, response contracts.Response) error {
	if output != OutputJSON && output != OutputJSONL {
		return fmt.Errorf("unsupported output format %q", output)
	}
	if err := response.Validate(); err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(response)
}

// WriteEvent writes one normalized event as a JSONL record.
func WriteEvent(writer io.Writer, event contracts.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(event)
}

// ExitCode maps stable RPC errors to stable process outcomes.
func ExitCode(response contracts.Response) int {
	if response.OK {
		return 0
	}
	if response.Error == nil {
		return 1
	}
	switch response.Error.Code {
	case contracts.CodeInvalidArgument, contracts.CodeProtocolUnsupported:
		return 2
	case contracts.CodeDaemonUnavailable, contracts.CodeDaemonNotReady, contracts.CodeConnectionUnavailable:
		return 3
	case contracts.CodeAuthLocked:
		return 4
	default:
		return 1
	}
}

// AuthImportOptions carries only paths and explicit fallback authorization.
// It deliberately has no token field.
type AuthImportOptions struct {
	ProfileName    string
	ConfigDir      string
	DataDir        string
	RuntimeDir     string
	AllowPlaintext bool
	RequestID      string
}

// ImportToken imports a token from stdin-like input, writes only its opaque
// credential reference to the profile, and returns a token-free response.
func ImportToken(ctx context.Context, input io.Reader, options AuthImportOptions) (contracts.Response, error) {
	paths, err := profile.NewPaths(options.ConfigDir, options.DataDir, options.RuntimeDir, options.ProfileName)
	if err != nil {
		return contracts.Response{}, err
	}
	store, err := profile.NewFileStore(options.DataDir, options.AllowPlaintext)
	if err != nil {
		return contracts.Response{}, err
	}
	item := profile.Profile{Name: options.ProfileName}
	if existing, err := profile.Load(paths.ConfigFile); err == nil {
		item = existing
	} else if !errors.Is(err, fs.ErrNotExist) {
		return contracts.Response{}, err
	}
	item, err = profile.ImportToken(ctx, input, store, item)
	if err != nil {
		return contracts.Response{}, err
	}
	if err := profile.Save(paths.ConfigFile, item); err != nil {
		return contracts.Response{}, err
	}
	payload, err := json.Marshal(struct {
		ProfileID          string `json:"profile_id"`
		CredentialImported bool   `json:"credential_imported"`
	}{ProfileID: options.ProfileName, CredentialImported: true})
	if err != nil {
		return contracts.Response{}, err
	}
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  options.RequestID,
		OK:         true,
		Data:       payload,
		Meta:       &contracts.Meta{ProfileID: options.ProfileName},
	}, nil
}

// ErrorResponse converts local command errors into the shared protocol shape.
func ErrorResponse(requestID string, code contracts.ErrorCode, err error) contracts.Response {
	message := "command failed"
	if err != nil {
		message = err.Error()
	}
	return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: false, Error: &contracts.Error{Code: code, Message: message}}
}

// IsInvalidArgument identifies local argument and input failures.
func IsInvalidArgument(err error) bool {
	return errors.Is(err, profile.ErrInvalidName) || errors.Is(err, profile.ErrInvalidDeployment) || errors.Is(err, profile.ErrPlaintextDisabled) || errors.Is(err, profile.ErrInvalidToken)
}
