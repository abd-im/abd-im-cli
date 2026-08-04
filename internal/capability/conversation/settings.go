package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	SetPinnedMethod        = "conversation.set_pinned"
	SetPinnedScope         = "conversation.set_pinned"
	SetReceiveOptionMethod = "conversation.set_receive_option"
	SetReceiveOptionScope  = "conversation.set_receive_option"
)

// ReceiveOption is the complete, fixed set of server-supported conversation
// receive choices. It intentionally does not expose a numeric SDK option.
type ReceiveOption string

const (
	ReceiveOptionReceive         ReceiveOption = "receive"
	ReceiveOptionDoNotReceive    ReceiveOption = "do_not_receive"
	ReceiveOptionReceiveNoNotify ReceiveOption = "receive_no_notify"
)

// SetPinnedInput contains the only two fields accepted by the pinned action.
type SetPinnedInput struct {
	ConversationID string `json:"conversation_id"`
	Pinned         bool   `json:"pinned"`
}

// SetReceiveOptionInput contains the only two fields accepted by the receive
// option action.
type SetReceiveOptionInput struct {
	ConversationID string        `json:"conversation_id"`
	Option         ReceiveOption `json:"option"`
}

// SettingsSender is intentionally narrower than a general conversation API.
// Implementations must use a fixed server action and return
// operation.ErrOutcomeUnknown when its result cannot be determined after
// submission.
type SettingsSender interface {
	SetPinned(context.Context, SetPinnedInput) error
	SetReceiveOption(context.Context, SetReceiveOptionInput) error
}

// SetPinnedHandler exposes one typed, grant-scoped conversation setting.
type SetPinnedHandler struct {
	guard  *operation.Guard
	sender SettingsSender
}

func NewSetPinned(guard *operation.Guard, sender SettingsSender) (*SetPinnedHandler, error) {
	if guard == nil || sender == nil {
		return nil, errors.New("operation guard and conversation settings sender are required")
	}
	return &SetPinnedHandler{guard: guard, sender: sender}, nil
}

func (h *SetPinnedHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:   SetPinnedMethod,
		Handle: h.handle,
	}
}

func (h *SetPinnedHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetPinned(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid conversation.set_pinned input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "conversation.set_pinned requires idempotency_key")
	}
	return executeSetting(ctx, h.guard, request, SetPinnedScope, "conversation-set-pinned-", input, func(ctx context.Context) error {
		return h.sender.SetPinned(ctx, input)
	})
}

// SetReceiveOptionHandler exposes one typed, grant-scoped conversation
// setting. It cannot patch any other conversation property.
type SetReceiveOptionHandler struct {
	guard  *operation.Guard
	sender SettingsSender
}

func NewSetReceiveOption(guard *operation.Guard, sender SettingsSender) (*SetReceiveOptionHandler, error) {
	if guard == nil || sender == nil {
		return nil, errors.New("operation guard and conversation settings sender are required")
	}
	return &SetReceiveOptionHandler{guard: guard, sender: sender}, nil
}

func (h *SetReceiveOptionHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:   SetReceiveOptionMethod,
		Handle: h.handle,
	}
}

func (h *SetReceiveOptionHandler) handle(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetReceiveOption(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid conversation.set_receive_option input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "conversation.set_receive_option requires idempotency_key")
	}
	return executeSetting(ctx, h.guard, request, SetReceiveOptionScope, "conversation-set-receive-option-", input, func(ctx context.Context) error {
		return h.sender.SetReceiveOption(ctx, input)
	})
}

func executeSetting(ctx context.Context, guard *operation.Guard, request contracts.Request, scope, operationPrefix string, input any, action func(context.Context) error) (json.RawMessage, error) {
	outcome, err := guard.Execute(ctx, operation.Request{
		ID:             operationPrefix + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, action)
	if err != nil {
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior conversation setting outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "conversation setting failed")
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func parseSetPinned(raw json.RawMessage) (SetPinnedInput, error) {
	var decoded struct {
		ConversationID string `json:"conversation_id"`
		Pinned         *bool  `json:"pinned"`
	}
	if err := decodeSettingsInput(raw, &decoded); err != nil {
		return SetPinnedInput{}, err
	}
	if decoded.Pinned == nil {
		return SetPinnedInput{}, errors.New("pinned setting is required")
	}
	input := SetPinnedInput{ConversationID: strings.TrimSpace(decoded.ConversationID), Pinned: *decoded.Pinned}
	if !validIdentifier(input.ConversationID) {
		return SetPinnedInput{}, errors.New("conversation ID must contain 1-256 bytes")
	}
	return input, nil
}

func parseSetReceiveOption(raw json.RawMessage) (SetReceiveOptionInput, error) {
	var input SetReceiveOptionInput
	if err := decodeSettingsInput(raw, &input); err != nil {
		return SetReceiveOptionInput{}, err
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Option = ReceiveOption(strings.TrimSpace(string(input.Option)))
	if !validIdentifier(input.ConversationID) {
		return SetReceiveOptionInput{}, errors.New("conversation ID must contain 1-256 bytes")
	}
	switch input.Option {
	case ReceiveOptionReceive, ReceiveOptionDoNotReceive, ReceiveOptionReceiveNoNotify:
		return input, nil
	default:
		return SetReceiveOptionInput{}, errors.New("receive option is not supported")
	}
}

func decodeSettingsInput(raw json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("only one settings input object is allowed")
	}
	return nil
}
