// Package stdio implements the modern MCP JSON-RPC stream boundary.
package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	ProtocolVersion = "2026-07-28"
	maxMessageBytes = 1 << 20
)

// Request is a structurally valid MCP JSON-RPC request. Notifications are
// consumed by Serve and never passed to its handler.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// Handler dispatches one request and returns a JSON-RPC response value.
type Handler func(context.Context, Request) (response any, send bool)

// RPCError is a JSON-RPC error response payload.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e RPCError) Error() string { return e.Message }

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Error   RPCError        `json:"error"`
}

type successResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// Success returns a valid JSON-RPC result response.
func Success(id json.RawMessage, result any) any {
	return successResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// Error returns a valid JSON-RPC error response. id is omitted when it could
// not be read from a malformed request.
func Error(id json.RawMessage, err RPCError) any {
	return errorResponse{JSONRPC: "2.0", ID: id, Error: err}
}

// Serve reads newline-delimited MCP messages and writes only JSON-RPC
// responses. It returns when input closes, context is cancelled, or I/O fails.
func Serve(ctx context.Context, input io.Reader, output io.Writer, handler Handler) error {
	if input == nil || output == nil || handler == nil {
		return errors.New("MCP input, output, and handler are required")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxMessageBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, send := decode(scanner.Bytes(), ctx, handler)
		if !send {
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

type wireRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func decode(payload []byte, ctx context.Context, handler Handler) (any, bool) {
	var request wireRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		if !json.Valid(payload) {
			return Error(nil, RPCError{Code: -32700, Message: "parse error"}), true
		}
		return Error(nil, RPCError{Code: -32600, Message: "invalid request"}), true
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		return Error(nil, RPCError{Code: -32600, Message: "invalid request"}), true
	}
	if len(request.ID) == 0 {
		return nil, false
	}
	if !validRequestID(request.ID) {
		return Error(nil, RPCError{Code: -32600, Message: "invalid request"}), true
	}
	return handler(ctx, Request{ID: request.ID, Method: request.Method, Params: request.Params})
}

// ValidateMeta verifies the modern MCP fields required on every request.
func ValidateMeta(raw json.RawMessage) error {
	var params map[string]json.RawMessage
	if !IsJSONObject(raw) || json.Unmarshal(raw, &params) != nil {
		return RPCError{Code: -32602, Message: "request metadata is required"}
	}
	metaRaw, exists := params["_meta"]
	if !exists || !IsJSONObject(metaRaw) {
		return RPCError{Code: -32602, Message: "request metadata is required"}
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return RPCError{Code: -32602, Message: "request metadata is required"}
	}
	var version string
	if rawVersion, exists := meta["io.modelcontextprotocol/protocolVersion"]; !exists || json.Unmarshal(rawVersion, &version) != nil || version == "" {
		return RPCError{Code: -32602, Message: "MCP protocol version is required"}
	}
	if rawCapabilities, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists || !IsJSONObject(rawCapabilities) {
		return RPCError{Code: -32602, Message: "client capabilities are required"}
	}
	if version != ProtocolVersion {
		return RPCError{Code: -32022, Message: "unsupported protocol version", Data: map[string]any{
			"supported": []string{ProtocolVersion}, "requested": version,
		}}
	}
	return nil
}

// IsJSONObject reports whether raw is a non-null JSON object.
func IsJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) != 0 && json.Unmarshal(raw, &value) == nil && value != nil
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
