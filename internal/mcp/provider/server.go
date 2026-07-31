// Package provider exposes a run-private MCP tool registry over stdio.
package provider

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
	"github.com/abd-im/abd-im-cli/internal/mcp/stdio"
)

// Tool binds one provider-visible MCP name to a typed run-proxy method.
// Visible is evaluated when a run adapter is constructed from its manifest and
// grant snapshot; the run proxy enforces subsequent revocation and expiry.
type Tool struct {
	Name        string
	Description string
	Method      string
	InputSchema json.RawMessage
	Visible     func() bool
}

type registeredTool struct {
	Name        string
	Description string
	Method      string
	InputSchema json.RawMessage
}

// Server translates provider MCP requests into one run-private ToolProxy.
type Server struct {
	profileID string
	grant     string
	proxy     contracts.ToolProxy
	tools     map[string]registeredTool
	list      []registeredTool
}

// New creates a provider adapter. The opaque grant stays in this process and
// is never exposed through MCP arguments or responses.
func New(profileID, credential string, proxy contracts.ToolProxy, tools []Tool) (*Server, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	if strings.TrimSpace(credential) == "" {
		return nil, errors.New("run grant credential is required")
	}
	if proxy == nil {
		return nil, errors.New("run tool proxy is required")
	}

	registered := make(map[string]registeredTool, len(tools))
	list := make([]registeredTool, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if !validToolName(tool.Name) {
			return nil, fmt.Errorf("invalid MCP tool name %q", tool.Name)
		}
		if strings.TrimSpace(tool.Description) == "" || strings.TrimSpace(tool.Method) == "" || tool.Visible == nil {
			return nil, errors.New("MCP tool description, daemon method, and visibility are required")
		}
		if !stdio.IsJSONObject(tool.InputSchema) {
			return nil, fmt.Errorf("MCP tool %q input schema must be a JSON object", tool.Name)
		}
		if _, exists := seen[tool.Name]; exists {
			return nil, fmt.Errorf("duplicate MCP tool %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}
		if !tool.Visible() {
			continue
		}
		copy := registeredTool{
			Name:        tool.Name,
			Description: tool.Description,
			Method:      tool.Method,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		}
		registered[copy.Name] = copy
		list = append(list, copy)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return &Server{profileID: profileID, grant: credential, proxy: proxy, tools: registered, list: list}, nil
}

// Serve reads provider MCP stdio messages and writes only MCP JSON-RPC.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	return stdio.Serve(ctx, input, output, s.handle)
}

func (s *Server) handle(ctx context.Context, request stdio.Request) (any, bool) {
	switch request.Method {
	case "initialize":
		result, err := s.initialize(request.Params)
		if err != nil {
			return stdio.Error(request.ID, asRPCError(err)), true
		}
		return stdio.Success(request.ID, result), true
	case "server/discover":
		if err := stdio.ValidateMeta(request.Params); err != nil {
			return stdio.Error(request.ID, asRPCError(err)), true
		}
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

// initialize supports the standard MCP handshake used by Codex-launched stdio
// servers. server/discover remains available for the existing local adapter.
func (s *Server) initialize(raw json.RawMessage) (any, error) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if !stdio.IsJSONObject(raw) || json.Unmarshal(raw, &params) != nil || params.ProtocolVersion != stdio.ProtocolVersion {
		return nil, stdio.RPCError{Code: -32602, Message: "unsupported protocol version"}
	}
	return struct {
		ProtocolVersion string            `json:"protocolVersion"`
		Capabilities    map[string]any    `json:"capabilities"`
		ServerInfo      map[string]string `json:"serverInfo"`
	}{
		ProtocolVersion: stdio.ProtocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{"listChanged": false}},
		ServerInfo:      map[string]string{"name": "abdim-provider", "version": contracts.APIVersionV1},
	}, nil
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
		Name           string          `json:"name"`
		Arguments      json.RawMessage `json:"arguments"`
		IdempotencyKey string          `json:"idempotency_key"`
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
	if params.IdempotencyKey == "" {
		params.IdempotencyKey = idempotencyKey(arguments)
	}
	requestID, err := localRequestID()
	if err != nil {
		return nil, stdio.RPCError{Code: -32603, Message: "internal error"}
	}
	response, err := s.proxy.Call(ctx, contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      requestID,
		ProfileID:      s.profileID,
		Method:         tool.Method,
		Params:         arguments,
		Grant:          s.grant,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		response = failedResponse(requestID, contracts.CodeInternal, "run tool proxy failed", false)
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

func idempotencyKey(arguments json.RawMessage) string {
	var values map[string]json.RawMessage
	if json.Unmarshal(arguments, &values) != nil {
		return ""
	}
	var key string
	_ = json.Unmarshal(values["idempotency_key"], &key)
	return key
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *Server) resultMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/serverInfo": map[string]string{"name": "abdim-provider", "version": contracts.APIVersionV1},
	}
}

func asRPCError(err error) stdio.RPCError {
	var rpc stdio.RPCError
	if !errors.As(err, &rpc) {
		return stdio.RPCError{Code: -32603, Message: "internal error"}
	}
	return rpc
}

func validateListParams(raw json.RawMessage) error {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(raw) == 0 {
		return nil
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
