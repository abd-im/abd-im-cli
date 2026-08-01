package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/service"
	operationsservice "github.com/abd-im/abd-im-cli/internal/service/operations"
)

// RunOperationOwnerMethods binds operational methods only to an owner
// dispatcher. They are deliberately separate from the provider tool registry.
func RunOperationOwnerMethods(reader *operationsservice.Service) ([]OwnerMethod, error) {
	if reader == nil {
		return nil, errors.New("run operations service is required")
	}
	return []OwnerMethod{
		ownerMethod(operationsservice.RunListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input operationsservice.ListInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			result, err := reader.List(ctx, input)
			if err != nil {
				return OwnerResult{}, runOperationFailure(err)
			}
			return OwnerResult{Data: result.Data, Meta: service.ContractMeta(result.Meta)}, nil
		}),
		ownerMethod(operationsservice.RunCancelMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input operationsservice.CancelInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			result, err := reader.Cancel(ctx, input)
			if err != nil {
				return OwnerResult{}, runOperationFailure(err)
			}
			return OwnerResult{Data: result.Data, Meta: service.ContractMeta(result.Meta)}, nil
		}),
		ownerMethod(operationsservice.OperationGetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input operationsservice.OperationInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			result, err := reader.Operation(ctx, input)
			if err != nil {
				return OwnerResult{}, runOperationFailure(err)
			}
			return OwnerResult{Data: result.Data, Meta: service.ContractMeta(result.Meta)}, nil
		}),
		ownerMethod(operationsservice.OperationMarkUnknownMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input operationsservice.OperationInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			result, err := reader.MarkOperationUnknown(ctx, input)
			if err != nil {
				return OwnerResult{}, runOperationFailure(err)
			}
			return OwnerResult{Data: result.Data, Meta: service.ContractMeta(result.Meta)}, nil
		}),
	}, nil
}

func runOperationFailure(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidArgument), errors.Is(err, service.ErrCursorInvalid), errors.Is(err, control.ErrNotFound), errors.Is(err, operationsservice.ErrRunNotActive):
		return MethodFailure(contracts.CodeInvalidArgument, "invalid run or operation request", false)
	case errors.Is(err, service.ErrCursorExpired):
		return MethodFailure(contracts.CodeCursorExpired, "run cursor has expired", false)
	default:
		return MethodFailure(contracts.CodeInternal, "run operation failed", false)
	}
}
