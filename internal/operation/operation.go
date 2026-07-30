// Package operation durably guards remote side effects and their outcomes.
package operation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abd-im-cli/abdim-cli/internal/control"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with prior input")
	ErrOutcomeUnknown      = errors.New("side-effect outcome is unknown")
)

type Request struct {
	ID             string
	ProfileID      string
	Scope          string
	IdempotencyKey string
	Input          any
}

type Outcome struct {
	Operation control.Operation
	Executed  bool
}

type Effect func(context.Context) error

type Guard struct{ store *control.Store }

func NewGuard(store *control.Store) (*Guard, error) {
	if store == nil {
		return nil, errors.New("control store is required")
	}
	return &Guard{store: store}, nil
}

// Execute records unknown before invoking Effect. It never retries a previous
// unknown operation, including under a fresh idempotency key with same input.
func (g *Guard) Execute(ctx context.Context, request Request, effect Effect) (Outcome, error) {
	if request.ID == "" || request.ProfileID == "" || request.Scope == "" || request.IdempotencyKey == "" || effect == nil {
		return Outcome{}, errors.New("operation ID, profile, scope, idempotency key, and effect are required")
	}
	digest, err := digest(request.Input)
	if err != nil {
		return Outcome{}, err
	}
	if existing, err := g.store.OperationByIdempotencyKey(ctx, request.ProfileID, request.Scope, request.IdempotencyKey); err == nil {
		if existing.InputDigest != digest {
			return Outcome{Operation: existing}, ErrIdempotencyConflict
		}
		if existing.Status == control.OperationUnknown {
			return Outcome{Operation: existing}, ErrOutcomeUnknown
		}
		return Outcome{Operation: existing}, nil
	} else if !errors.Is(err, control.ErrNotFound) {
		return Outcome{}, err
	}
	if unknown, err := g.store.UnknownOperationByInputDigest(ctx, request.ProfileID, request.Scope, digest); err == nil {
		return Outcome{Operation: unknown}, ErrOutcomeUnknown
	} else if !errors.Is(err, control.ErrNotFound) {
		return Outcome{}, err
	}
	operation := control.Operation{ID: request.ID, ProfileID: request.ProfileID, Scope: request.Scope, IdempotencyKey: request.IdempotencyKey, InputDigest: digest, Status: control.OperationUnknown}
	if err := g.store.PutOperation(ctx, operation); err != nil {
		return Outcome{}, err
	}
	if err := effect(ctx); err != nil {
		if errors.Is(err, ErrOutcomeUnknown) {
			return Outcome{Operation: operation, Executed: true}, ErrOutcomeUnknown
		}
		if updateErr := g.store.UpdateOperationStatus(ctx, operation.ID, control.OperationFailed); updateErr != nil {
			return Outcome{Operation: operation, Executed: true}, updateErr
		}
		operation.Status = control.OperationFailed
		return Outcome{Operation: operation, Executed: true}, err
	}
	if err := g.store.UpdateOperationStatus(ctx, operation.ID, control.OperationConfirmed); err != nil {
		return Outcome{Operation: operation, Executed: true}, ErrOutcomeUnknown
	}
	operation.Status = control.OperationConfirmed
	return Outcome{Operation: operation, Executed: true}, nil
}

func digest(input any) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode operation input: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
