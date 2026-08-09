package control

import (
	"errors"
	"strings"
	"time"
)

// Profile identifies one daemon-owned isolation boundary.
type Profile struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Event is a durable inbound fact. It stores message and conversation IDs only.
type Event struct {
	ID             string
	ProfileID      string
	Sequence       uint64
	SDKDedupKey    string
	Type           string
	ConversationID string
	MessageID      string
	OccurredAt     time.Time
	RecordedAt     time.Time
}

func (event Event) validate() error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.ProfileID) == "" {
		return errors.New("event ID and profile ID are required")
	}
	if strings.TrimSpace(event.SDKDedupKey) == "" || strings.TrimSpace(event.Type) == "" {
		return errors.New("event SDK dedup key and type are required")
	}
	return nil
}

// OperationStatus records the terminal or uncertain state of a remote effect.
type OperationStatus string

const (
	OperationConfirmed OperationStatus = "confirmed"
	OperationFailed    OperationStatus = "failed"
	OperationUnknown   OperationStatus = "unknown"
)

// Operation is a remote side effect identified by its scope and idempotency key.
// InputDigest is a digest of canonical input rather than the input itself.
type Operation struct {
	ID             string
	ProfileID      string
	Scope          string
	IdempotencyKey string
	InputDigest    string
	TargetSummary  string
	Status         OperationStatus
	ErrorSummary   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RunStatus describes the lifecycle of a provider turn. It deliberately
// records identifiers and a bounded reason only, never prompts or output.
type RunStatus string

const (
	RunQueued      RunStatus = "queued"
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunInterrupted RunStatus = "interrupted"
	RunCancelled   RunStatus = "cancelled"
)

// Run is the durable operational record for one provider turn.
type Run struct {
	ID             string
	ProfileID      string
	ConversationID string
	EventID        string
	Status         RunStatus
	Reason         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProviderSession binds one IM conversation to opaque provider-owned state.
type ProviderSession struct {
	ProfileID      string
	ConversationID string
	Provider       string
	SessionRef     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (session ProviderSession) validate() error {
	if strings.TrimSpace(session.ProfileID) == "" || strings.TrimSpace(session.ConversationID) == "" || strings.TrimSpace(session.Provider) == "" || strings.TrimSpace(session.SessionRef) == "" {
		return errors.New("provider session profile, conversation, provider, and reference are required")
	}
	if len(session.SessionRef) > 1024 {
		return errors.New("provider session reference must not exceed 1024 bytes")
	}
	return nil
}

func (run Run) validate() error {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.ProfileID) == "" {
		return errors.New("run ID and profile ID are required")
	}
	if strings.TrimSpace(run.ConversationID) == "" || strings.TrimSpace(run.EventID) == "" {
		return errors.New("run conversation and event IDs are required")
	}
	switch run.Status {
	case RunQueued, RunRunning, RunCompleted, RunInterrupted, RunCancelled:
	default:
		return errors.New("run status must be queued, running, completed, interrupted, or cancelled")
	}
	if len(run.Reason) > 256 {
		return errors.New("run reason must not exceed 256 bytes")
	}
	return nil
}

// ReplySlot binds a provider result to the original event and conversation.
// It contains only identifiers, never reply text or message content.
type ReplySlot struct {
	ID                   string
	ProfileID            string
	EventID              string
	ConversationID       string
	TriggerMessageID     string
	RecipientID          string
	GroupID              string
	RunID                string
	OperationID          string
	BusinessConnectionID string
	CreatedAt            time.Time
}

func (slot ReplySlot) validate() error {
	if strings.TrimSpace(slot.ID) == "" || strings.TrimSpace(slot.ProfileID) == "" || strings.TrimSpace(slot.EventID) == "" {
		return errors.New("reply slot ID, profile ID, and event ID are required")
	}
	if strings.TrimSpace(slot.ConversationID) == "" || strings.TrimSpace(slot.TriggerMessageID) == "" || strings.TrimSpace(slot.RunID) == "" {
		return errors.New("reply slot conversation, trigger message, and run ID are required")
	}
	if (strings.TrimSpace(slot.RecipientID) == "" && strings.TrimSpace(slot.GroupID) == "") || (strings.TrimSpace(slot.RecipientID) != "" && strings.TrimSpace(slot.GroupID) != "") {
		return errors.New("reply slot requires exactly one private or group target")
	}
	return nil
}

func (operation Operation) validate() error {
	if strings.TrimSpace(operation.ID) == "" || strings.TrimSpace(operation.ProfileID) == "" {
		return errors.New("operation ID and profile ID are required")
	}
	if strings.TrimSpace(operation.Scope) == "" || strings.TrimSpace(operation.IdempotencyKey) == "" {
		return errors.New("operation scope and idempotency key are required")
	}
	if strings.TrimSpace(operation.InputDigest) == "" {
		return errors.New("operation input digest is required")
	}
	if len(operation.TargetSummary) > 256 || len(operation.ErrorSummary) > 256 {
		return errors.New("operation diagnostic summaries must not exceed 256 bytes")
	}
	switch operation.Status {
	case OperationConfirmed, OperationFailed, OperationUnknown:
		return nil
	default:
		return errors.New("operation status must be confirmed, failed, or unknown")
	}
}

// Attachment records a daemon-owned file reference without retaining its
// contents, original name, or local filesystem path.
type Attachment struct {
	ID        string
	ProfileID string
	RunID     string
	GrantID   string
	Kind      string
	SizeBytes int64
	ByteLimit int64
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (attachment Attachment) validate() error {
	if strings.TrimSpace(attachment.ID) == "" || strings.ContainsAny(attachment.ID, `/\\`) {
		return errors.New("attachment ID is required and must not be a path")
	}
	if strings.TrimSpace(attachment.ProfileID) == "" || strings.TrimSpace(attachment.RunID) == "" || strings.TrimSpace(attachment.GrantID) == "" {
		return errors.New("attachment profile ID, run ID, and grant ID are required")
	}
	if strings.TrimSpace(attachment.Kind) == "" || attachment.SizeBytes < 0 || attachment.ByteLimit <= 0 || attachment.ExpiresAt.IsZero() {
		return errors.New("attachment kind, non-negative size, positive byte limit, and expiry are required")
	}
	return nil
}
