// Package contracts defines the versioned boundary between abdim components.
package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const APIVersionV1 = "v1"

var ErrInvalidContract = errors.New("invalid v1 contract")

// Request is the JSON envelope sent over local RPC and run-scoped tool proxies.
type Request struct {
	APIVersion     string          `json:"api_version"`
	RequestID      string          `json:"request_id"`
	ProfileID      string          `json:"profile_id"`
	Method         string          `json:"method"`
	Params         json.RawMessage `json:"params"`
	Grant          string          `json:"grant,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

func (r Request) Validate() error {
	if r.APIVersion != APIVersionV1 {
		return contractError("api_version must be %q", APIVersionV1)
	}
	if strings.TrimSpace(r.RequestID) == "" {
		return contractError("request_id is required")
	}
	if strings.TrimSpace(r.ProfileID) == "" {
		return contractError("profile_id is required")
	}
	if strings.TrimSpace(r.Method) == "" {
		return contractError("method is required")
	}
	if !json.Valid(r.Params) {
		return contractError("params must be valid JSON")
	}
	return nil
}

// Meta accompanies successful responses.
type Meta struct {
	ProfileID  string      `json:"profile_id"`
	Stale      bool        `json:"stale"`
	Schema     string      `json:"schema,omitempty"`
	Capability *Capability `json:"capability,omitempty"`
}

// Capability describes why a typed method is or is not exposed. It is
// optional in the shared envelope so existing lifecycle responses remain
// byte-for-byte compatible while typed reads can surface verification state.
type Capability struct {
	Method        string `json:"method"`
	Scope         string `json:"scope"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	SDKVersion    string `json:"sdk_version,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
}

// Response is the JSON envelope returned by local RPC and tool proxies.
type Response struct {
	APIVersion string          `json:"api_version"`
	RequestID  string          `json:"request_id"`
	OK         bool            `json:"ok"`
	Data       json.RawMessage `json:"data,omitempty"`
	Meta       *Meta           `json:"meta,omitempty"`
	Error      *Error          `json:"error,omitempty"`
}

func (r Response) Validate() error {
	if r.APIVersion != APIVersionV1 {
		return contractError("api_version must be %q", APIVersionV1)
	}
	if strings.TrimSpace(r.RequestID) == "" {
		return contractError("request_id is required")
	}
	if r.OK {
		if r.Error != nil {
			return contractError("successful response cannot contain error")
		}
		if r.Meta == nil || strings.TrimSpace(r.Meta.ProfileID) == "" {
			return contractError("successful response requires meta.profile_id")
		}
		if !json.Valid(r.Data) {
			return contractError("successful response requires valid JSON data")
		}
		return nil
	}
	if r.Error == nil {
		return contractError("failed response requires error")
	}
	if len(r.Data) != 0 || r.Meta != nil {
		return contractError("failed response cannot contain data or meta")
	}
	return r.Error.Validate()
}

// ErrorCode is a stable, machine-readable failure reason.
type ErrorCode string

const (
	CodeInvalidArgument       ErrorCode = "INVALID_ARGUMENT"
	CodeDaemonUnavailable     ErrorCode = "DAEMON_UNAVAILABLE"
	CodeDaemonNotReady        ErrorCode = "DAEMON_NOT_READY"
	CodeProtocolUnsupported   ErrorCode = "PROTOCOL_UNSUPPORTED"
	CodeAuthLocked            ErrorCode = "AUTH_LOCKED"
	CodeGrantInvalid          ErrorCode = "GRANT_INVALID"
	CodePolicyDenied          ErrorCode = "POLICY_DENIED"
	CodeIdempotencyConflict   ErrorCode = "IDEMPOTENCY_CONFLICT"
	CodeConnectionUnavailable ErrorCode = "CONNECTION_UNAVAILABLE"
	CodeSDKError              ErrorCode = "SDK_ERROR"
	CodeOutcomeUnknown        ErrorCode = "OUTCOME_UNKNOWN"
	CodeCursorExpired         ErrorCode = "CURSOR_EXPIRED"
	CodeConfirmationRequired  ErrorCode = "CONFIRMATION_REQUIRED"
	CodeInternal              ErrorCode = "INTERNAL"
)

func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidArgument, CodeDaemonUnavailable, CodeDaemonNotReady,
		CodeProtocolUnsupported, CodeAuthLocked, CodeGrantInvalid, CodePolicyDenied,
		CodeIdempotencyConflict, CodeConnectionUnavailable, CodeSDKError,
		CodeOutcomeUnknown, CodeCursorExpired, CodeConfirmationRequired, CodeInternal:
		return true
	default:
		return false
	}
}

// Error is returned when a request cannot be completed.
type Error struct {
	Code      ErrorCode       `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

func (e Error) Validate() error {
	if !e.Code.Valid() {
		return contractError("unsupported error code %q", e.Code)
	}
	if strings.TrimSpace(e.Message) == "" {
		return contractError("error message is required")
	}
	if len(e.Details) != 0 && !json.Valid(e.Details) {
		return contractError("error details must be valid JSON")
	}
	return nil
}

// Event is a normalized daemon event. DedupKey is the SDK-specific identity
// used by the ledger and is distinct from the daemon-assigned EventID.
type Event struct {
	APIVersion string          `json:"api_version"`
	EventID    string          `json:"event_id"`
	ProfileID  string          `json:"profile_id"`
	Sequence   uint64          `json:"sequence"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	DedupKey   string          `json:"dedup_key,omitempty"`
	Data       json.RawMessage `json:"data"`
}

const (
	EventMessageReceived EventType = "message.received"
	EventStateReconciled EventType = "state.reconciled"
)

type EventType string

func (e Event) Validate() error {
	if e.APIVersion != APIVersionV1 {
		return contractError("api_version must be %q", APIVersionV1)
	}
	if strings.TrimSpace(e.EventID) == "" {
		return contractError("event_id is required")
	}
	if strings.TrimSpace(e.ProfileID) == "" {
		return contractError("profile_id is required")
	}
	if e.Sequence == 0 {
		return contractError("sequence is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return contractError("type is required")
	}
	if e.OccurredAt.IsZero() {
		return contractError("occurred_at is required")
	}
	if !json.Valid(e.Data) {
		return contractError("data must be valid JSON")
	}
	return nil
}

func contractError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidContract, fmt.Sprintf(format, args...))
}

// SDK is the small lifecycle boundary used by the daemon bridge.
type SDK interface {
	InitSDK(context.Context) error
	InitResources(context.Context) error
	SetEventListener(EventListener) error
	Login(context.Context) error
	Shutdown(context.Context) error
}

type EventListener func(context.Context, Event)

// Provider and Session are the configured provider adapter boundary.
type Provider interface {
	Start(context.Context, StartRequest) (Session, error)
}

type StartRequest struct {
	ProfileID       string
	RunID           string
	GrantCredential string
	Proxy           ToolProxy
}

type Session interface {
	Turn(context.Context, TurnRequest) (TurnResult, error)
	Cancel(context.Context) error
	Close(context.Context) error
}

type TurnRequest struct {
	RunID           string
	EventID         string
	GrantCredential string
}

type TurnResult struct {
	FinalText   string
	ToolSummary []string
	SessionRef  string
	Diagnostic  string
}

// ToolProxy is the only request path made available to a restricted provider.
type ToolProxy interface {
	Call(context.Context, Request) (Response, error)
	Close(context.Context) error
}
