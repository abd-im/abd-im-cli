package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestServerListsOnlyVisibleToolsAndInjectsGrant(t *testing.T) {
	proxy := &recordingProxy{}
	visible := true
	server := newServer(t, proxy, &visible)
	output, err := serve(server, strings.Join([]string{
		encodeRequest("discover", "server/discover", metaParams("")),
		encodeRequest("list", "tools/list", metaParams("")),
		encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{"grant":"client-grant","method":"daemon.shutdown"}`)),
	}, "\n"))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3; output = %s", len(responses), output)
	}
	var discovery struct {
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(responses[0].Result, &discovery); err != nil || len(discovery.SupportedVersions) != 1 || discovery.SupportedVersions[0] != "2026-07-28" {
		t.Fatalf("discovery = %+v, %v", discovery, err)
	}
	if _, ok := discovery.Capabilities["tools"]; !ok {
		t.Fatalf("discovery has no tools capability: %+v", discovery)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[1].Result, &listed); err != nil {
		t.Fatalf("decode tools/list = %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "abdim.profile.get" {
		t.Fatalf("tools/list = %+v", listed.Tools)
	}
	if len(proxy.calls) != 1 || proxy.calls[0].Method != "profile.get" || proxy.calls[0].ProfileID != "work" || proxy.calls[0].Grant != "daemon-grant" || string(proxy.calls[0].Params) != `{"grant":"client-grant","method":"daemon.shutdown"}` {
		t.Fatalf("run proxy calls = %+v", proxy.calls)
	}
	if strings.Contains(output, "daemon-grant") {
		t.Fatalf("MCP output leaked the run grant: %s", output)
	}
	var result struct {
		ResultType        string             `json:"resultType"`
		StructuredContent contracts.Response `json:"structuredContent"`
	}
	if err := json.Unmarshal(responses[2].Result, &result); err != nil || result.ResultType != "complete" || !result.StructuredContent.OK {
		t.Fatalf("tools/call = %+v, %v", result, err)
	}
}

func TestServerSupportsCodexMCPHandshake(t *testing.T) {
	proxy := &recordingProxy{}
	server, err := New("work", "daemon-grant", proxy, DefaultTools([]string{"message.history"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := serve(server, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"codex","version":"test"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n"))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	responses := decodeResponses(t, output)
	if len(responses) != 2 || responses[0].Error != nil || responses[1].Error != nil {
		t.Fatalf("handshake responses = %s", output)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[1].Result, &listed); err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "abdim.message.history" {
		t.Fatalf("tools/list = %s, %v", responses[1].Result, err)
	}
}

func TestServerUsesIdempotencyKeyFromToolArguments(t *testing.T) {
	proxy := &recordingProxy{}
	server, err := New("work", "daemon-grant", proxy, DefaultTools([]string{"group.create"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.group.create","arguments":{"name":"team","member_ids":["member-1"],"idempotency_key":"create-1"}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(proxy.calls) != 1 || proxy.calls[0].Method != "group.create" || proxy.calls[0].IdempotencyKey != "create-1" {
		t.Fatalf("group.create request = %+v", proxy.calls)
	}
}

func TestServerExposesTextSendWithItsIdempotencyKey(t *testing.T) {
	proxy := &recordingProxy{}
	server, err := New("work", "daemon-grant", proxy, DefaultTools([]string{"message.send_text"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.message.send_text","arguments":{"text":"hello","recipient_id":"user-1","idempotency_key":"send-1"}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(proxy.calls) != 1 || proxy.calls[0].Method != "message.send_text" || proxy.calls[0].IdempotencyKey != "send-1" {
		t.Fatalf("message.send_text request = %+v", proxy.calls)
	}
}

func TestDefaultToolsIgnoreMethodsOutsideFixedRegistry(t *testing.T) {
	tools := DefaultTools([]string{"message.history", "daemon.shutdown", "sdk.call"})
	if len(tools) != 1 || tools[0].Name != "abdim.message.history" {
		t.Fatalf("DefaultTools() = %+v", tools)
	}
}

func TestServerRejectsToolsOutsideConstructionSnapshot(t *testing.T) {
	proxy := &recordingProxy{}
	visible := false
	server := newServer(t, proxy, &visible)
	output, err := serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	response := decodeResponses(t, output)[0]
	if response.Error == nil || response.Error.Code != -32602 || len(proxy.calls) != 0 {
		t.Fatalf("denied response = %+v; calls = %+v", response, proxy.calls)
	}
}

func TestServerReturnsRunProxyFailuresAsToolErrors(t *testing.T) {
	proxy := &recordingProxy{err: errors.New("run proxy test marker")}
	visible := true
	server := newServer(t, proxy, &visible)
	output, err := serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	response := decodeResponses(t, output)[0]
	if response.Error != nil || strings.Contains(output, "run proxy test marker") {
		t.Fatalf("run proxy response = %s", output)
	}
	var result struct {
		IsError           bool               `json:"isError"`
		StructuredContent contracts.Response `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil || !result.IsError || result.StructuredContent.Error == nil || result.StructuredContent.Error.Code != contracts.CodeInternal {
		t.Fatalf("tool error = %+v, %v", result, err)
	}
}

func TestServerDoesNotExposeMismatchedRunProxyResponse(t *testing.T) {
	visible := true
	server := newServer(t, crossProfileProxy{}, &visible)
	output, err := serve(server, encodeRequest("call", "tools/call", metaParams(`,"name":"abdim.profile.get","arguments":{}`)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	response := decodeResponses(t, output)[0]
	if response.Error == nil || response.Error.Code != -32603 || strings.Contains(output, "must-not-leak") {
		t.Fatalf("unsafe run proxy response = %s", output)
	}
}

func TestNewValidatesRunPrivateRegistry(t *testing.T) {
	proxy := &recordingProxy{}
	visible := func() bool { return true }
	tool := Tool{Name: "abdim.profile.get", Description: "Read profile.", Method: "profile.get", InputSchema: json.RawMessage(`{"type":"object"}`), Visible: visible}
	if _, err := New("", "daemon-grant", proxy, nil); err == nil {
		t.Fatal("New() accepted empty profile ID")
	}
	if _, err := New("work", "", proxy, nil); err == nil {
		t.Fatal("New() accepted an empty run grant")
	}
	if _, err := New("work", "daemon-grant", nil, nil); err == nil {
		t.Fatal("New() accepted a nil run proxy")
	}
	invalid := tool
	invalid.Visible = nil
	if _, err := New("work", "daemon-grant", proxy, []Tool{invalid}); err == nil {
		t.Fatal("New() accepted a tool without visibility")
	}
	invalid = tool
	invalid.InputSchema = json.RawMessage(`[]`)
	if _, err := New("work", "daemon-grant", proxy, []Tool{invalid}); err == nil {
		t.Fatal("New() accepted a non-object input schema")
	}
}

type recordingProxy struct {
	calls []contracts.Request
	err   error
}

func (p *recordingProxy) Call(_ context.Context, request contracts.Request) (contracts.Response, error) {
	p.calls = append(p.calls, request)
	if p.err != nil {
		return contracts.Response{}, p.err
	}
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  request.RequestID,
		OK:         true,
		Data:       request.Params,
		Meta:       &contracts.Meta{ProfileID: request.ProfileID},
	}, nil
}

func (*recordingProxy) Close(context.Context) error { return nil }

type crossProfileProxy struct{}

func (crossProfileProxy) Call(_ context.Context, request contracts.Request) (contracts.Response, error) {
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  request.RequestID,
		OK:         true,
		Data:       json.RawMessage(`{"secret":"must-not-leak"}`),
		Meta:       &contracts.Meta{ProfileID: "other-profile"},
	}, nil
}

func (crossProfileProxy) Close(context.Context) error { return nil }

func newServer(t *testing.T, proxy contracts.ToolProxy, visible *bool) *Server {
	t.Helper()
	server, err := New("work", "daemon-grant", proxy, []Tool{
		{Name: "abdim.profile.get", Description: "Read profile.", Method: "profile.get", InputSchema: json.RawMessage(`{"type":"object"}`), Visible: func() bool { return *visible }},
		{Name: "abdim.hidden", Description: "Hidden tool.", Method: "daemon.shutdown", InputSchema: json.RawMessage(`{"type":"object"}`), Visible: func() bool { return false }},
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

type decodedResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error"`
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
