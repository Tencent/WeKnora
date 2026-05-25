// internal/handler/parser_engine_allowlist.go
package handler

import (
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const parserEngineAllowListEnv = "PARSER_ENGINE_ALLOW_LIST"

// supportedParserEngines 与 internal/infrastructure/docparser/engine_registry.go
// 中的 RegisterEngine 列表对齐。
var supportedParserEngines = []string{
	"builtin", "simple", "weknoracloud", "mineru", "mineru_cloud",
}

func getSupportedParserEngines() []string {
	out := make([]string, len(supportedParserEngines))
	copy(out, supportedParserEngines)
	return out
}

// getAllowedParserEngines parses PARSER_ENGINE_ALLOW_LIST. Empty env → all
// supported engines allowed (backwards compatible). Unknown names are silently
// dropped. Accepts , ; | \n \t and space as separators.
func getAllowedParserEngines() map[string]bool {
	raw := strings.TrimSpace(os.Getenv(parserEngineAllowListEnv))
	allowed := make(map[string]bool, len(supportedParserEngines))

	if raw == "" {
		for _, name := range supportedParserEngines {
			allowed[name] = true
		}
		return allowed
	}

	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	}) {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		for _, supported := range supportedParserEngines {
			if name == supported {
				allowed[name] = true
				break
			}
		}
	}

	return allowed
}

// isParserEngineAllowed returns true when the engine is in the allow list (or
// the env var is unset). Empty name returns true (defensive default).
func isParserEngineAllowed(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	return getAllowedParserEngines()[name]
}

// firstAllowedParserEngine returns the first supported engine name that is
// allowed by the current allow list, in supportedParserEngines order. Returns
// "" if none are allowed.
func firstAllowedParserEngine() string {
	allowed := getAllowedParserEngines()
	for _, name := range supportedParserEngines {
		if allowed[name] {
			return name
		}
	}
	return ""
}

// allowedParserEnginesSorted returns the supported names that are currently
// allowed, in supportedParserEngines order. Used for ListParserEngines response.
func allowedParserEnginesSorted() []string {
	allowed := getAllowedParserEngines()
	out := make([]string, 0, len(supportedParserEngines))
	for _, name := range supportedParserEngines {
		if allowed[name] {
			out = append(out, name)
		}
	}
	return out
}

// resolveDefaultParserEngine returns the engine name the frontend should
// pre-select as the default for new KB parser_engine_rules.
//
// Precedence:
//  1. builtin yaml `parser_engine.default_engine` (if set and allowed by
//     PARSER_ENGINE_ALLOW_LIST and in supportedParserEngines)
//  2. "" (frontend falls back to its built-in heuristic: first available + allowed)
//
// Unknown / disallowed names yield "" — never silently mislead the UI.
func resolveDefaultParserEngine() string {
	b := types.GetBuiltinParserEngine()
	if b == nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(b.DefaultEngine))
	if name == "" {
		return ""
	}
	if !isParserEngineAllowed(name) {
		return ""
	}
	for _, supported := range supportedParserEngines {
		if name == supported {
			return name
		}
	}
	return ""
}
