package contracts

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestV1RequestJSONContract(t *testing.T) {
	request := Request{
		APIVersion:     APIVersionV1,
		RequestID:      "req-1",
		ProfileID:      "work",
		Method:         "conversation.list",
		Params:         json.RawMessage(`{"limit":20}`),
		Grant:          "opaque-grant",
		IdempotencyKey: "operation-1",
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	got, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `{"api_version":"v1","request_id":"req-1","profile_id":"work","method":"conversation.list","params":{"limit":20},"grant":"opaque-grant","idempotency_key":"operation-1"}`
	if string(got) != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}

	request.APIVersion = "v2"
	if err := request.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
	}
}

func TestV1ResponseJSONContracts(t *testing.T) {
	success := Response{
		APIVersion: APIVersionV1,
		RequestID:  "req-1",
		OK:         true,
		Data:       json.RawMessage(`{"items":[]}`),
		Meta:       &Meta{ProfileID: "work", Stale: true},
	}
	if err := success.Validate(); err != nil {
		t.Fatalf("success Validate() error = %v", err)
	}
	got, err := json.Marshal(success)
	if err != nil {
		t.Fatalf("Marshal(success) error = %v", err)
	}
	const successJSON = `{"api_version":"v1","request_id":"req-1","ok":true,"data":{"items":[]},"meta":{"profile_id":"work","stale":true}}`
	if string(got) != successJSON {
		t.Fatalf("Marshal(success) = %s, want %s", got, successJSON)
	}

	failure := Response{
		APIVersion: APIVersionV1,
		RequestID:  "req-1",
		Error: &Error{
			Code:      CodeGrantInvalid,
			Message:   "grant expired",
			Retryable: false,
			Details:   json.RawMessage(`{"grant_id":"grant-1"}`),
		},
	}
	if err := failure.Validate(); err != nil {
		t.Fatalf("failure Validate() error = %v", err)
	}
	got, err = json.Marshal(failure)
	if err != nil {
		t.Fatalf("Marshal(failure) error = %v", err)
	}
	const failureJSON = `{"api_version":"v1","request_id":"req-1","ok":false,"error":{"code":"GRANT_INVALID","message":"grant expired","retryable":false,"details":{"grant_id":"grant-1"}}}`
	if string(got) != failureJSON {
		t.Fatalf("Marshal(failure) = %s, want %s", got, failureJSON)
	}

	failure.Error.Code = "UNSTABLE"
	if err := failure.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("failure Validate() error = %v, want ErrInvalidContract", err)
	}
}

func TestV1EventJSONContract(t *testing.T) {
	event := Event{
		APIVersion: APIVersionV1,
		EventID:    "evt-1",
		ProfileID:  "work",
		Sequence:   7,
		Type:       string(EventStateReconciled),
		OccurredAt: time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC),
		Data:       json.RawMessage(`{"reason":"restart"}`),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	got, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `{"api_version":"v1","event_id":"evt-1","profile_id":"work","sequence":7,"type":"state.reconciled","occurred_at":"2026-07-31T12:00:00Z","data":{"reason":"restart"}}`
	if string(got) != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}
}
