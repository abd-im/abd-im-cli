// Package cli provides daemon-facing command behavior without owning transport
// or SDK initialization.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

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
	return errors.Is(err, profile.ErrInvalidName) || errors.Is(err, profile.ErrInvalidDeployment) || errors.Is(err, profile.ErrInvalidToken)
}
