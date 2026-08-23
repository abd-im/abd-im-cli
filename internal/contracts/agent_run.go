package contracts

import (
	"context"
	"errors"
	"strings"
)

const (
	AgentRunSchema        = "abd.agent_run"
	AgentRunSchemaVersion = 2
)

type AgentRunMetadata struct {
	Schema           string `json:"schema"`
	SchemaVersion    int    `json:"schemaVersion"`
	RunID            string `json:"runId"`
	TriggerMessageID string `json:"triggerMessageId"`
}

type RunEventSink func(context.Context, RunEvent) error

type RunEvent interface {
	runEvent()
}

type EventHeader struct {
	Event string         `json:"event"`
	At    int64          `json:"at"`
	Meta  map[string]any `json:"_meta,omitempty"`
}

type RunLifecycleEvent struct {
	EventHeader
	Reason     string `json:"reason,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
}

func (RunLifecycleEvent) runEvent() {}

type ItemStartedEvent struct {
	EventHeader
	Item RunItem `json:"item"`
}

func (ItemStartedEvent) runEvent() {}

type ItemDeltaEvent struct {
	EventHeader
	ItemID  string       `json:"itemId"`
	Content ContentBlock `json:"content"`
}

func (ItemDeltaEvent) runEvent() {}

type ItemUpdatedEvent struct {
	EventHeader
	ItemID string     `json:"itemId"`
	Update ItemUpdate `json:"update"`
}

func (ItemUpdatedEvent) runEvent() {}

type ItemCompletedEvent struct {
	EventHeader
	ItemID    string `json:"itemId"`
	Outcome   string `json:"outcome"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func (ItemCompletedEvent) runEvent() {}

type UsageUpdatedEvent struct {
	EventHeader
	Usage Usage `json:"usage"`
}

func (UsageUpdatedEvent) runEvent() {}

type PermissionRequestedEvent struct {
	EventHeader
	Request PermissionRequest `json:"request"`
}

func (PermissionRequestedEvent) runEvent() {}

type PermissionResolvedEvent struct {
	EventHeader
	Resolution PermissionResolution `json:"resolution"`
}

func (PermissionResolvedEvent) runEvent() {}

type RunItem interface {
	runItem()
}

type MessageItem struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Phase   string         `json:"phase"`
	Content []ContentBlock `json:"content"`
}

func (MessageItem) runItem() {}

type ReasoningItem struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Content []ContentBlock `json:"content"`
}

func (ReasoningItem) runItem() {}

type ToolLocation struct {
	Path string `json:"path"`
	Line int64  `json:"line,omitempty"`
}

type ToolItem struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Title      string         `json:"title"`
	Category   string         `json:"category"`
	Status     string         `json:"status"`
	Input      any            `json:"input,omitempty"`
	Content    []ContentBlock `json:"content"`
	Locations  []ToolLocation `json:"locations"`
	DurationMS int64          `json:"durationMs,omitempty"`
	ErrorCode  string         `json:"errorCode,omitempty"`
}

func (ToolItem) runItem() {}

type PlanEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type PlanItem struct {
	ID      string      `json:"id"`
	Type    string      `json:"type"`
	Entries []PlanEntry `json:"entries"`
}

func (PlanItem) runItem() {}

type ArtifactItem struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	MediaType    string `json:"mediaType"`
	URI          string `json:"uri,omitempty"`
	AttachmentID string `json:"attachmentId,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

func (ArtifactItem) runItem() {}

type AgentItem struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name,omitempty"`
	Task       string `json:"task,omitempty"`
	ChildRunID string `json:"childRunId,omitempty"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
}

func (AgentItem) runItem() {}

type ContentBlock interface {
	contentBlock()
}

type TextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextBlock) contentBlock() {}

type ImageBlock struct {
	Type         string `json:"type"`
	URI          string `json:"uri,omitempty"`
	AttachmentID string `json:"attachmentId,omitempty"`
	MediaType    string `json:"mediaType"`
	Alt          string `json:"alt,omitempty"`
}

func (ImageBlock) contentBlock() {}

type ResourceBlock struct {
	Type         string `json:"type"`
	URI          string `json:"uri,omitempty"`
	AttachmentID string `json:"attachmentId,omitempty"`
	Name         string `json:"name"`
	MediaType    string `json:"mediaType,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

func (ResourceBlock) contentBlock() {}

type DiffBlock struct {
	Type      string `json:"type"`
	Path      string `json:"path"`
	OldText   string `json:"oldText"`
	NewText   string `json:"newText"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (DiffBlock) contentBlock() {}

type TerminalBlock struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (TerminalBlock) contentBlock() {}

type ItemUpdate interface {
	itemUpdate()
}

type ToolStateUpdate struct {
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Locations  []ToolLocation `json:"locations"`
	DurationMS int64          `json:"durationMs,omitempty"`
	ErrorCode  string         `json:"errorCode,omitempty"`
}

func (ToolStateUpdate) itemUpdate() {}

type PlanEntriesUpdate struct {
	Type    string      `json:"type"`
	Entries []PlanEntry `json:"entries"`
}

func (PlanEntriesUpdate) itemUpdate() {}

type AgentStateUpdate struct {
	Type       string `json:"type"`
	ChildRunID string `json:"childRunId,omitempty"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
}

func (AgentStateUpdate) itemUpdate() {}

type Usage struct {
	InputTokens       int64      `json:"inputTokens,omitempty"`
	CachedInputTokens int64      `json:"cachedInputTokens,omitempty"`
	OutputTokens      int64      `json:"outputTokens,omitempty"`
	TotalTokens       int64      `json:"totalTokens,omitempty"`
	Cost              *UsageCost `json:"cost,omitempty"`
}

type UsageCost struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type PermissionOption struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type PermissionRequest struct {
	ID          string             `json:"id"`
	ItemID      string             `json:"itemId,omitempty"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Options     []PermissionOption `json:"options"`
}

type PermissionResolution struct {
	RequestID string `json:"requestId"`
	Outcome   string `json:"outcome"`
	OptionID  string `json:"optionId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func NewRunLifecycleEvent(event string, at int64) RunLifecycleEvent {
	return RunLifecycleEvent{EventHeader: EventHeader{Event: event, At: at}}
}

func NewItemStartedEvent(at int64, item RunItem) ItemStartedEvent {
	return ItemStartedEvent{EventHeader: EventHeader{Event: "item.started", At: at}, Item: item}
}

func NewItemDeltaEvent(at int64, itemID string, content ContentBlock) ItemDeltaEvent {
	return ItemDeltaEvent{EventHeader: EventHeader{Event: "item.delta", At: at}, ItemID: itemID, Content: content}
}

func NewItemUpdatedEvent(at int64, itemID string, update ItemUpdate) ItemUpdatedEvent {
	return ItemUpdatedEvent{EventHeader: EventHeader{Event: "item.updated", At: at}, ItemID: itemID, Update: update}
}

func NewItemCompletedEvent(at int64, itemID, outcome string) ItemCompletedEvent {
	return ItemCompletedEvent{EventHeader: EventHeader{Event: "item.completed", At: at}, ItemID: itemID, Outcome: outcome}
}

func ValidateRunEvent(event RunEvent) error {
	if event == nil {
		return errors.New("run event is required")
	}
	var header EventHeader
	switch value := event.(type) {
	case RunLifecycleEvent:
		header = value.EventHeader
		if value.Event != "run.queued" && value.Event != "run.started" && value.Event != "run.truncated" && value.Event != "run.finished" {
			return errors.New("invalid run lifecycle event")
		}
		if value.Event == "run.finished" && (value.Outcome == "" || value.Reason == "") {
			return errors.New("run.finished requires outcome and reason")
		}
	case ItemStartedEvent:
		header = value.EventHeader
		if value.Event != "item.started" || validateRunItem(value.Item) != nil {
			return errors.New("invalid item.started event")
		}
	case ItemDeltaEvent:
		header = value.EventHeader
		if value.Event != "item.delta" || strings.TrimSpace(value.ItemID) == "" || validateContentBlock(value.Content) != nil {
			return errors.New("invalid item.delta event")
		}
	case ItemUpdatedEvent:
		header = value.EventHeader
		if value.Event != "item.updated" || strings.TrimSpace(value.ItemID) == "" || value.Update == nil {
			return errors.New("invalid item.updated event")
		}
	case ItemCompletedEvent:
		header = value.EventHeader
		if value.Event != "item.completed" || strings.TrimSpace(value.ItemID) == "" || strings.TrimSpace(value.Outcome) == "" {
			return errors.New("invalid item.completed event")
		}
	case UsageUpdatedEvent:
		header = value.EventHeader
		if value.Event != "usage.updated" {
			return errors.New("invalid usage.updated event")
		}
	case PermissionRequestedEvent:
		header = value.EventHeader
		if value.Event != "permission.requested" || strings.TrimSpace(value.Request.ID) == "" || strings.TrimSpace(value.Request.Title) == "" || len(value.Request.Options) == 0 {
			return errors.New("invalid permission.requested event")
		}
	case PermissionResolvedEvent:
		header = value.EventHeader
		if value.Event != "permission.resolved" || strings.TrimSpace(value.Resolution.RequestID) == "" || strings.TrimSpace(value.Resolution.Outcome) == "" {
			return errors.New("invalid permission.resolved event")
		}
	default:
		return errors.New("unsupported run event")
	}
	if header.At <= 0 {
		return errors.New("run event timestamp is required")
	}
	return nil
}

func validateRunItem(item RunItem) error {
	if item == nil {
		return errors.New("item is required")
	}
	switch value := item.(type) {
	case MessageItem:
		if value.Type != "message" || value.ID == "" || value.Role != "assistant" || (value.Phase != "commentary" && value.Phase != "final") {
			return errors.New("invalid message item")
		}
	case ReasoningItem:
		if value.Type != "reasoning" || value.ID == "" {
			return errors.New("invalid reasoning item")
		}
	case ToolItem:
		if value.Type != "tool" || value.ID == "" || value.Name == "" || value.Title == "" || value.Category == "" || value.Status == "" {
			return errors.New("invalid tool item")
		}
	case PlanItem:
		if value.Type != "plan" || value.ID == "" {
			return errors.New("invalid plan item")
		}
	case ArtifactItem:
		if value.Type != "artifact" || value.ID == "" || value.Name == "" || value.MediaType == "" || (value.URI == "" && value.AttachmentID == "") {
			return errors.New("invalid artifact item")
		}
	case AgentItem:
		if value.Type != "agent" || value.ID == "" || value.Status == "" {
			return errors.New("invalid agent item")
		}
	default:
		return errors.New("unsupported run item")
	}
	return nil
}

func validateContentBlock(block ContentBlock) error {
	if block == nil {
		return errors.New("content block is required")
	}
	switch value := block.(type) {
	case TextBlock:
		if value.Type != "text" || value.Text == "" {
			return errors.New("invalid text block")
		}
	case ImageBlock:
		if value.Type != "image" || value.MediaType == "" || (value.URI == "" && value.AttachmentID == "") {
			return errors.New("invalid image block")
		}
	case ResourceBlock:
		if value.Type != "resource" || value.Name == "" || (value.URI == "" && value.AttachmentID == "") {
			return errors.New("invalid resource block")
		}
	case DiffBlock:
		if value.Type != "diff" || value.Path == "" {
			return errors.New("invalid diff block")
		}
	case TerminalBlock:
		if value.Type != "terminal" || value.Command == "" {
			return errors.New("invalid terminal block")
		}
	default:
		return errors.New("unsupported content block")
	}
	return nil
}
