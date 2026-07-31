package owner

import "encoding/json"

// DefaultTools is the current P1 typed-service owner read registry. It
// intentionally has no catch-all tool: every MCP name selects one fixed
// daemon typed method.
func DefaultTools() []Tool {
	return []Tool{
		tool("profile.get", "Read the active profile.", objectSchema(``)),
		tool("user.me", "Read the active user.", objectSchema(``)),
		tool("user.get", "Read selected users.", objectSchema(`"user_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}}`, "user_ids")),
		tool("daemon.status", "Read daemon status.", objectSchema(``)),
		tool("doctor.get", "Read daemon diagnostics.", objectSchema(``)),
		tool("conversation.list", "List conversations.", pageSchema(``)),
		tool("conversation.get", "Read one conversation.", objectSchema(`"conversation_id":{"type":"string"}`, "conversation_id")),
		tool("conversation.search", "Search conversations.", searchSchema()),
		tool("conversation.unread", "Read unread conversation count.", objectSchema(``)),
		tool("message.history", "Read conversation message history.", pageSchema(`"conversation_id":{"type":"string"}`, "conversation_id")),
		tool("message.search", "Search messages in one conversation.", searchSchemaWithConversation()),
		tool("message.get", "Read one message.", objectSchema(`"conversation_id":{"type":"string"},"message_id":{"type":"string"}`, "conversation_id", "message_id")),
		tool("group.list", "List groups.", pageSchema(``)),
		tool("group.get", "Read one group.", objectSchema(`"group_id":{"type":"string"}`, "group_id")),
		tool("group.search", "Search groups.", searchSchema()),
		tool("group.members.list", "List group members.", pageSchema(`"group_id":{"type":"string"}`, "group_id")),
		tool("group.members.search", "Search group members.", groupMemberSearchSchema()),
		tool("friend.list", "List friends.", pageSchema(``)),
		tool("friend.get", "Read one friend.", objectSchema(`"user_id":{"type":"string"}`, "user_id")),
		tool("friend.search", "Search friends.", searchSchema()),
		tool("blacklist.list", "List blacklist entries.", pageSchema(``)),
		tool("blacklist.get", "Read one blacklist entry.", objectSchema(`"user_id":{"type":"string"}`, "user_id")),
	}
}

func tool(method, description string, schema json.RawMessage) Tool {
	return Tool{Name: "abdim." + method, Description: description, Method: method, InputSchema: schema}
}

func objectSchema(properties string, required ...string) json.RawMessage {
	schema := `{"type":"object"`
	if properties != "" {
		schema += `,"properties":{` + properties + `}`
	}
	if len(required) != 0 {
		payload, _ := json.Marshal(required)
		schema += `,"required":` + string(payload)
	}
	return json.RawMessage(schema + `}`)
}

func pageSchema(properties string, required ...string) json.RawMessage {
	page := `"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}`
	if properties != "" {
		properties += `,`
	}
	required = append(required, "limit")
	return objectSchema(properties+page, required...)
}

func searchSchema() json.RawMessage {
	return objectSchema(`"query":{"type":"string","minLength":1,"maxLength":256},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}`, "query", "limit")
}

func searchSchemaWithConversation() json.RawMessage {
	return objectSchema(`"conversation_id":{"type":"string"},"query":{"type":"string","minLength":1,"maxLength":256},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}`, "conversation_id", "query", "limit")
}

func groupMemberSearchSchema() json.RawMessage {
	return objectSchema(`"group_id":{"type":"string"},"query":{"type":"string","minLength":1,"maxLength":256},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}`, "group_id", "query", "limit")
}
