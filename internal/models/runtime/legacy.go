package runtime

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// inferAPIFromURL preserves protocol selection for legacy URLs.
func inferAPIFromURL(baseURL string, current api.API) api.API {
	lower := strings.ToLower(strings.TrimRight(baseURL, "/"))
	switch {
	case strings.HasSuffix(lower, "/anthropic") || strings.HasSuffix(lower, "/anthropic/v1"):
		return api.APIAnthropicMessages
	case current == api.APIGoogleGenerativeAI &&
		(strings.HasSuffix(lower, "/openai") || strings.Contains(lower, "/openai/")):
		return api.APIOpenAICompletions
	}
	return current
}

// applyLegacyThinkingControl maps the pre-catalog thinking_control value
// onto the new settings. Unknown non-empty values keep the historical
// fallback (chat_template_kwargs). It reports whether a stored value was
// applied, so the caller can still silence catalogued non-reasoning models
// when a legacy default is ignored.
func applyLegacyThinkingControl(s *api.OpenAICompletionsSettings, value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return false
	}
	// The pre-catalog editor persisted "none" as OpenRouter's default for
	// every remote model row. It was not an explicit request to suppress the
	// provider's newer native reasoning format, which is needed to honor the
	// caller's thinking=false setting. Keep honoring every non-default legacy
	// encoding as an explicit per-row override.
	if normalized == "none" && s.ThinkingFormat == api.ThinkingFormatOpenRouter {
		return false
	}
	switch normalized {
	case "none":
		s.ThinkingFormat = api.ThinkingFormatNone
	case "enable_thinking":
		s.ThinkingFormat = api.ThinkingFormatEnableThinking
	case "thinking_type":
		s.ThinkingFormat = api.ThinkingFormatThinkingType
	default:
		s.ThinkingFormat = api.ThinkingFormatChatTemplateKwargs
	}
	return true
}
