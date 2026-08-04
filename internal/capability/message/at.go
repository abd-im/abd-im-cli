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
	AtMethod      = "message.send_at"
	AtScope       = "message.send_at"
	maxAtMentions = 10
)

// AtInput describes one group text message that explicitly mentions users.
// The group and every mentioned user are independently grant-authorized.
type AtInput struct {
	Text           string   `json:"text"`
	GroupID        string   `json:"group_id"`
	MentionUserIDs []string `json:"mention_user_ids"`
}

// AtSender delivers a message created with the SDK's typed text-at API.
type AtSender interface {
	SendAt(context.Context, string, string, []string) error
}

// AtHandler exposes the grant-scoped message.send_at action.
type AtHandler struct {
	guard  *operation.Guard
	sender AtSender
}

func NewAt(guard *operation.Guard, sender AtSender) (*AtHandler, error) {
	if guard == nil || sender == nil {
		return nil, errors.New("operation guard and message at sender are required")
	}
	return &AtHandler{guard: guard, sender: sender}, nil
}

func (h *AtHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:   AtMethod,
		Handle: h.handle,
	}
}

func (h *AtHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseAt(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.send_at input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.send_at requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "message-at-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          AtScope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		return h.sender.SendAt(ctx, input.Text, input.GroupID, input.MentionUserIDs)
	})
	if err != nil {
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior message.send_at outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "message.send_at failed")
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func parseAt(raw json.RawMessage) (AtInput, error) {
	var input AtInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return AtInput{}, err
	}
	if strings.TrimSpace(input.Text) == "" || len(input.Text) > maxTextBytes {
		return AtInput{}, errors.New("message text must contain 1-4096 bytes")
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	if input.GroupID == "" {
		return AtInput{}, errors.New("group ID is required")
	}
	if len(input.MentionUserIDs) == 0 || len(input.MentionUserIDs) > maxAtMentions {
		return AtInput{}, errors.New("1-10 mentioned user IDs are required")
	}
	seen := make(map[string]struct{}, len(input.MentionUserIDs))
	for index, userID := range input.MentionUserIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return AtInput{}, errors.New("mentioned user ID is required")
		}
		if _, exists := seen[userID]; exists {
			return AtInput{}, errors.New("duplicate mentioned user ID")
		}
		seen[userID] = struct{}{}
		input.MentionUserIDs[index] = userID
	}
	return input, nil
}
