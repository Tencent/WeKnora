package types

import (
	"os"
	"regexp"
	"strings"
)

// envPattern matches ${NAME} and ${NAME:-default} placeholders. Shared between
// built-in config loaders.
var envPattern = regexp.MustCompile(`\${([^}]+)}`)

// expandEnv substitutes ${NAME} occurrences with the corresponding os.Getenv
// value. Two forms are supported:
//
//	${NAME}            unset/empty → literal ${NAME} (string fields)
//	${NAME:-default}   unset/empty → default        (non-string fields like bool/int)
//
// The default-value form is required when the target YAML field is not a string
// (e.g. use_ssl: bool), because YAML would fail to parse a leftover ${NAME}
// literal as its target type.
func expandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-1]
		name, def, hasDefault := strings.Cut(inner, ":-")
		if v := os.Getenv(name); v != "" {
			return v
		}
		if hasDefault {
			return def
		}
		return m
	})
}
