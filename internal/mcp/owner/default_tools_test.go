package owner

import (
	"encoding/json"
	"sort"
	"testing"
)

func TestDefaultToolsExposeOnlyTheFixedP1OwnerReadRegistry(t *testing.T) {
	tools := DefaultTools()
	if len(tools) != 22 {
		t.Fatalf("DefaultTools() count = %d, want 22", len(tools))
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Method == "" || tool.Name != "abdim."+tool.Method || !isJSONObject(tool.InputSchema) {
			t.Fatalf("invalid default tool = %+v", tool)
		}
		names = append(names, tool.Name)
	}
	for _, name := range []string{"abdim.conversation.list", "abdim.message.history", "abdim.group.members.list", "abdim.blacklist.list"} {
		if !schemaRequires(t, tools, name, "limit") {
			t.Fatalf("%s schema does not require limit", name)
		}
	}
	sort.Strings(names)
	for _, forbidden := range []string{"abdim.daemon.shutdown", "abdim.rpc.call", "abdim.sdk.call"} {
		if contains(names, forbidden) {
			t.Fatalf("DefaultTools() exposes %q", forbidden)
		}
	}
}

func schemaRequires(t *testing.T, tools []Tool, name, field string) bool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		for _, required := range schema.Required {
			if required == field {
				return true
			}
		}
		return false
	}
	t.Fatalf("missing tool %s", name)
	return false
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func contains(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}
