package business

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-cli/internal/reply"
)

const (
	SendMessageMethod = "business.send_message"
	sendMessageScope  = "business.send_message"
)

type SendMessageInput struct {
	BusinessConnectionID string `json:"business_connection_id"`
	ConversationID       string `json:"conversation_id"`
	TriggerMessageID     string `json:"trigger_message_id"`
	Text                 string `json:"text"`
}

type Sender interface {
	SendBusinessText(context.Context, string, string, string, string, string) error
}

type Handler struct {
	guard  *operation.Guard
	sender Sender
}

func New(guard *operation.Guard, sender Sender) (*Handler, error) {
	if guard == nil || sender == nil {
		return nil, errors.New("operation guard and Business sender are required")
	}
	return &Handler{guard: guard, sender: sender}, nil
}

func (h *Handler) ProxyMethod() proxy.Method {
	return proxy.Method{Name: SendMessageMethod, Handle: h.handle}
}

func (h *Handler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	var input SendMessageInput
	if json.Unmarshal(request.Params, &input) != nil || strings.TrimSpace(input.BusinessConnectionID) == "" ||
		strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.TriggerMessageID) == "" ||
		strings.TrimSpace(input.Text) == "" || len(input.Text) > 4096 {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid business.send_message input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "business.send_message requires idempotency_key")
	}
	operationID := "business-send-" + request.IdempotencyKey
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID: operationID, ProfileID: request.ProfileID, Scope: sendMessageScope,
		IdempotencyKey: request.IdempotencyKey, Input: input,
	}, func(ctx context.Context) error {
		err := h.sender.SendBusinessText(ctx, input.BusinessConnectionID, input.ConversationID, input.TriggerMessageID, input.Text, operationID)
		if errors.Is(err, reply.ErrOutcomeUnknown) {
			return operation.ErrOutcomeUnknown
		}
		return err
	})
	if err != nil {
		switch {
		case errors.Is(err, operation.ErrIdempotencyConflict):
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		case errors.Is(err, operation.ErrOutcomeUnknown):
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior business.send_message outcome is unknown")
		default:
			return nil, proxy.Failure(contracts.CodeSDKError, "business.send_message failed")
		}
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}
