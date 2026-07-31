package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeParsesRequestsAndSuppressesNotifications(t *testing.T) {
	var received []Request
	var output bytes.Buffer
	err := Serve(context.Background(), strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`,
	}, "\n")), &output, func(_ context.Context, request Request) (any, bool) {
		received = append(received, request)
		return Success(request.ID, map[string]any{"resultType": "complete"}), true
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(received) != 1 || received[0].Method != "tools/list" {
		t.Fatalf("received = %+v", received)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.JSONRPC != "2.0" || string(response.ID) != "1" {
		t.Fatalf("response = %s, %v", output.String(), err)
	}
}

func TestValidateMetaRejectsMissingAndUnsupportedValues(t *testing.T) {
	if err := ValidateMeta(json.RawMessage(`{}`)); err == nil {
		t.Fatal("ValidateMeta() accepted missing metadata")
	}
	err := ValidateMeta(json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}}}`))
	item, ok := err.(RPCError)
	if !ok || item.Code != -32022 {
		t.Fatalf("ValidateMeta() error = %#v", err)
	}
}
