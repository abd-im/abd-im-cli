package commands

import "encoding/json"

// Owner is the fixed local management command registry.
func Owner() []Command {
	return []Command{
		command("profile.get", "Read the active profile.", objectSchema(``)),
		command("user.me", "Read the active user.", objectSchema(``)),
		command("user.get", "Read selected users.", objectSchema(`"user_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}}`, "user_ids")),
		command("daemon.status", "Read daemon status.", objectSchema(``)),
		command("doctor.get", "Read daemon diagnostics.", objectSchema(``)),
		command("conversation.list", "List conversations.", pageSchema(``)),
		command("conversation.get", "Read one conversation.", objectSchema(`"conversation_id":{"type":"string"}`, "conversation_id")),
		command("conversation.search", "Search conversations.", searchSchema()),
		command("message.history", "Read conversation message history.", pageSchema(`"conversation_id":{"type":"string"}`, "conversation_id")),
		command("message.search", "Search messages in one conversation.", searchSchemaWithConversation()),
		command("message.get", "Read one message.", objectSchema(`"conversation_id":{"type":"string"},"message_id":{"type":"string"}`, "conversation_id", "message_id")),
		command("group.list", "List groups.", pageSchema(``)),
		command("group.get", "Read one group.", objectSchema(`"group_id":{"type":"string"}`, "group_id")),
		command("group.search", "Search groups.", searchSchema()),
		command("group.members.list", "List group members.", pageSchema(`"group_id":{"type":"string"}`, "group_id")),
		command("group.members.search", "Search group members.", groupMemberSearchSchema()),
		command("friend.list", "List friends.", pageSchema(``)),
		command("friend.get", "Read one friend.", objectSchema(`"user_id":{"type":"string"}`, "user_id")),
		command("friend.search", "Search friends.", searchSchema()),
		command("blacklist.list", "List blacklist entries.", pageSchema(``)),
		command("blacklist.get", "Read one blacklist entry.", objectSchema(`"user_id":{"type":"string"}`, "user_id")),
		command("run.list", "List bounded daemon run diagnostics.", pageSchema(``)),
		command("run.cancel", "Cancel one active daemon run.", objectSchema(`"run_id":{"type":"string","minLength":1}`, "run_id")),
		command("operation.get", "Read one redacted operation diagnostic.", objectSchema(`"operation_id":{"type":"string","minLength":1}`, "operation_id")),
		command("operation.mark_unknown", "Mark one operation outcome unknown.", objectSchema(`"operation_id":{"type":"string","minLength":1}`, "operation_id")),
	}
}

func command(method, description string, schema json.RawMessage) Command {
	return Command{Method: method, Description: description, InputSchema: schema}
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
