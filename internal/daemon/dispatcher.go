package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

// OwnerMethod is one daemon-owned typed service. Methods are registered when
// the daemon is composed; callers cannot select an endpoint or SDK function.
type OwnerMethod struct {
	Name   string
	Handle func(context.Context, json.RawMessage) (OwnerResult, error)
}

// OwnerResult is the in-process result of one typed owner method.
type OwnerResult struct {
	Data any
	Meta contracts.Meta
}

// Dispatcher exposes the daemon's fixed owner typed-method registry over the
// shared local RPC contract.
type Dispatcher struct {
	profileID string
	methods   map[string]OwnerMethod
}

// NewDispatcher registers the owner methods available for one daemon profile.
func NewDispatcher(profileID string, methods []OwnerMethod) (*Dispatcher, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	registered := make(map[string]OwnerMethod, len(methods))
	for _, method := range methods {
		if strings.TrimSpace(method.Name) == "" || method.Handle == nil {
			return nil, errors.New("typed method name and handler are required")
		}
		if _, exists := registered[method.Name]; exists {
			return nil, fmt.Errorf("duplicate typed method %q", method.Name)
		}
		registered[method.Name] = method
	}
	return &Dispatcher{profileID: profileID, methods: registered}, nil
}

// Handle dispatches a validated local request to one registered typed method.
// It returns public contract failures for all invalid or unsafe results so raw
// service errors never reach the local socket or owner MCP adapter.
func (d *Dispatcher) Handle(ctx context.Context, request contracts.Request) (contracts.Response, error) {
	if err := request.Validate(); err != nil {
		return dispatcherFailure(request.RequestID, contracts.CodeInvalidArgument, "invalid owner request", false), nil
	}
	if request.ProfileID != d.profileID {
		return dispatcherFailure(request.RequestID, contracts.CodeInvalidArgument, "profile does not match daemon", false), nil
	}
	method, exists := d.methods[request.Method]
	if !exists {
		return dispatcherFailure(request.RequestID, contracts.CodeInvalidArgument, "method is not an exposed typed service", false), nil
	}
	result, err := method.Handle(ctx, append(json.RawMessage(nil), request.Params...))
	if err != nil {
		var typed *MethodError
		if errors.As(err, &typed) && typed.valid() {
			return dispatcherFailure(request.RequestID, typed.Code, typed.Message, typed.Retryable), nil
		}
		return dispatcherFailure(request.RequestID, contracts.CodeInternal, "typed service failed", false), nil
	}
	if result.Meta.ProfileID != d.profileID {
		return dispatcherFailure(request.RequestID, contracts.CodeInternal, "typed service returned a mismatched profile", false), nil
	}
	payload, err := json.Marshal(result.Data)
	if err != nil {
		return dispatcherFailure(request.RequestID, contracts.CodeInternal, "typed service returned invalid data", false), nil
	}
	response := contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  request.RequestID,
		OK:         true,
		Data:       payload,
		Meta:       &result.Meta,
	}
	if err := response.Validate(); err != nil {
		return dispatcherFailure(request.RequestID, contracts.CodeInternal, "typed service returned an invalid response", false), nil
	}
	return response, nil
}

// MethodError lets a registered typed service deliberately return one stable
// public contract error. Its message must not contain SDK or credential data.
type MethodError struct {
	Code      contracts.ErrorCode
	Message   string
	Retryable bool
}

func (e *MethodError) Error() string { return e.Message }

func (e *MethodError) valid() bool {
	return e != nil && e.Code.Valid() && strings.TrimSpace(e.Message) != ""
}

// MethodFailure constructs a stable public typed-service failure.
func MethodFailure(code contracts.ErrorCode, message string, retryable bool) error {
	return &MethodError{Code: code, Message: message, Retryable: retryable}
}

func dispatcherFailure(requestID string, code contracts.ErrorCode, message string, retryable bool) contracts.Response {
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  responseRequestID(requestID),
		OK:         false,
		Error:      &contracts.Error{Code: code, Message: message, Retryable: retryable},
	}
}

func responseRequestID(requestID string) string {
	if strings.TrimSpace(requestID) == "" {
		return "invalid"
	}
	return requestID
}
