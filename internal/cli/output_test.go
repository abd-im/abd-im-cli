package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestJSONAndJSONLOutputUseSharedEnvelope(t *testing.T) {
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: "req-1", OK: true, Data: json.RawMessage(`{"items":[]}`), Meta: &contracts.Meta{ProfileID: "work"}}
	for _, output := range []Output{OutputJSON, OutputJSONL} {
		var buffer bytes.Buffer
		if err := WriteResponse(&buffer, output, response); err != nil {
			t.Fatalf("WriteResponse(%q) error = %v", output, err)
		}
		var got contracts.Response
		if err := json.Unmarshal(buffer.Bytes(), &got); err != nil {
			t.Fatalf("WriteResponse(%q) wrote invalid JSON: %v", output, err)
		}
		if got.RequestID != "req-1" || !got.OK {
			t.Fatalf("WriteResponse(%q) = %+v", output, got)
		}
	}
	if got := ExitCode(ErrorResponse("req-1", contracts.CodeDaemonNotReady, nil)); got != 3 {
		t.Fatalf("ExitCode(DAEMON_NOT_READY) = %d, want 3", got)
	}
}
