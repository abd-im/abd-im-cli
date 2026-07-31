package owner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

func TestServerDiscoversListsAndCallsOnlyRegisteredTools(t *testing.T) {
	var calls []contracts.Request
	server := newServer(t, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		calls = append(calls, request)
		return contracts.Response{
			APIVersion: contracts.APIVersionV1,
			RequestID:  request.RequestID,
			OK:         true,
			Data:       request.Params,
			Meta:       &contracts.Meta{ProfileID: request.ProfileID, Schema: "abdim.service/v1"},
		}, nil
	})
	output, err := serve(server, strings.Join([]string{
		encodeRequest("discover-1", "server/discover", metaParams("")),
		encodeRequest("list-1", "tools/list", metaParams("")),
		encodeRequest("call-1", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{"method":"daemon.shutdown"}`)),
	}, "\n"))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3; output = %s", len(responses), output)
	}

	var discovery struct {
		ResultType        string         `json:"resultType"`
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
		Meta              map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(responses[0].Result, &discovery); err != nil {
		t.Fatalf("decode discovery = %v", err)
	}
	if discovery.ResultType != "complete" || len(discovery.SupportedVersions) != 1 || discovery.SupportedVersions[0] != ProtocolVersion {
		t.Fatalf("discovery = %+v", discovery)
	}
	if _, ok := discovery.Capabilities["tools"]; !ok || discovery.Meta["io.modelcontextprotocol/serverInfo"] == nil {
		t.Fatalf("discovery misses MCP metadata: %+v", discovery)
	}

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[1].Result, &listed); err != nil {
		t.Fatalf("decode tool list = %v", err)
	}
	if len(listed.Tools) != 2 || listed.Tools[0].Name != "abdim.conversation.get" || listed.Tools[1].Name != "abdim.profile.get" {
		t.Fatalf("tools/list = %+v", listed.Tools)
	}

	var result struct {
		ResultType        string             `json:"resultType"`
		StructuredContent contracts.Response `json:"structuredContent"`
		Content           []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responses[2].Result, &result); err != nil {
		t.Fatalf("decode tool result = %v", err)
	}
	if result.ResultType != "complete" || !result.StructuredContent.OK || len(result.Content) != 1 {
		t.Fatalf("tools/call result = %+v", result)
	}
	if len(calls) != 1 || calls[0].Method != "profile.get" || calls[0].ProfileID != "work" || string(calls[0].Params) != `{"method":"daemon.shutdown"}` || calls[0].RequestID == "" {
		t.Fatalf("daemon calls = %+v", calls)
	}
}

func TestServerRejectsMissingOrUnsupportedModernMetadata(t *testing.T) {
	server := newServer(t, func(context.Context, contracts.Request) (contracts.Response, error) {
		t.Fatal("handler must not be called")
		return contracts.Response{}, nil
	})
	output, err := serve(server, strings.Join([]string{
		encodeRequest("missing-meta", "server/discover", `{}`),
		encodeRequest("missing-capabilities", "tools/list", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`),
		encodeRequest("old-version", "server/discover", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}}}`),
	}, "\n"))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if len(responses) != 3 || responses[0].Error == nil || responses[0].Error.Code != -32602 || responses[1].Error == nil || responses[1].Error.Code != -32602 {
		t.Fatalf("metadata errors = %+v", responses)
	}
	if responses[2].Error == nil || responses[2].Error.Code != -32022 {
		t.Fatalf("version error = %+v", responses[2])
	}
	var detail struct {
		Supported []string `json:"supported"`
		Requested string   `json:"requested"`
	}
	if err := json.Unmarshal(responses[2].Error.Data, &detail); err != nil || detail.Requested != "2025-11-25" || len(detail.Supported) != 1 || detail.Supported[0] != ProtocolVersion {
		t.Fatalf("unsupported version detail = %s, %v", responses[2].Error.Data, err)
	}
}

func TestServerRejectsUnknownToolsAndDoesNotReplyToNotifications(t *testing.T) {
	calls := 0
	server := newServer(t, func(context.Context, contracts.Request) (contracts.Response, error) {
		calls++
		return contracts.Response{}, nil
	})
	output, err := serve(server, strings.Join([]string{
		encodeRequest("unknown-tool", "tools/call", metaParams(`,"name":"abdim.daemon.shutdown","arguments":{}`)),
		encodeRequest("unknown-method", "daemon.shutdown", metaParams("")),
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`,
	}, "\n"))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if calls != 0 || len(responses) != 2 {
		t.Fatalf("calls = %d, responses = %d; output = %s", calls, len(responses), output)
	}
	if responses[0].Error == nil || responses[0].Error.Code != -32602 || responses[1].Error == nil || responses[1].Error.Code != -32601 {
		t.Fatalf("responses = %+v", responses)
	}
}

func TestServerDistinguishesMalformedJSONFromInvalidRequests(t *testing.T) {
	server := newServer(t, func(context.Context, contracts.Request) (contracts.Response, error) {
		t.Fatal("handler must not be called")
		return contracts.Response{}, nil
	})
	output, err := serve(server, "{\n[]")
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if len(responses) != 2 || responses[0].Error == nil || responses[0].Error.Code != -32700 || responses[1].Error == nil || responses[1].Error.Code != -32600 {
		t.Fatalf("request errors = %+v", responses)
	}
}

func TestServerReturnsDaemonFailuresAsToolErrors(t *testing.T) {
	server := newServer(t, func(context.Context, contracts.Request) (contracts.Response, error) {
		return contracts.Response{}, errors.New("test transport marker")
	})
	output, err := serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	response := decodeResponses(t, output)[0]
	if response.Error != nil {
		t.Fatalf("tools/call protocol error = %+v", response.Error)
	}
	var result struct {
		IsError           bool               `json:"isError"`
		StructuredContent contracts.Response `json:"structuredContent"`
		Content           []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode result = %v", err)
	}
	if !result.IsError || result.StructuredContent.Error == nil || result.StructuredContent.Error.Code != contracts.CodeDaemonUnavailable || strings.Contains(result.Content[0].Text, "test transport marker") {
		t.Fatalf("tool error = %+v", result)
	}
}

func TestServerDoesNotExposeMismatchedDaemonResponse(t *testing.T) {
	server := newServer(t, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		return contracts.Response{
			APIVersion: contracts.APIVersionV1,
			RequestID:  request.RequestID,
			OK:         true,
			Data:       json.RawMessage(`{"secret":"must-not-leak"}`),
			Meta:       &contracts.Meta{ProfileID: "other-profile"},
		}, nil
	})
	output, err := serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	response := decodeResponses(t, output)[0]
	if response.Error == nil || response.Error.Code != -32603 || strings.Contains(output, "must-not-leak") {
		t.Fatalf("unsafe daemon response = %s", output)
	}
}

func TestNewValidatesStaticToolRegistry(t *testing.T) {
	handler := func(context.Context, contracts.Request) (contracts.Response, error) { return contracts.Response{}, nil }
	if _, err := New("", handler, nil); err == nil {
		t.Fatal("New() accepted empty profile ID")
	}
	if _, err := New("work", nil, nil); err == nil {
		t.Fatal("New() accepted nil handler")
	}
	if _, err := New("work", handler, []Tool{{Name: "bad name", Description: "read", Method: "profile.get", InputSchema: json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("New() accepted invalid tool name")
	}
	if _, err := New("work", handler, []Tool{{Name: "abdim.profile.get", Description: "read", Method: "profile.get", InputSchema: json.RawMessage(`[]`)}}); err == nil {
		t.Fatal("New() accepted an array input schema")
	}
	if _, err := New("work", handler, []Tool{
		{Name: "abdim.profile.get", Description: "read", Method: "profile.get", InputSchema: json.RawMessage(`{}`)},
		{Name: "abdim.profile.get", Description: "duplicate", Method: "profile.get", InputSchema: json.RawMessage(`{}`)},
	}); err == nil {
		t.Fatal("New() accepted duplicate tool names")
	}
}

type decodedResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

func newServer(t *testing.T, handler func(context.Context, contracts.Request) (contracts.Response, error)) *Server {
	t.Helper()
	server, err := New("work", handler, []Tool{
		{Name: "abdim.profile.get", Description: "Read the current profile.", Method: "profile.get", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "abdim.conversation.get", Description: "Read one conversation.", Method: "conversation.get", InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string"}},"required":["conversation_id"]}`)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func serve(server *Server, input string) (string, error) {
	var output bytes.Buffer
	err := server.Serve(context.Background(), strings.NewReader(input), &output)
	return output.String(), err
}

func encodeRequest(id, method, params string) string {
	return `{"jsonrpc":"2.0","id":"` + id + `","method":"` + method + `","params":` + params + `}`
}

func metaParams(extra string) string {
	return `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}` + extra + `}`
}

func decodeResponses(t *testing.T, output string) []decodedResponse {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	responses := make([]decodedResponse, 0, len(lines))
	for _, line := range lines {
		var response decodedResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("stdout line is not JSON-RPC: %q: %v", line, err)
		}
		if response.JSONRPC != "2.0" {
			t.Fatalf("JSON-RPC version = %q", response.JSONRPC)
		}
		responses = append(responses, response)
	}
	return responses
}
