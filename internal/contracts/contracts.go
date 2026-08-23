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

type ConversationKind string

const (
	ConversationKindChat           ConversationKind = "chat"
	ConversationKindAgentWorkspace ConversationKind = "agent_workspace"
)

type ConversationClassifier interface {
	ConversationKind(context.Context, string) (ConversationKind, error)
}

var (
	ErrInvalidContract = errors.New("invalid v1 contract")
	ErrSessionNotFound = errors.New("provider session not found")
)

// Request is the JSON envelope sent over the daemon's local RPC socket.
type Request struct {
	APIVersion string          `json:"api_version"`
	RequestID  string          `json:"request_id"`
	ProfileID  string          `json:"profile_id"`
	As         string          `json:"as"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
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
	if r.As != "user" && r.As != "bot" {
		return contractError("as must be user or bot")
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
	ProfileID string `json:"profile_id"`
	Stale     bool   `json:"stale"`
	Schema    string `json:"schema,omitempty"`
}

// Response is the JSON envelope returned by local RPC.
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
	CodeConnectionUnavailable ErrorCode = "CONNECTION_UNAVAILABLE"
	CodeSDKError              ErrorCode = "SDK_ERROR"
	CodeCursorExpired         ErrorCode = "CURSOR_EXPIRED"
	CodeInternal              ErrorCode = "INTERNAL"
)

func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidArgument, CodeDaemonUnavailable, CodeDaemonNotReady,
		CodeProtocolUnsupported, CodeAuthLocked, CodeConnectionUnavailable,
		CodeSDKError, CodeCursorExpired, CodeInternal:
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

// SDKEvent is a normalized SDK callback before the daemon persists it. The
// ledger, not the SDK adapter, assigns the public event ID and sequence.
type SDKEvent struct {
	ProfileID  string
	Type       string
	OccurredAt time.Time
	DedupKey   string
	Data       json.RawMessage
	// MessageText is transient provider input. It is excluded from ledger,
	// IPC and JSON serialization.
	MessageText  string        `json:"-"`
	MessageQuote *MessageQuote `json:"-"`
}

// MessageQuote is untrusted, transient context attached to an inbound reply.
type MessageQuote struct {
	Text              string
	Offset            int32
	SourceClientMsgID string
	SourceServerMsgID string
}

func (e SDKEvent) Validate() error {
	if strings.TrimSpace(e.ProfileID) == "" {
		return contractError("SDK event profile_id is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return contractError("SDK event type is required")
	}
	if strings.TrimSpace(e.DedupKey) == "" {
		return contractError("SDK event dedup_key is required")
	}
	if !json.Valid(e.Data) {
		return contractError("SDK event data must be valid JSON")
	}
	return nil
}

// SDK is the small lifecycle boundary used by the daemon bridge.
type SDK interface {
	InitSDK(context.Context) error
	InitResources(context.Context) error
	SetEventListener(EventListener) error
	Login(context.Context) error
	Shutdown(context.Context) error
}

type EventListener func(context.Context, SDKEvent)

// Provider and Session are the configured provider adapter boundary.
type Provider interface {
	Start(context.Context, StartRequest) (Session, error)
}

type StartRequest struct {
	ProfileID string
	RunID     string
	// StateKey is a stable, opaque key for provider state shared by runs in
	// the same conversation. It must not contain the conversation ID itself.
	StateKey   string
	SessionRef string
}

type Session interface {
	Turn(context.Context, TurnRequest) (TurnResult, error)
	Cancel(context.Context) error
	Close(context.Context) error
}

type TurnRequest struct {
	RunID   string
	EventID string
	// Prompt is transient provider input derived from the SDK callback.
	Prompt string
	// Events is optional. Agent workspace turns persist canonical run events;
	// normal chat turns consume final assistant message deltas.
	Events RunEventSink
}

type TurnResult struct {
	FinalText   string
	ToolSummary []string
	SessionRef  string
	Diagnostic  string
}
