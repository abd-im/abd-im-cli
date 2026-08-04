// Package message implements verified message-domain remote actions.
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
	Method       = "message.send_text"
	Scope        = "message.send_text"
	maxTextBytes = 4096
)

type Input struct {
	Text        string `json:"text"`
	RecipientID string `json:"recipient_id,omitempty"`
	GroupID     string `json:"group_id,omitempty"`
}

type Sender interface {
	SendText(context.Context, string, string, string) error
}

type Handler struct {
	guard  *operation.Guard
	sender Sender
}

func New(guard *operation.Guard, sender Sender) (*Handler, error) {
	if guard == nil || sender == nil {
		return nil, errors.New("operation guard and message sender are required")
	}
	return &Handler{guard: guard, sender: sender}, nil
}

func (h *Handler) ProxyMethod() proxy.Method {
	return proxy.Method{Name: Method, Handle: h.handle}
}

func (h *Handler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parse(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.send_text input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.send_text requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{ID: "message-send-" + request.IdempotencyKey, ProfileID: request.ProfileID, Scope: Scope, IdempotencyKey: request.IdempotencyKey, Input: input}, func(ctx context.Context) error {
		return h.sender.SendText(ctx, input.Text, input.RecipientID, input.GroupID)
	})
	if err != nil {
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior message.send_text outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "message.send_text failed")
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func parse(raw json.RawMessage) (Input, error) {
	var input Input
	if err := json.Unmarshal(raw, &input); err != nil {
		return Input{}, err
	}
	if strings.TrimSpace(input.Text) == "" || len(input.Text) > maxTextBytes {
		return Input{}, errors.New("message text must contain 1-4096 bytes")
	}
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	input.GroupID = strings.TrimSpace(input.GroupID)
	if (input.RecipientID == "" && input.GroupID == "") || (input.RecipientID != "" && input.GroupID != "") {
		return Input{}, errors.New("exactly one message recipient is required")
	}
	return input, nil
}
