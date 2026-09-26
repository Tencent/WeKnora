package manifest

import (
	"fmt"
	"regexp"
	"strings"
)

// Config scopes a template may reference.
const (
	// ScopeConfig is the workspace's plugin configuration (config.tenant).
	ScopeConfig = "config"
	// ScopeSystem is the platform-wide plugin configuration (config.system).
	ScopeSystem = "system"
)

// templateRef matches ${scope.key}.
var templateRef = regexp.MustCompile(`\$\{([a-zA-Z]+)\.([a-zA-Z0-9_]+)\}`)

// TemplateRef is one ${scope.key} in a templated value.
type TemplateRef struct {
	Scope string
	Key   string
}

// TemplateRefs lists the references in a templated value.
func TemplateRefs(s string) []TemplateRef {
	var out []TemplateRef
	for _, m := range templateRef.FindAllStringSubmatch(s, -1) {
		out = append(out, TemplateRef{Scope: m[1], Key: m[2]})
	}
	return out
}

// ExpandTemplate replaces each ${scope.key} with lookup's answer. It fails
// when a referenced value is missing or empty, so a request is never sent
// with a half-filled credential.
func ExpandTemplate(s string, lookup func(scope, key string) (string, bool)) (string, error) {
	var missing []string
	out := templateRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := templateRef.FindStringSubmatch(ref)
		v, ok := lookup(m[1], m[2])
		if !ok || v == "" {
			missing = append(missing, m[1]+"."+m[2])
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("not configured: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func validateTemplate(value, where string, add func(string, ...any)) {
	for _, ref := range TemplateRefs(value) {
		if ref.Scope != ScopeConfig && ref.Scope != ScopeSystem {
			add("%s references ${%s.%s}; only ${config.<key>} and ${system.<key>} are allowed",
				where, ref.Scope, ref.Key)
		}
	}
	if rest := templateRef.ReplaceAllString(value, ""); strings.Contains(rest, "${") {
		add("%s has a malformed ${...} reference", where)
	}
}
