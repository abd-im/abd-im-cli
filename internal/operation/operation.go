// Package operation durably guards remote side effects and their outcomes.
package operation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/control"
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
	operation.TargetSummary = targetSummary(request.Input)
	if err := g.store.PutOperation(ctx, operation); err != nil {
		return Outcome{}, err
	}
	if err := effect(ctx); err != nil {
		if errors.Is(err, ErrOutcomeUnknown) {
			return Outcome{Operation: operation, Executed: true}, ErrOutcomeUnknown
		}
		if updateErr := g.store.UpdateOperationFailure(ctx, operation.ID, failureSummary(err)); updateErr != nil {
			return Outcome{Operation: operation, Executed: true}, updateErr
		}
		operation.Status = control.OperationFailed
		operation.ErrorSummary = failureSummary(err)
		return Outcome{Operation: operation, Executed: true}, err
	}
	if err := g.store.UpdateOperationStatus(ctx, operation.ID, control.OperationConfirmed); err != nil {
		return Outcome{Operation: operation, Executed: true}, ErrOutcomeUnknown
	}
	operation.Status = control.OperationConfirmed
	return Outcome{Operation: operation, Executed: true}, nil
}

// targetSummary keeps the operation diagnostic useful without retaining a
// request body. It recognizes only typed target identifier fields and limits
// the number of stored references.
func targetSummary(input any) string {
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	targets := make(map[string]struct{})
	collectTargets(value, targets)
	if len(targets) == 0 {
		return ""
	}
	values := make([]string, 0, len(targets))
	for target := range targets {
		values = append(values, target)
	}
	sort.Strings(values)
	if len(values) > 10 {
		values = values[:10]
	}
	summary := make([]string, 0, len(values))
	size := 0
	for _, value := range values {
		extra := len(value)
		if len(summary) != 0 {
			extra++
		}
		if size+extra > 256 {
			break
		}
		summary = append(summary, value)
		size += extra
	}
	return strings.Join(summary, ",")
}

func collectTargets(value any, targets map[string]struct{}) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			kind, known := targetKind(key)
			if known {
				collectTargetValues(kind, child, targets)
				continue
			}
			collectTargets(child, targets)
		}
	case []any:
		for _, child := range item {
			collectTargets(child, targets)
		}
	}
}

func targetKind(field string) (string, bool) {
	switch field {
	case "conversation_id":
		return "conversation", true
	case "group_id":
		return "group", true
	case "recipient_id":
		return "recipient", true
	case "user_id", "new_owner_user_id":
		return "user", true
	case "message_id", "source_message_id", "up_to_message_id":
		return "message", true
	case "member_ids", "user_ids", "mention_user_ids", "mentioned_user_ids":
		return "user", true
	default:
		return "", false
	}
}

func collectTargetValues(kind string, value any, targets map[string]struct{}) {
	switch item := value.(type) {
	case string:
		if id := strings.TrimSpace(item); id != "" && len(id) <= 256 {
			targets[kind+":"+id] = struct{}{}
		}
	case []any:
		for _, child := range item {
			collectTargetValues(kind, child, targets)
		}
	}
}

func failureSummary(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "action cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "action deadline exceeded"
	default:
		return "remote action failed"
	}
}

func digest(input any) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode operation input: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
