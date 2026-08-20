package control

import (
	"errors"
	"strings"
	"time"
)

type Profile struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Event stores only stable inbound references used for deduplication.
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
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.ProfileID) == "" || strings.TrimSpace(event.SDKDedupKey) == "" || strings.TrimSpace(event.Type) == "" {
		return errors.New("event ID, profile ID, dedup key, and type are required")
	}
	return nil
}

type ProviderSession struct {
	ProfileID      string
	ConversationID string
	Provider       string
	SessionRef     string
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
