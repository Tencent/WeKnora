package tools

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/sandbox"
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

func inferSkillName(explicit, command string) string {
	if sandbox.IsValidSkillName(strings.TrimSpace(explicit)) {
		return strings.TrimSpace(explicit)
	}
	prefix := sandbox.SkillsImageRoot + "/"
	from := 0
	for {
		idx := strings.Index(command[from:], prefix)
		if idx < 0 {
			return ""
		}
		idx += from
		name, ok := firstPathSegment(command[idx+len(prefix):])
		if ok && sandbox.IsValidSkillName(name) {
			return name
		}
		from = idx + len(prefix)
	}
}

func firstPathSegment(rest string) (string, bool) {
	end := 0
	for end < len(rest) {
		r := rune(rest[end])
		if r == '/' || unicode.IsSpace(r) || r == '"' || r == '\'' || r == ';' || r == '&' || r == '|' {
			break
		}
		end++
	}
	if end == 0 {
		return "", false
	}
	return rest[:end], true
}
