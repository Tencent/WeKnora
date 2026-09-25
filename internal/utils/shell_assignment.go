package utils

import (
	"regexp"
	"strings"
)

// shellAssignmentPattern finds NAME=value in a model-built shell command.
// The name must be UPPER_SNAKE_CASE so flags like --model and URLs are not
// treated as environment variables.
var shellAssignmentPattern = regexp.MustCompile(
	`(?:^|[;|&\s])(?:export\s+)?([A-Z_][A-Z0-9_]{0,127})=(?:"([^"]*)"|'([^']*)'|([^\s;|&]+))`,
)

// MaskCommandAssignments replaces the value of every NAME=value assignment
// with a placeholder. A command is logged at Info, and passing a credential
// inline is a documented way to hand a skill its key, so the raw string must
// never reach the log.
func MaskCommandAssignments(command string) string {
	return shellAssignmentPattern.ReplaceAllStringFunc(command, func(match string) string {
		eq := strings.Index(match, "=")
		if eq < 0 {
			return match
		}
		return match[:eq+1] + "***"
	})
}

// ExtractShellAssignments returns NAME=value pairs from a model-built command.
func ExtractShellAssignments(command string) map[string]string {
	out := map[string]string{}
	for _, match := range shellAssignmentPattern.FindAllStringSubmatch(command, -1) {
		name := match[1]
		value := match[2] + match[3] + match[4]
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	return out
}
