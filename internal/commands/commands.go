// Package commands defines the fixed abdim CLI commands.
package commands

// Resolve maps a space-separated CLI command path to one daemon method.
func Resolve(args, commands []string) (string, int) {
	methods := make(map[string]struct{}, len(commands))
	for _, method := range commands {
		methods[method] = struct{}{}
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
