// Package owner exposes a fixed owner-only MCP tool registry over stdio.
package owner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/mcp/stdio"
)

const ProtocolVersion = stdio.ProtocolVersion

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
		if !stdio.IsJSONObject(tool.InputSchema) {
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
	return stdio.Serve(ctx, input, output, s.handle)
}

func (s *Server) handle(ctx context.Context, request stdio.Request) (any, bool) {
	if err := stdio.ValidateMeta(request.Params); err != nil {
		return stdio.Error(request.ID, asRPCError(err)), true
	}
	switch request.Method {
	case "server/discover":
		return stdio.Success(request.ID, s.discovery()), true
	case "tools/list":
		if err := validateListParams(request.Params); err != nil {
			return stdio.Error(request.ID, asRPCError(err)), true
		}
		return stdio.Success(request.ID, s.toolList()), true
	case "tools/call":
		result, err := s.call(ctx, request.Params)
		if err != nil {
			return stdio.Error(request.ID, asRPCError(err)), true
		}
		return stdio.Success(request.ID, result), true
	default:
		return stdio.Error(request.ID, stdio.RPCError{Code: -32601, Message: "method not found"}), true
	}
}

func asRPCError(err error) stdio.RPCError {
	var rpc stdio.RPCError
	if !errors.As(err, &rpc) {
		return stdio.RPCError{Code: -32603, Message: "internal error"}
	}
	return rpc
}

func (s *Server) discovery() any {
	return struct {
		ResultType        string         `json:"resultType"`
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
		Meta              map[string]any `json:"_meta"`
	}{
		ResultType:        "complete",
		SupportedVersions: []string{stdio.ProtocolVersion},
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
		return nil, stdio.RPCError{Code: -32602, Message: "invalid tool parameters"}
	}
	tool, exists := s.tools[params.Name]
	if !exists {
		return nil, stdio.RPCError{Code: -32602, Message: "unknown tool"}
	}
	arguments := json.RawMessage(`{}`)
	if len(params.Arguments) != 0 {
		if !stdio.IsJSONObject(params.Arguments) {
			return nil, stdio.RPCError{Code: -32602, Message: "tool arguments must be a JSON object"}
		}
		arguments = append(json.RawMessage(nil), params.Arguments...)
	}
	requestID, err := localRequestID()
	if err != nil {
		return nil, stdio.RPCError{Code: -32603, Message: "internal error"}
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
		return nil, stdio.RPCError{Code: -32603, Message: "internal error"}
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, stdio.RPCError{Code: -32603, Message: "internal error"}
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

func (s *Server) resultMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/serverInfo": map[string]string{"name": "abdim", "version": contracts.APIVersionV1},
	}
}

func validateListParams(raw json.RawMessage) error {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return stdio.RPCError{Code: -32602, Message: "invalid tool list parameters"}
	}
	if params.Cursor != "" {
		return stdio.RPCError{Code: -32602, Message: "tool list cursor is not available"}
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
