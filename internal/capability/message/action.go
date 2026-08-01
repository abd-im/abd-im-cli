package message

import (
	"encoding/json"
	"errors"

	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

func messageActionFailure(err error, method string) error {
	if errors.Is(err, operation.ErrIdempotencyConflict) {
		return proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
	}
	if errors.Is(err, operation.ErrOutcomeUnknown) {
		return proxy.Failure(contracts.CodeOutcomeUnknown, "prior "+method+" outcome is unknown")
	}
	return proxy.Failure(contracts.CodeSDKError, method+" failed")
}

func messageActionResult(operationID, status string) (json.RawMessage, error) {
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{operationID, status})
}
