// Package commands defines the fixed abdim CLI command catalog.
package commands

import "encoding/json"

// Command documents one typed daemon method exposed through abdim.
type Command struct {
	Method      string          `json:"method"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Resolve maps a space-separated CLI command path to one catalog method.
func Resolve(args []string, catalog []Command) (string, int) {
	methods := make(map[string]struct{}, len(catalog))
	for _, item := range catalog {
		methods[item.Method] = struct{}{}
	}
	for index, argument := range args {
		if len(argument) >= 2 && argument[:2] == "--" {
			break
		}
		method := ""
		for part := 0; part <= index; part++ {
			if part > 0 {
				method += "."
			}
			method += args[part]
		}
		if _, exists := methods[method]; exists {
			return method, index + 1
		}
	}
	return "", 0
}
