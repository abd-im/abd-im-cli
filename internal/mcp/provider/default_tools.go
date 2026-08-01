package provider

import (
	"encoding/json"

	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/mcp/owner"
)

// DefaultTools maps the fixed typed-method registry to provider MCP names.
// The supplied methods are already a construction-time authorization snapshot;
// unknown methods intentionally have no MCP representation.
func DefaultTools(methods []string) []Tool {
	allowed := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		allowed[method] = struct{}{}
	}
	tools := make([]Tool, 0, len(allowed))
	for _, tool := range owner.DefaultTools() {
		if _, exists := allowed[tool.Method]; !exists {
			continue
		}
		tools = append(tools, Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Method:      tool.Method,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[groupcapability.Method]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + groupcapability.Method,
			Description: "Create a group with approved members.",
			Method:      groupcapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"member_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["name","member_ids","idempotency_key"]}`),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.Method]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + messagecapability.Method,
			Description: "Send a text message to an approved user or group.",
			Method:      messagecapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
			Visible:     func() bool { return true },
		})
	}
	return tools
}
