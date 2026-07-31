// Package reply binds provider output to the event that authorized it.
package reply

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/control"
)

var (
	ErrOutcomeUnknown      = errors.New("reply outcome is unknown")
	ErrIdempotencyConflict = errors.New("reply idempotency conflict")
)

const replyScope = "event.reply"

// Binding is copied from the triggering event before a provider turn begins.
// No provider input can supply or replace its conversation target.
type Binding struct {
	ProfileID        string
	EventID          string
	ConversationID   string
	TriggerMessageID string
	RecipientID      string
	GroupID          string
	RunID            string
}

// Delivery is constructed exclusively from a persisted reply slot.
type Delivery struct {
	ProfileID        string
	EventID          string
	ConversationID   string
	TriggerMessageID string
	RecipientID      string
	GroupID          string
	OperationID      string
	Text             string
}

// Sender is the narrow SDK-facing reply capability. It has no generic send or
// conversation override method.
type Sender interface {
	Reply(context.Context, Delivery) error
}

// Outcome is the durable operation state of one event-bound reply.
type Outcome struct {
	Slot      control.ReplySlot
	Operation control.Operation
}

// Service persists reply slots and reply operations around the actual sender.
type Service struct {
	store  *control.Store
	sender Sender
}

func New(store *control.Store, sender Sender) (*Service, error) {
	if store == nil || sender == nil {
		return nil, errors.New("control store and reply sender are required")
	}
	return &Service{store: store, sender: sender}, nil
}

// Reserve creates the sole reply slot for an event before provider execution.
func (s *Service) Reserve(ctx context.Context, binding Binding) (control.ReplySlot, error) {
	if err := validateBinding(binding); err != nil {
		return control.ReplySlot{}, err
	}
	if existing, err := s.store.ReplySlotByEvent(ctx, binding.ProfileID, binding.EventID); err == nil {
		return existing, nil
	} else if !errors.Is(err, control.ErrNotFound) {
		return control.ReplySlot{}, err
	}
	slot := control.ReplySlot{
		ID:               newID(),
		ProfileID:        binding.ProfileID,
		EventID:          binding.EventID,
		ConversationID:   binding.ConversationID,
		TriggerMessageID: binding.TriggerMessageID,
		RecipientID:      binding.RecipientID,
		GroupID:          binding.GroupID,
		RunID:            binding.RunID,
		OperationID:      newID(),
	}
	if err := s.store.PutReplySlot(ctx, slot); err != nil {
		if errors.Is(err, control.ErrConflict) {
			return s.store.ReplySlotByEvent(ctx, binding.ProfileID, binding.EventID)
		}
		return control.ReplySlot{}, err
	}
	return slot, nil
}

// Deliver sends final text only to its persisted event-bound target. A stored
// unknown/confirmed/failed operation is returned as-is and is never resent.
func (s *Service) Deliver(ctx context.Context, profileID, eventID, finalText string) (Outcome, error) {
	if strings.TrimSpace(profileID) == "" || strings.TrimSpace(eventID) == "" {
		return Outcome{}, errors.New("profile ID and event ID are required")
	}
	slot, err := s.store.ReplySlotByEvent(ctx, profileID, eventID)
	if err != nil {
		return Outcome{}, err
	}
	key := profileID + ":" + eventID + ":" + slot.ID
	digest := inputDigest(finalText)
	operation, err := s.store.OperationByIdempotencyKey(ctx, profileID, replyScope, key)
	if err == nil {
		if operation.InputDigest != digest {
			return Outcome{Slot: slot, Operation: operation}, ErrIdempotencyConflict
		}
		return Outcome{Slot: slot, Operation: operation}, nil
	}
	if !errors.Is(err, control.ErrNotFound) {
		return Outcome{Slot: slot}, err
	}

	operation = control.Operation{
		ID:             slot.OperationID,
		ProfileID:      profileID,
		Scope:          replyScope,
		IdempotencyKey: key,
		InputDigest:    digest,
		Status:         control.OperationUnknown,
	}
	if err := s.store.PutOperation(ctx, operation); err != nil {
		if errors.Is(err, control.ErrConflict) {
			operation, err = s.store.OperationByIdempotencyKey(ctx, profileID, replyScope, key)
			if err == nil {
				if operation.InputDigest != digest {
					return Outcome{Slot: slot, Operation: operation}, ErrIdempotencyConflict
				}
				return Outcome{Slot: slot, Operation: operation}, nil
			}
		}
		return Outcome{Slot: slot}, err
	}

	delivery := Delivery{
		ProfileID:        slot.ProfileID,
		EventID:          slot.EventID,
		ConversationID:   slot.ConversationID,
		TriggerMessageID: slot.TriggerMessageID,
		RecipientID:      slot.RecipientID,
		GroupID:          slot.GroupID,
		OperationID:      slot.OperationID,
		Text:             finalText,
	}
	if err := s.sender.Reply(ctx, delivery); err != nil {
		if errors.Is(err, ErrOutcomeUnknown) {
			return Outcome{Slot: slot, Operation: operation}, ErrOutcomeUnknown
		}
		if updateErr := s.store.UpdateOperationStatus(ctx, operation.ID, control.OperationFailed); updateErr != nil {
			return Outcome{Slot: slot, Operation: operation}, fmt.Errorf("record reply failure: %w", updateErr)
		}
		operation.Status = control.OperationFailed
		return Outcome{Slot: slot, Operation: operation}, err
	}
	if err := s.store.UpdateOperationStatus(ctx, operation.ID, control.OperationConfirmed); err != nil {
		// The message may have been accepted but no durable confirmation exists.
		return Outcome{Slot: slot, Operation: operation}, ErrOutcomeUnknown
	}
	operation.Status = control.OperationConfirmed
	return Outcome{Slot: slot, Operation: operation}, nil
}

func validateBinding(binding Binding) error {
	if strings.TrimSpace(binding.ProfileID) == "" || strings.TrimSpace(binding.EventID) == "" || strings.TrimSpace(binding.ConversationID) == "" || strings.TrimSpace(binding.TriggerMessageID) == "" || strings.TrimSpace(binding.RunID) == "" {
		return errors.New("reply binding profile, event, conversation, trigger message, and run IDs are required")
	}
	if (strings.TrimSpace(binding.RecipientID) == "" && strings.TrimSpace(binding.GroupID) == "") || (strings.TrimSpace(binding.RecipientID) != "" && strings.TrimSpace(binding.GroupID) != "") {
		return errors.New("reply binding requires exactly one private or group target")
	}
	return nil
}

func inputDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("reply-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
