// Package conversation implements verified conversation-domain remote actions.
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	Method             = "conversation.mark_read"
	Scope              = "conversation.mark_read"
	maxIdentifierBytes = 256
)

var errWindowDenied = errors.New("mark read is outside the grant message window")

// Input identifies the server message through which the conversation may be
// marked read. The provider never supplies a sequence number.
type Input struct {
	ConversationID string `json:"conversation_id"`
	UpToMessageID  string `json:"up_to_message_id"`
}

// Boundary is a server-verified message identity and sequence number.
type Boundary struct {
	ConversationID string
	MessageID      string
	ServerSeq      int64
}

// BoundaryResolver resolves one message through a daemon-owned server source.
// It must not use the SDK local database.
type BoundaryResolver interface {
	ResolveBoundary(context.Context, string, string) (Boundary, error)
}

// MarkReadRequest is the minimal fixed input to the server write endpoint.
type MarkReadRequest struct {
	ConversationID string
	HasReadSeq     int64
}

// Sender performs the fixed server-side mark-conversation-as-read request.
// Implementations must return operation.ErrOutcomeUnknown after a submission
// whose final outcome cannot be known.
type Sender interface {
	MarkRead(context.Context, MarkReadRequest) error
}

type Handler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	resolver BoundaryResolver
	sender   Sender
}

func New(manifest *capability.Manifest, guard *operation.Guard, resolver BoundaryResolver, sender Sender) (*Handler, error) {
	if manifest == nil || guard == nil || resolver == nil || sender == nil {
		return nil, errors.New("manifest, operation guard, boundary resolver, and conversation sender are required")
	}
	return &Handler{manifest: manifest, guard: guard, resolver: resolver, sender: sender}, nil
}

func (h *Handler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:    Method,
		Scope:   Scope,
		Allowed: func() bool { return h.manifest.Allows(Method, Scope) },
		Targets: targets,
		Handle:  h.handle,
	}
}

func targets(raw json.RawMessage) ([]string, error) {
	input, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.ConversationTarget(input.ConversationID)}, nil
}

func (h *Handler) handle(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
	input, err := parse(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid conversation.mark_read input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "conversation.mark_read requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "conversation-mark-read-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          Scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		return h.markRead(ctx, item, input)
	})
	if err != nil {
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior conversation.mark_read outcome is unknown")
		}
		if errors.Is(err, errWindowDenied) {
			return nil, proxy.Failure(contracts.CodePolicyDenied, "grant message window does not authorize read boundary")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "conversation.mark_read failed")
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func (h *Handler) markRead(ctx context.Context, item grant.Grant, input Input) error {
	window := item.MessageWindow
	if window.ConversationID != input.ConversationID || window.BeforeMessageID == "" {
		return errWindowDenied
	}
	upTo, err := h.resolve(ctx, input.ConversationID, input.UpToMessageID)
	if err != nil {
		return errWindowDenied
	}
	before, err := h.resolve(ctx, input.ConversationID, window.BeforeMessageID)
	if err != nil || upTo.ServerSeq >= before.ServerSeq {
		return errWindowDenied
	}
	if window.AfterMessageID != "" {
		after, err := h.resolve(ctx, input.ConversationID, window.AfterMessageID)
		if err != nil || upTo.ServerSeq <= after.ServerSeq {
			return errWindowDenied
		}
	}
	return h.sender.MarkRead(ctx, MarkReadRequest{ConversationID: input.ConversationID, HasReadSeq: upTo.ServerSeq})
}

func (h *Handler) resolve(ctx context.Context, conversationID, messageID string) (Boundary, error) {
	boundary, err := h.resolver.ResolveBoundary(ctx, conversationID, messageID)
	if err != nil {
		return Boundary{}, err
	}
	if boundary.ConversationID != conversationID || boundary.MessageID != messageID || boundary.ServerSeq < 1 {
		return Boundary{}, errors.New("server returned an invalid message boundary")
	}
	return boundary, nil
}

func parse(raw json.RawMessage) (Input, error) {
	var input Input
	if err := json.Unmarshal(raw, &input); err != nil {
		return Input{}, err
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.UpToMessageID = strings.TrimSpace(input.UpToMessageID)
	if !validIdentifier(input.ConversationID) || !validIdentifier(input.UpToMessageID) {
		return Input{}, errors.New("conversation and message IDs must contain 1-256 bytes")
	}
	return input, nil
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= maxIdentifierBytes
}
