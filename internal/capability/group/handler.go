// Package group implements verified group-domain remote actions.
package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const Method = "group.create"
const Scope = "group.create"

type Input struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}
type Creator interface {
	CreateGroup(context.Context, Input) error
}
type Handler struct {
	guard   *operation.Guard
	creator Creator
}

func New(guard *operation.Guard, creator Creator) (*Handler, error) {
	if guard == nil || creator == nil {
		return nil, errors.New("operation guard and group creator are required")
	}
	return &Handler{guard, creator}, nil
}
func (h *Handler) ProxyMethod() proxy.Method {
	return proxy.Method{Name: Method, Handle: h.handle}
}
func (h *Handler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parse(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid group.create input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "group.create requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{ID: "group-" + request.IdempotencyKey, ProfileID: request.ProfileID, Scope: Scope, IdempotencyKey: request.IdempotencyKey, Input: input}, func(ctx context.Context) error { return h.creator.CreateGroup(ctx, input) })
	if err != nil {
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior group.create outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "group.create failed")
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
	if input.Name == "" || len(input.MemberIDs) == 0 || len(input.MemberIDs) > 100 {
		return Input{}, errors.New("group name and 1-100 members are required")
	}
	seen := map[string]struct{}{}
	for _, id := range input.MemberIDs {
		if id == "" {
			return Input{}, errors.New("member ID is required")
		}
		if _, exists := seen[id]; exists {
			return Input{}, fmt.Errorf("duplicate member ID")
		}
		seen[id] = struct{}{}
	}
	return input, nil
}
