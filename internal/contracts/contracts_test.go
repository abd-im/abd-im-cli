package contracts

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestV1RequestJSONContract(t *testing.T) {
	request := Request{
		APIVersion: APIVersionV1,
		RequestID:  "req-1",
		ProfileID:  "work",
		As:         "user",
		Method:     "conversation.list",
		Params:     json.RawMessage(`{"limit":20}`),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	got, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `{"api_version":"v1","request_id":"req-1","profile_id":"work","as":"user","method":"conversation.list","params":{"limit":20}}`
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
			Code:      CodeSDKError,
			Message:   "SDK request failed",
			Retryable: false,
			Details:   json.RawMessage(`{"request":"message.send"}`),
		},
	}
	if err := failure.Validate(); err != nil {
		t.Fatalf("failure Validate() error = %v", err)
	}
	got, err = json.Marshal(failure)
	if err != nil {
		t.Fatalf("Marshal(failure) error = %v", err)
	}
	const failureJSON = `{"api_version":"v1","request_id":"req-1","ok":false,"error":{"code":"SDK_ERROR","message":"SDK request failed","retryable":false,"details":{"request":"message.send"}}}`
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

func TestSDKEventValidation(t *testing.T) {
	event := SDKEvent{
		ProfileID: "work",
		Type:      string(EventMessageReceived),
		DedupKey:  "sdk-message-1",
		Data:      json.RawMessage(`{"conversation_id":"conversation-1"}`),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("SDKEvent.Validate() error = %v", err)
	}
	event.DedupKey = ""
	if err := event.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("SDKEvent.Validate() error = %v, want ErrInvalidContract", err)
	}
}
