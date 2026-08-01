// Package blacklist implements verified blacklist-domain remote actions.
package blacklist

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
	AddMethod    = "blacklist.add"
	AddScope     = "blacklist.add"
	RemoveMethod = "blacklist.remove"
	RemoveScope  = "blacklist.remove"

	maxUserIDBytes = 256
)

var errNotBlocked = errors.New("user is not in the blacklist")

// Input identifies exactly one user. The daemon-owned source derives the
// blacklist owner from its authenticated profile context.
type Input struct {
	UserID string `json:"user_id"`
}

// Source is the narrow server action and relationship-read surface required
// for blacklist mutations. It must not call SDK APIs that synchronize local
// state.
type Source interface {
	AddBlacklist(context.Context, string) error
	RemoveBlacklist(context.Context, string) error
	IsBlacklisted(context.Context, string) (bool, error)
}

type Handler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	source   Source
}

func New(manifest *capability.Manifest, guard *operation.Guard, source Source) (*Handler, error) {
	if manifest == nil || guard == nil || source == nil {
		return nil, errors.New("manifest, operation guard, and blacklist source are required")
	}
	return &Handler{manifest: manifest, guard: guard, source: source}, nil
}

// ProxyMethods returns the complete static blacklist action surface.
func (h *Handler) ProxyMethods() []proxy.Method {
	return []proxy.Method{
		h.proxyMethod(AddMethod, AddScope),
		h.proxyMethod(RemoveMethod, RemoveScope),
	}
}

func (h *Handler) proxyMethod(method, scope string) proxy.Method {
	return proxy.Method{
		Name:    method,
		Scope:   scope,
		Allowed: func() bool { return h.manifest.Allows(method, scope) },
		Targets: targets,
		Handle: func(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
			return h.handle(ctx, method, scope, request)
		},
	}
}

func targets(raw json.RawMessage) ([]string, error) {
	input, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return []string{grant.UserTarget(input.UserID)}, nil
}

func (h *Handler) handle(ctx context.Context, method, scope string, request contracts.Request) (json.RawMessage, error) {
	input, err := parse(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid "+method+" input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, method+" requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             strings.ReplaceAll(method, ".", "-") + "-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		if method == RemoveMethod {
			blocked, err := h.source.IsBlacklisted(ctx, input.UserID)
			if err != nil {
				return err
			}
			if !blocked {
				return errNotBlocked
			}
			return h.source.RemoveBlacklist(ctx, input.UserID)
		}
		return h.source.AddBlacklist(ctx, input.UserID)
	})
	if err != nil {
		if errors.Is(err, errNotBlocked) {
			return nil, proxy.Failure(contracts.CodePolicyDenied, "blacklist.remove requires an existing blacklist entry")
		}
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior "+method+" outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, method+" failed")
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
	input.UserID = strings.TrimSpace(input.UserID)
	if input.UserID == "" || len(input.UserID) > maxUserIDBytes {
		return Input{}, errors.New("user ID must contain 1-256 bytes")
	}
	return input, nil
}
