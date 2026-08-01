package message

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
	CustomMethod              = "message.send_custom"
	CustomScope               = "message.send_custom"
	maxCustomDataBytes        = 4096
	maxCustomExtensionBytes   = 1024
	maxCustomDescriptionBytes = 512
)

// CustomInput describes an opaque custom payload. It is used only to send
// the message and is not persisted by this handler or the operation store.
type CustomInput struct {
	Data        string `json:"data"`
	Extension   string `json:"extension,omitempty"`
	Description string `json:"description,omitempty"`
	RecipientID string `json:"recipient_id,omitempty"`
	GroupID     string `json:"group_id,omitempty"`
}

type CustomSender interface {
	SendCustom(context.Context, string, string, string, string, string) error
}

type CustomHandler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	sender   CustomSender
}

func NewCustom(manifest *capability.Manifest, guard *operation.Guard, sender CustomSender) (*CustomHandler, error) {
	if manifest == nil || guard == nil || sender == nil {
		return nil, errors.New("manifest, operation guard, and custom sender are required")
	}
	return &CustomHandler{manifest: manifest, guard: guard, sender: sender}, nil
}

func (h *CustomHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:    CustomMethod,
		Scope:   CustomScope,
		Allowed: func() bool { return h.manifest.Allows(CustomMethod, CustomScope) },
		Targets: customTargets,
		Handle:  h.handle,
	}
}

func customTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseCustom(raw)
	if err != nil {
		return nil, err
	}
	if input.RecipientID != "" {
		return []string{grant.UserTarget(input.RecipientID)}, nil
	}
	return []string{grant.GroupTarget(input.GroupID)}, nil
}

func (h *CustomHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseCustom(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.send_custom input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.send_custom requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "message-custom-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          CustomScope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		return h.sender.SendCustom(ctx, input.Data, input.Extension, input.Description, input.RecipientID, input.GroupID)
	})
	if err != nil {
		return nil, messageActionFailure(err, "message.send_custom")
	}
	return messageActionResult(outcome.Operation.ID, string(outcome.Operation.Status))
}

func parseCustom(raw json.RawMessage) (CustomInput, error) {
	var input CustomInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return CustomInput{}, err
	}
	input.Extension = strings.TrimSpace(input.Extension)
	input.Description = strings.TrimSpace(input.Description)
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	input.GroupID = strings.TrimSpace(input.GroupID)
	if len(input.Data) == 0 || len(input.Data) > maxCustomDataBytes || len(input.Extension) > maxCustomExtensionBytes || len(input.Description) > maxCustomDescriptionBytes {
		return CustomInput{}, errors.New("custom message fields are invalid")
	}
	if (input.RecipientID == "" && input.GroupID == "") || (input.RecipientID != "" && input.GroupID != "") {
		return CustomInput{}, errors.New("exactly one message recipient is required")
	}
	return input, nil
}
