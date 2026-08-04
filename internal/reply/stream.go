package reply

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/abd-im/abd-im-cli/internal/control"
)

const maxStreamPacketBytes = 16 * 1024

var ErrNonMonotonicStream = errors.New("stream output replaced an already delivered prefix")

type StreamDelivery struct {
	ProfileID        string
	EventID          string
	RecipientID      string
	GroupID          string
	ConversationID   string
	TriggerMessageID string
	ClientMsgID      string
	Type             string
	Content          string
}

type StreamRef struct {
	ConversationID string
	ClientMsgID    string
}

type StreamAppend struct {
	ProfileID      string
	EventID        string
	ConversationID string
	ClientMsgID    string
	StartIndex     int64
	Packets        []string
	End            bool
}

type StreamSender interface {
	StartStream(context.Context, StreamDelivery) (StreamRef, error)
	AppendStream(context.Context, StreamAppend) error
}

type Stream struct {
	sender StreamSender
	slot   control.ReplySlot

	mu        sync.Mutex
	ref       StreamRef
	delivered string
	nextIndex int64
	started   bool
	ended     bool
}

func (s *Service) NewStream(ctx context.Context, profileID, eventID string) (*Stream, error) {
	if ctx == nil || strings.TrimSpace(profileID) == "" || strings.TrimSpace(eventID) == "" {
		return nil, errors.New("stream profile and event IDs are required")
	}
	sender, ok := s.sender.(StreamSender)
	if !ok {
		return nil, errors.New("reply sender does not support stream messages")
	}
	slot, err := s.store.ReplySlotByEvent(ctx, profileID, eventID)
	if err != nil {
		return nil, err
	}
	return &Stream{sender: sender, slot: slot}, nil
}

// Update accepts the complete current Agent text. Only a new suffix can be
// represented by OpenIM's append-only Stream protocol.
func (s *Stream) Update(ctx context.Context, text string) error {
	if ctx == nil {
		return errors.New("stream context is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return errors.New("stream is already ended")
	}
	if !strings.HasPrefix(text, s.delivered) {
		return ErrNonMonotonicStream
	}
	suffix := strings.TrimPrefix(text, s.delivered)
	if suffix == "" {
		return nil
	}
	packets := splitStreamText(suffix)
	if !s.started {
		ref, err := s.sender.StartStream(ctx, StreamDelivery{
			ProfileID: s.slot.ProfileID, EventID: s.slot.EventID,
			RecipientID: s.slot.RecipientID, GroupID: s.slot.GroupID,
			ConversationID: s.slot.ConversationID, TriggerMessageID: s.slot.TriggerMessageID,
			ClientMsgID: s.slot.OperationID,
			Type:        "text", Content: packets[0],
		})
		if err != nil {
			return err
		}
		if ref.ConversationID != s.slot.ConversationID || ref.ClientMsgID != s.slot.OperationID {
			return errors.New("stream sender returned an unexpected message identity")
		}
		s.ref = ref
		s.started = true
		packets = packets[1:]
	}
	for _, packet := range packets {
		if err := s.sender.AppendStream(ctx, StreamAppend{
			ProfileID: s.slot.ProfileID, EventID: s.slot.EventID,
			ConversationID: s.ref.ConversationID, ClientMsgID: s.ref.ClientMsgID,
			StartIndex: s.nextIndex, Packets: []string{packet},
		}); err != nil {
			return err
		}
		s.nextIndex++
	}
	s.delivered = text
	return nil
}

func (s *Stream) Finish(ctx context.Context, finalText string) error {
	if ctx == nil {
		return errors.New("stream context is required")
	}
	if err := s.Update(ctx, finalText); err != nil {
		_ = s.Close(context.WithoutCancel(ctx))
		return err
	}
	return s.Close(ctx)
}

func (s *Stream) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("stream context is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.ended {
		return nil
	}
	if err := s.sender.AppendStream(ctx, StreamAppend{
		ProfileID: s.slot.ProfileID, EventID: s.slot.EventID,
		ConversationID: s.ref.ConversationID, ClientMsgID: s.ref.ClientMsgID,
		StartIndex: s.nextIndex, End: true,
	}); err != nil {
		return err
	}
	s.ended = true
	return nil
}

func splitStreamText(value string) []string {
	packets := make([]string, 0, len(value)/maxStreamPacketBytes+1)
	for len(value) > maxStreamPacketBytes {
		end := maxStreamPacketBytes
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		packets = append(packets, value[:end])
		value = value[end:]
	}
	if value != "" {
		packets = append(packets, value)
	}
	return packets
}
