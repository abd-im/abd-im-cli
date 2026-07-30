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
	Status         OperationStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ReplySlot binds a provider result to the original event and conversation.
// It contains only identifiers, never reply text or message content.
type ReplySlot struct {
	ID               string
	ProfileID        string
	EventID          string
	ConversationID   string
	TriggerMessageID string
	RunID            string
	OperationID      string
	CreatedAt        time.Time
}

func (slot ReplySlot) validate() error {
	if strings.TrimSpace(slot.ID) == "" || strings.TrimSpace(slot.ProfileID) == "" || strings.TrimSpace(slot.EventID) == "" {
		return errors.New("reply slot ID, profile ID, and event ID are required")
	}
	if strings.TrimSpace(slot.ConversationID) == "" || strings.TrimSpace(slot.TriggerMessageID) == "" || strings.TrimSpace(slot.RunID) == "" {
		return errors.New("reply slot conversation, trigger message, and run ID are required")
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
	switch operation.Status {
	case OperationConfirmed, OperationFailed, OperationUnknown:
		return nil
	default:
		return errors.New("operation status must be confirmed, failed, or unknown")
	}
}

// MessageWindow identifies the only message range a grant can read. It never
// includes message text.
type MessageWindow struct {
	ConversationID  string `json:"conversation_id"`
	AfterMessageID  string `json:"after_message_id,omitempty"`
	BeforeMessageID string `json:"before_message_id,omitempty"`
}

// Grant defines the authorization issued to one provider run.
type Grant struct {
	ID                  string
	ProfileID           string
	RunID               string
	Principal           string
	Scopes              []string
	TargetAllowlist     []string
	MessageWindow       MessageWindow
	AttachmentByteLimit int64
	RateLimit           int64
	ApprovalPolicy      string
	ExpiresAt           time.Time
	CreatedAt           time.Time
}

func (grant Grant) validate() error {
	if strings.TrimSpace(grant.ID) == "" || strings.TrimSpace(grant.ProfileID) == "" || strings.TrimSpace(grant.RunID) == "" {
		return errors.New("grant ID, profile ID, and run ID are required")
	}
	if strings.TrimSpace(grant.Principal) == "" || len(grant.Scopes) == 0 {
		return errors.New("grant principal and at least one scope are required")
	}
	for _, scope := range grant.Scopes {
		if strings.TrimSpace(scope) == "" {
			return errors.New("grant scopes must not be empty")
		}
	}
	for _, target := range grant.TargetAllowlist {
		if strings.TrimSpace(target) == "" {
			return errors.New("grant targets must not be empty")
		}
	}
	if grant.AttachmentByteLimit < 0 || grant.RateLimit < 0 {
		return errors.New("grant limits must not be negative")
	}
	if strings.TrimSpace(grant.ApprovalPolicy) == "" || grant.ExpiresAt.IsZero() {
		return errors.New("grant approval policy and expiry are required")
	}
	return nil
}
