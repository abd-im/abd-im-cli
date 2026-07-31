// Package owner exposes a fixed owner-only MCP tool registry over stdio.
package owner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/ipc"
)

const (
	ProtocolVersion = "2026-07-28"
	maxMessageBytes = 1 << 20
)

// Tool binds one MCP-visible name to one daemon-owned typed method. A caller
// can only select from this registry; it cannot supply a method or endpoint.
type Tool struct {
	Name        string
	Description string
	Method      string
	InputSchema json.RawMessage
}

type registeredTool struct {
	Tool
}

// Server translates owner MCP requests into the daemon's local contract.
type Server struct {
	profileID string
	handler   ipc.Handler
	tools     map[string]registeredTool
	list      []registeredTool
}

// New creates a modern MCP server. Tools must be registered by the local
// composition root before the stdio process accepts any client input.
func New(profileID string, handler ipc.Handler, tools []Tool) (*Server, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	if handler == nil {
		return nil, errors.New("daemon handler is required")
	}

	registered := make(map[string]registeredTool, len(tools))
	list := make([]registeredTool, 0, len(tools))
	for _, tool := range tools {
		if !validToolName(tool.Name) {
			return nil, fmt.Errorf("invalid MCP tool name %q", tool.Name)
		}
		if strings.TrimSpace(tool.Description) == "" || strings.TrimSpace(tool.Method) == "" {
			return nil, errors.New("MCP tool description and daemon method are required")
		}
		if !jsonObject(tool.InputSchema) {
			return nil, fmt.Errorf("MCP tool %q input schema must be a JSON object", tool.Name)
		}
		if _, exists := registered[tool.Name]; exists {
			return nil, fmt.Errorf("duplicate MCP tool %q", tool.Name)
		}
		copy := registeredTool{Tool: Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Method:      tool.Method,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		}}
		registered[copy.Name] = copy
		list = append(list, copy)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return &Server{profileID: profileID, handler: handler, tools: registered, list: list}, nil
}

// Serve reads newline-delimited MCP JSON-RPC messages and writes only JSON-RPC
// responses. It returns when input closes, context is cancelled, or I/O fails.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if input == nil || output == nil {
		return errors.New("MCP input and output are required")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxMessageBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, ok := s.handle(ctx, scanner.Bytes())
		if !ok {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write MCP response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP request: %w", err)
	}
	return nil
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e rpcError) Error() string { return e.Message }

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Error   rpcError        `json:"error"`
}

type successResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

func (s *Server) handle(ctx context.Context, payload []byte) (any, bool) {
	var request request
	if err := json.Unmarshal(payload, &request); err != nil {
		if !json.Valid(payload) {
			return errorResponse{JSONRPC: "2.0", Error: rpcError{Code: -32700, Message: "parse error"}}, true
		}
		return errorResponse{JSONRPC: "2.0", Error: rpcError{Code: -32600, Message: "invalid request"}}, true
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		return errorResponse{JSONRPC: "2.0", Error: rpcError{Code: -32600, Message: "invalid request"}}, true
	}
	if len(request.ID) == 0 {
		return nil, false
	}
	if !validRequestID(request.ID) {
		return errorResponse{JSONRPC: "2.0", Error: rpcError{Code: -32600, Message: "invalid request"}}, true
	}

	if err := validateMeta(request.Params); err != nil {
		return s.error(request.ID, err), true
	}
	switch request.Method {
	case "server/discover":
		return s.success(request.ID, s.discovery()), true
	case "tools/list":
		if err := validateListParams(request.Params); err != nil {
			return s.error(request.ID, err), true
		}
		return s.success(request.ID, s.toolList()), true
	case "tools/call":
		result, err := s.call(ctx, request.Params)
		if err != nil {
			return s.error(request.ID, err), true
		}
		return s.success(request.ID, result), true
	default:
		return s.error(request.ID, rpcError{Code: -32601, Message: "method not found"}), true
	}
}

func (s *Server) discovery() any {
	return struct {
		ResultType        string         `json:"resultType"`
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
		Meta              map[string]any `json:"_meta"`
	}{
		ResultType:        "complete",
		SupportedVersions: []string{ProtocolVersion},
		Capabilities:      map[string]any{"tools": map[string]any{"listChanged": false}},
		Meta:              s.resultMeta(),
	}
}

func (s *Server) toolList() any {
	type listedTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	tools := make([]listedTool, 0, len(s.list))
	for _, tool := range s.list {
		tools = append(tools, listedTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	return struct {
		ResultType string         `json:"resultType"`
		Tools      []listedTool   `json:"tools"`
		Meta       map[string]any `json:"_meta"`
	}{ResultType: "complete", Tools: tools, Meta: s.resultMeta()}
}

func (s *Server) call(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || strings.TrimSpace(params.Name) == "" {
		return nil, rpcError{Code: -32602, Message: "invalid tool parameters"}
	}
	tool, exists := s.tools[params.Name]
	if !exists {
		return nil, rpcError{Code: -32602, Message: "unknown tool"}
	}
	arguments := json.RawMessage(`{}`)
	if len(params.Arguments) != 0 {
		if !jsonObject(params.Arguments) {
			return nil, rpcError{Code: -32602, Message: "tool arguments must be a JSON object"}
		}
		arguments = append(json.RawMessage(nil), params.Arguments...)
	}
	requestID, err := localRequestID()
	if err != nil {
		return nil, rpcError{Code: -32603, Message: "internal error"}
	}
	response, err := s.handler(ctx, contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  requestID,
		ProfileID:  s.profileID,
		Method:     tool.Method,
		Params:     arguments,
	})
	if err != nil {
		response = failedResponse(requestID, contracts.CodeDaemonUnavailable, "daemon request failed", true)
	}
	if err := response.Validate(); err != nil || response.RequestID != requestID || (response.OK && response.Meta.ProfileID != s.profileID) {
		return nil, rpcError{Code: -32603, Message: "internal error"}
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, rpcError{Code: -32603, Message: "internal error"}
	}
	return struct {
		ResultType        string          `json:"resultType"`
		Content           []textContent   `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError,omitempty"`
		Meta              map[string]any  `json:"_meta"`
	}{
		ResultType:        "complete",
		Content:           []textContent{{Type: "text", Text: string(payload)}},
		StructuredContent: payload,
		IsError:           !response.OK,
		Meta:              s.resultMeta(),
	}, nil
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *Server) success(id json.RawMessage, result any) successResponse {
	return successResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *Server) error(id json.RawMessage, err error) errorResponse {
	var rpc rpcError
	if !errors.As(err, &rpc) {
		rpc = rpcError{Code: -32602, Message: "invalid request parameters"}
	}
	return errorResponse{JSONRPC: "2.0", ID: id, Error: rpc}
}

func (s *Server) resultMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/serverInfo": map[string]string{"name": "abdim", "version": contracts.APIVersionV1},
	}
}

func validateMeta(raw json.RawMessage) error {
	var params map[string]json.RawMessage
	if !jsonObject(raw) || json.Unmarshal(raw, &params) != nil {
		return rpcError{Code: -32602, Message: "request metadata is required"}
	}
	metaRaw, exists := params["_meta"]
	if !exists || !jsonObject(metaRaw) {
		return rpcError{Code: -32602, Message: "request metadata is required"}
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return rpcError{Code: -32602, Message: "request metadata is required"}
	}
	var version string
	if rawVersion, exists := meta["io.modelcontextprotocol/protocolVersion"]; !exists || json.Unmarshal(rawVersion, &version) != nil || version == "" {
		return rpcError{Code: -32602, Message: "MCP protocol version is required"}
	}
	if rawCapabilities, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists || !jsonObject(rawCapabilities) {
		return rpcError{Code: -32602, Message: "client capabilities are required"}
	}
	if version != ProtocolVersion {
		return rpcError{Code: -32022, Message: "unsupported protocol version", Data: map[string]any{
			"supported": []string{ProtocolVersion}, "requested": version,
		}}
	}
	return nil
}

func validateListParams(raw json.RawMessage) error {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return rpcError{Code: -32602, Message: "invalid tool list parameters"}
	}
	if params.Cursor != "" {
		return rpcError{Code: -32602, Message: "tool list cursor is not available"}
	}
	return nil
}

func failedResponse(requestID string, code contracts.ErrorCode, message string, retryable bool) contracts.Response {
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  requestID,
		OK:         false,
		Error:      &contracts.Error{Code: code, Message: message, Retryable: retryable},
	}
}

func localRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "mcp-" + hex.EncodeToString(value[:]), nil
}

func validToolName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

func validRequestID(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	if trimmed[0] == '"' {
		var id string
		return json.Unmarshal(value, &id) == nil
	}
	if trimmed[0] == '-' {
		trimmed = trimmed[1:]
	}
	if trimmed == "" || (len(trimmed) > 1 && trimmed[0] == '0') {
		return false
	}
	for _, character := range trimmed {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func jsonObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) != 0 && json.Unmarshal(raw, &value) == nil && value != nil
}
