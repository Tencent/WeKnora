package skills

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// UnmarshalSkillFrontmatter decodes the YAML between SKILL.md's --- markers.
//
// Third-party skills (ClawHub / SkillHub) often indent `version` / `description`
// under `name:` as if it were a nested mapping, or leave a colon unquoted in
// a scalar. Strict YAML rejects both with "mapping values are not allowed in
// this context". When the first parse fails, those two conservative repairs
// are retried so those archives still install; valid frontmatter is unchanged.
//
// The decode always lands on a temporary value and is copied into dest only
// on success, so a failed candidate cannot leave dest half-written.
// repaired is true when a repair candidate was what succeeded.
func UnmarshalSkillFrontmatter(frontmatter string, dest any) (repaired bool, err error) {
	firstErr := unmarshalFrontmatterCopy(frontmatter, dest)
	if firstErr == nil {
		return false, nil
	}
	for _, candidate := range frontmatterRepairCandidates(frontmatter) {
		if candidate == frontmatter {
			continue
		}
		if unmarshalFrontmatterCopy(candidate, dest) == nil {
			return true, nil
		}
	}
	return false, firstErr
}

func unmarshalFrontmatterCopy(src string, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("frontmatter dest must be a non-nil pointer")
	}
	tmp := reflect.New(rv.Elem().Type())
	if err := yaml.Unmarshal([]byte(src), tmp.Interface()); err != nil {
		return err
	}
	rv.Elem().Set(tmp.Elem())
	return nil
}

func frontmatterRepairCandidates(frontmatter string) []string {
	outdented := repairAccidentalNestedFrontmatter(frontmatter)
	quoted := quoteColonInUnquotedScalars(frontmatter)
	both := quoteColonInUnquotedScalars(outdented)
	return []string{outdented, quoted, both}
}

// repairAccidentalNestedFrontmatter outdents keys that were nested under a
// plain scalar, e.g.
//
//	name: 命理大师
//	  version: 1.2.6
//	  description: |
//
// A real nested mapping (`compatibility:` with no value on the same line) is
// left alone because it is valid YAML and does not need this pass.
func repairAccidentalNestedFrontmatter(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		line := lines[i]
		out = append(out, line)
		i++
		if !isPlainScalarMapping(strings.TrimSpace(line)) {
			continue
		}
		indent := leadingWS(line)
		if i >= len(lines) {
			break
		}
		next := lines[i]
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			out = append(out, lines[i])
			i++
			if i < len(lines) {
				next = lines[i]
			}
		}
		if i >= len(lines) {
			break
		}
		nextIndent := leadingWS(next)
		if nextIndent <= indent || !looksLikeYAMLKey(strings.TrimSpace(next)) {
			continue
		}
		extra := nextIndent - indent
		for i < len(lines) {
			cur := lines[i]
			if strings.TrimSpace(cur) == "" {
				out = append(out, cur)
				i++
				continue
			}
			curIndent := leadingWS(cur)
			if curIndent < nextIndent {
				break
			}
			out = append(out, stripLeadingWS(cur, extra))
			i++
		}
	}
	return strings.Join(out, "\n")
}

var unquotedColonScalarRE = regexp.MustCompile(
	`^(\s*(?:name|description)\s*:\s*)([^"'|>{\[\s#].*:.+)$`,
)

// quoteColonInUnquotedScalars wraps name/description values that contain a
// colon so `description: Foo: bar` is not parsed as a nested mapping.
func quoteColonInUnquotedScalars(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		m := unquotedColonScalarRE.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		value := strings.TrimSpace(m[2])
		if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") {
			continue
		}
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		lines[i] = m[1] + `"` + escaped + `"`
	}
	return strings.Join(lines, "\n")
}

func isPlainScalarMapping(trimmed string) bool {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	key, value, ok := strings.Cut(trimmed, ":")
	if !ok || strings.TrimSpace(key) == "" {
		return false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "#") {
		return false
	}
	if value == "|" || value == ">" ||
		strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") ||
		strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
		return false
	}
	return true
}

func looksLikeYAMLKey(trimmed string) bool {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
		return false
	}
	key, _, ok := strings.Cut(trimmed, ":")
	if !ok {
		return false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if strings.HasPrefix(key, `"`) || strings.HasPrefix(key, "'") {
		return true
	}
	for i, r := range key {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func leadingWS(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

func stripLeadingWS(s string, n int) string {
	if n <= 0 {
		return s
	}
	i := 0
	for i < len(s) && i < n && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}
