package tools

import (
	"regexp"
	"strings"
)

// assignmentPattern finds NAME=value in a model-built shell command.
// It is not used on user chat text. The name must be UPPER_SNAKE_CASE so
// flags like --model and URLs are not treated as environment variables.
var assignmentPattern = regexp.MustCompile(
	`(?:^|[;|&\s])(?:export\s+)?([A-Z_][A-Z0-9_]{0,127})=(?:"([^"]*)"|'([^']*)'|([^\s;|&]+))`,
)

func extractExportedEnv(command string) map[string]string {
	out := map[string]string{}
	for _, match := range assignmentPattern.FindAllStringSubmatch(command, -1) {
		name := match[1]
		value := match[2] + match[3] + match[4]
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	return out
}

func collectUsedSkillEnv(command string, toolEnv map[string]string) map[string]string {
	out := extractExportedEnv(command)
	for name, value := range toolEnv {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[name] = value
	}
	return out
}

// maskCommandAssignments replaces the value of every NAME=value assignment with
// a placeholder. A command is logged at Info, and passing a credential inline is
// a documented way to hand a skill its key, so the raw string must never reach
// the log.
func maskCommandAssignments(command string) string {
	return assignmentPattern.ReplaceAllStringFunc(command, func(match string) string {
		eq := strings.Index(match, "=")
		if eq < 0 {
			return match
		}
		return match[:eq+1] + "***"
	})
}
