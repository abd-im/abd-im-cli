package message

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	RevokeMethod = "message.revoke"
	RevokeScope  = "message.revoke"
)

// RevokeInput identifies exactly one server message in an approved
// conversation. The source verifies that the profile owner sent it.
type RevokeInput struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

// Revoker resolves server history and invokes only the fixed revoke action.
type Revoker interface {
	Revoke(context.Context, RevokeInput) error
}

type RevokeHandler struct {
	guard   *operation.Guard
	revoker Revoker
}

func NewRevoke(guard *operation.Guard, revoker Revoker) (*RevokeHandler, error) {
	if guard == nil || revoker == nil {
		return nil, errors.New("operation guard and message revoker are required")
	}
	return &RevokeHandler{guard: guard, revoker: revoker}, nil
}

func (h *RevokeHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:   RevokeMethod,
		Handle: h.handle,
	}
}

func (h *RevokeHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseRevoke(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.revoke input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.revoke requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "message-revoke-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          RevokeScope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		return h.revoker.Revoke(ctx, input)
	})
	if err != nil {
		return nil, messageActionFailure(err, "message.revoke")
	}
	return messageActionResult(outcome.Operation.ID, string(outcome.Operation.Status))
}

func parseRevoke(raw json.RawMessage) (RevokeInput, error) {
	var input RevokeInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return RevokeInput{}, err
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	if input.ConversationID == "" || input.MessageID == "" {
		return RevokeInput{}, errors.New("conversation ID and message ID are required")
	}
	return input, nil
}
