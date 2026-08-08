package reply

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
)

const (
	AgentRunStreamType    = "agent_run_v1"
	maxAgentRunBytes      = 128 * 1024
	maxAgentRunPackets    = 4096
	maxAgentActivityBytes = 32 * 1024
	agentAnswerReserve    = 72 * 1024
	agentTerminalReserve  = 1024
	agentAnswerFlushBytes = 4 * 1024
	agentAnswerFlushDelay = 75 * time.Millisecond
)

var errAgentRunLimit = errors.New("agent run exceeds stream message limit")

type AgentRunEnvelope struct {
	Schema int    `json:"schema"`
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

type AgentRunPacket struct {
	Version      int      `json:"version"`
	Kind         string   `json:"kind"`
	RunID        string   `json:"runId,omitempty"`
	CallID       string   `json:"callId,omitempty"`
	RequestID    string   `json:"requestId,omitempty"`
	Name         string   `json:"name,omitempty"`
	Text         string   `json:"text,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Status       string   `json:"status,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Choices      []string `json:"choices,omitempty"`
	DurationMS   int64    `json:"durationMs,omitempty"`
	MediaType    string   `json:"mediaType,omitempty"`
	Size         int64    `json:"size,omitempty"`
	AttachmentID string   `json:"attachmentId,omitempty"`
}

type AgentRun struct {
	sender StreamSender
	slot   control.ReplySlot

	mu              sync.Mutex
	ref             StreamRef
	answer          string
	pendingAnswer   string
	flushTimer      *time.Timer
	asyncErr        error
	answerTruncated bool
	nextIndex       int64
	totalBytes      int
	activityBytes   int
	startedAt       time.Time
	started         bool
	ended           bool
}

func (s *Service) NewAgentRun(ctx context.Context, profileID, eventID string) (*AgentRun, error) {
	if ctx == nil || strings.TrimSpace(profileID) == "" || strings.TrimSpace(eventID) == "" {
		return nil, errors.New("agent run profile and event IDs are required")
	}
	sender, ok := s.sender.(StreamSender)
	if !ok {
		return nil, errors.New("reply sender does not support stream messages")
	}
	slot, err := s.store.ReplySlotByEvent(ctx, profileID, eventID)
	if err != nil {
		return nil, err
	}
	return &AgentRun{sender: sender, slot: slot}, nil
}

func (r *AgentRun) Queued(ctx context.Context) error {
	return r.append(ctx, AgentRunPacket{Version: 1, Kind: "run.queued", RunID: r.slot.RunID}, false)
}

func (r *AgentRun) Started(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startedAt.IsZero() {
		r.startedAt = time.Now()
	}
	return r.appendLocked(ctx, AgentRunPacket{Version: 1, Kind: "run.started", RunID: r.slot.RunID}, false, false)
}

func (r *AgentRun) Activity(ctx context.Context, activity contracts.TurnActivity) error {
	packet, ok := packetFromActivity(activity)
	if !ok {
		return nil
	}
	return r.append(ctx, packet, true)
}

// Answer accepts the complete current final-answer text and appends only its
// new suffix, matching the existing TurnOutput contract.
func (r *AgentRun) Answer(ctx context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.asyncErr != nil {
		return r.asyncErr
	}
	current := r.answer + r.pendingAnswer
	if !strings.HasPrefix(text, current) {
		return ErrNonMonotonicStream
	}
	delta := strings.TrimPrefix(text, current)
	if delta == "" {
		return nil
	}
	if r.answerTruncated {
		r.answer = text
		return nil
	}
	r.pendingAnswer += delta
	if len(r.pendingAnswer) >= agentAnswerFlushBytes {
		return r.flushAnswerLocked(ctx)
	}
	if r.flushTimer == nil {
		r.flushTimer = time.AfterFunc(agentAnswerFlushDelay, r.flushAnswer)
	}
	return nil
}

func (r *AgentRun) Complete(ctx context.Context) error {
	return r.terminal(ctx, AgentRunPacket{Version: 1, Kind: "run.completed"})
}

func (r *AgentRun) Fail(ctx context.Context, summary string) error {
	return r.terminal(ctx, AgentRunPacket{Version: 1, Kind: "run.failed", Summary: summary})
}

func (r *AgentRun) Cancel(ctx context.Context) error {
	return r.terminal(ctx, AgentRunPacket{Version: 1, Kind: "run.cancelled"})
}

func (r *AgentRun) terminal(ctx context.Context, packet AgentRunPacket) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return nil
	}
	if r.flushTimer != nil {
		r.flushTimer.Stop()
		r.flushTimer = nil
	}
	if !r.startedAt.IsZero() {
		packet.DurationMS = time.Since(r.startedAt).Milliseconds()
	}
	if err := r.flushAnswerLocked(ctx); err != nil {
		return err
	}
	if err := r.appendLocked(ctx, packet, false, true); err != nil {
		return err
	}
	r.ended = true
	return nil
}

func (r *AgentRun) append(ctx context.Context, packet AgentRunPacket, activity bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendLocked(ctx, packet, activity, false)
}

func (r *AgentRun) appendLocked(ctx context.Context, packet AgentRunPacket, activity, end bool) error {
	if ctx == nil {
		return errors.New("agent run context is required")
	}
	if r.ended {
		return errors.New("agent run is already ended")
	}
	if r.asyncErr != nil {
		return r.asyncErr
	}
	value, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	if len(value) > maxStreamPacketBytes {
		if activity {
			return nil
		}
		return errors.New("agent run packet exceeds stream packet limit")
	}
	envelopeBytes := 0
	if !r.started {
		envelope, marshalErr := json.Marshal(AgentRunEnvelope{Schema: 1, RunID: r.slot.RunID, Status: "running"})
		if marshalErr != nil {
			return marshalErr
		}
		envelopeBytes = len(envelope)
	}
	packetLimit := int64(maxAgentRunPackets)
	byteLimit := maxAgentRunBytes
	if !end {
		packetLimit--
		byteLimit -= agentTerminalReserve
	}
	if activity {
		byteLimit -= agentAnswerReserve
	}
	if r.nextIndex >= packetLimit || r.totalBytes+envelopeBytes+len(value) > byteLimit {
		if activity {
			return nil
		}
		return errAgentRunLimit
	}
	if activity && r.activityBytes+len(value) > maxAgentActivityBytes {
		return nil
	}
	if !r.started {
		envelope, marshalErr := json.Marshal(AgentRunEnvelope{Schema: 1, RunID: r.slot.RunID, Status: "running"})
		if marshalErr != nil {
			return marshalErr
		}
		ref, startErr := r.sender.StartStream(ctx, StreamDelivery{
			ProfileID: r.slot.ProfileID, EventID: r.slot.EventID,
			RecipientID: r.slot.RecipientID, GroupID: r.slot.GroupID,
			ConversationID: r.slot.ConversationID, TriggerMessageID: r.slot.TriggerMessageID,
			ClientMsgID: r.slot.OperationID, Type: AgentRunStreamType, Content: string(envelope),
		})
		if startErr != nil {
			return startErr
		}
		if ref.ConversationID != r.slot.ConversationID || ref.ClientMsgID != r.slot.OperationID {
			return errors.New("stream sender returned an unexpected message identity")
		}
		r.ref = ref
		r.started = true
		r.totalBytes = len(envelope)
	}
	if err := r.sender.AppendStream(ctx, StreamAppend{
		ProfileID: r.slot.ProfileID, EventID: r.slot.EventID,
		ConversationID: r.ref.ConversationID, ClientMsgID: r.ref.ClientMsgID,
		StartIndex: r.nextIndex, Packets: []string{string(value)}, End: end,
	}); err != nil {
		return err
	}
	r.nextIndex++
	r.totalBytes += len(value)
	if activity {
		r.activityBytes += len(value)
	}
	return nil
}

func (r *AgentRun) flushAnswer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushTimer = nil
	if r.ended || r.asyncErr != nil {
		return
	}
	if err := r.flushAnswerLocked(context.Background()); err != nil {
		r.asyncErr = err
	}
}

func (r *AgentRun) flushAnswerLocked(ctx context.Context) error {
	if r.asyncErr != nil {
		return r.asyncErr
	}
	if r.pendingAnswer == "" {
		return nil
	}
	for _, delta := range splitAgentAnswer(r.pendingAnswer) {
		if err := r.appendLocked(ctx, AgentRunPacket{Version: 1, Kind: "answer.delta", Text: delta}, false, false); err != nil {
			if errors.Is(err, errAgentRunLimit) {
				r.answer += r.pendingAnswer
				r.pendingAnswer = ""
				r.answerTruncated = true
				return nil
			}
			return err
		}
	}
	r.answer += r.pendingAnswer
	r.pendingAnswer = ""
	return nil
}

func splitAgentAnswer(value string) []string {
	result := make([]string, 0, len(value)/maxStreamPacketBytes+1)
	for value != "" {
		end := len(value)
		for end > 0 {
			packet, _ := json.Marshal(AgentRunPacket{Version: 1, Kind: "answer.delta", Text: value[:end]})
			if len(packet) <= maxStreamPacketBytes {
				break
			}
			end /= 2
			for end > 0 && !utf8.RuneStart(value[end]) {
				end--
			}
		}
		if end == 0 {
			end = len(value)
			_, size := utf8.DecodeRuneInString(value)
			if size > 0 && size < end {
				end = size
			}
		}
		result = append(result, value[:end])
		value = value[end:]
	}
	return result
}

func packetFromActivity(activity contracts.TurnActivity) (AgentRunPacket, bool) {
	packet := AgentRunPacket{
		Version: 1, Kind: activity.Kind, CallID: activity.CallID, RequestID: activity.RequestID,
		Name: activity.Name, Summary: activity.Summary, Status: activity.Status,
		Decision: activity.Decision, Choices: append([]string(nil), activity.Choices...),
		DurationMS: activity.DurationMS, MediaType: activity.MediaType, Size: activity.Size,
		AttachmentID: activity.AttachmentID,
	}
	if activity.Kind == "artifact" {
		packet.Name = activity.ArtifactName
	} else if activity.Kind == "activity.summary" {
		packet.Text = activity.Summary
		packet.Summary = ""
	}
	switch activity.Kind {
	case "activity.summary", "tool.started", "tool.completed", "approval.requested", "approval.resolved", "artifact":
		return packet, true
	default:
		return AgentRunPacket{}, false
	}
}
