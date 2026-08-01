package message

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	LocationMethod              = "message.send_location"
	LocationScope               = "message.send_location"
	maxLocationDescriptionBytes = 512
)

// LocationInput describes one location message addressed to exactly one
// explicit recipient or group.
type LocationInput struct {
	Description string  `json:"description,omitempty"`
	Longitude   float64 `json:"longitude"`
	Latitude    float64 `json:"latitude"`
	RecipientID string  `json:"recipient_id,omitempty"`
	GroupID     string  `json:"group_id,omitempty"`
}

// LocationSender creates and submits a typed SDK location message.
type LocationSender interface {
	SendLocation(context.Context, string, float64, float64, string, string) error
}

// LocationHandler exposes the grant-scoped message.send_location action.
type LocationHandler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	sender   LocationSender
}

func NewLocation(manifest *capability.Manifest, guard *operation.Guard, sender LocationSender) (*LocationHandler, error) {
	if manifest == nil || guard == nil || sender == nil {
		return nil, errors.New("manifest, operation guard, and location sender are required")
	}
	return &LocationHandler{manifest: manifest, guard: guard, sender: sender}, nil
}

func (h *LocationHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:    LocationMethod,
		Scope:   LocationScope,
		Allowed: func() bool { return h.manifest.Allows(LocationMethod, LocationScope) },
		Targets: locationTargets,
		Handle:  h.handle,
	}
}

func locationTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseLocation(raw)
	if err != nil {
		return nil, err
	}
	if input.RecipientID != "" {
		return []string{grant.UserTarget(input.RecipientID)}, nil
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func (h *LocationHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseLocation(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.send_location input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.send_location requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "message-location-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          LocationScope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		return h.sender.SendLocation(ctx, input.Description, input.Longitude, input.Latitude, input.RecipientID, input.GroupID)
	})
	if err != nil {
		return nil, messageActionFailure(err, "message.send_location")
	}
	return messageActionResult(outcome.Operation.ID, string(outcome.Operation.Status))
}

func parseLocation(raw json.RawMessage) (LocationInput, error) {
	var input LocationInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return LocationInput{}, err
	}
	input.Description = strings.TrimSpace(input.Description)
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	input.GroupID = strings.TrimSpace(input.GroupID)
	if len(input.Description) > maxLocationDescriptionBytes || math.IsNaN(input.Longitude) || math.IsInf(input.Longitude, 0) || math.IsNaN(input.Latitude) || math.IsInf(input.Latitude, 0) || input.Longitude < -180 || input.Longitude > 180 || input.Latitude < -90 || input.Latitude > 90 {
		return LocationInput{}, errors.New("location fields are invalid")
	}
	if (input.RecipientID == "" && input.GroupID == "") || (input.RecipientID != "" && input.GroupID != "") {
		return LocationInput{}, errors.New("exactly one message recipient is required")
	}
	return input, nil
}
