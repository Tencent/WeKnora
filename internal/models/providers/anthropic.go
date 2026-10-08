// Package providers registers Anthropic's native Messages API.
//
// Facts (https://platform.claude.com/docs/en/api/messages — docs.anthropic.com
// now redirects there):
//   - authentication is the `x-api-key` header plus `anthropic-version`,
//     still 2023-06-01 in every current example;
//   - `max_tokens` is mandatory (the protocol default of 4096 applies when
//     the caller sets no budget);
//   - `output_config.effort` takes low | medium | high | xhigh | max and
//     nothing else — there is no "minimal" rung, so the vendor level map
//     folds minimal onto low
//     (https://platform.claude.com/docs/en/build-with-claude/effort).
//     `max` is listed for Fable 5.1 / Fable 5 / Opus 5 / Opus 4.8 / Opus 4.7 /
//     Opus 4.6 / Sonnet 5 / Sonnet 4.6; `xhigh` for the same set minus
//     Opus 4.6 and Sonnet 4.6, which is why those two map xhigh onto max;
//   - Claude 4.7 and later reject `thinking: {"type": "enabled"}` with a 400
//     and must use `{"type": "adaptive"}`; it is deprecated but still
//     accepted on the 4.6 generation, and it is the only mode on 4.5 and
//     earlier
//     (https://platform.claude.com/docs/en/build-with-claude/thinking-troubleshooting#supported-models);
//   - only Fable 5.1 / Fable 5 (and the limited-availability Mythos models)
//     are "always on" and reject `thinking: {"type": "disabled"}`, so only
//     those entries carry "off": null. Opus 5, Opus 4.8 and Opus 4.7 accept
//     "disabled" (Opus 5 at effort high or below, which is what a caller
//     asking for "off" gets because effort and off are mutually exclusive
//     here);
//   - Claude Opus 4.5 is the one extended-thinking-only model that also
//     accepts `output_config.effort`, so it carries supports_effort with
//     budget mode;
//   - temperature and top_p cannot be sent alongside thinking; prompt
//     caching uses `cache_control` breakpoints (including on tools), offers
//     a 1h TTL and reports cache usage;
//   - interleaved thinking still uses the `interleaved-thinking-2025-05-14`
//     beta header, and only in manual budget mode: adaptive models interleave
//     on their own and the Claude API ignores the header there.
//
// Unverified: Claude Mythos 5.1 / Mythos 5 / Mythos Preview are documented
// but limited-availability, so they are left out of the catalog.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/anthropic.svg
var anthropicIcon []byte

// AnthropicID is the provider identifier stored on model rows.
const AnthropicID = "anthropic"

// AnthropicBaseURL is the documented first-party endpoint.
const AnthropicBaseURL = "https://api.anthropic.com/v1"

func newAnthropicProvider() *Definition {
	return &Definition{
		ID:    AnthropicID,
		Name:  "Anthropic",
		Names: map[string]string{"zh-CN": "Anthropic"},
		Description: "Claude models via the native Anthropic Messages API: " +
			"claude-opus-5, claude-sonnet-5, claude-haiku-4-5, etc.",
		Website:      "https://console.anthropic.com",
		Icon:         anthropicIcon,
		API:          api.APIAnthropicMessages,
		Order:        32,
		RequiresAuth: true,
		Auth:         AuthXAPIKey,
		URLPatterns:  []string{"api.anthropic.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AnthropicBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		// Protocol defaults (budget-mode thinking, 2023-06-01 version) apply
		// at vendor level; adaptive-effort models override per entry.
		Compat: VendorCompat{},
		// Anthropic's effort ladder has no "minimal" rung; fold it onto low
		// for every model so a caller asking for minimal never sends a value
		// the Messages API rejects.
		ThinkingLevels: api.ThinkingLevelMap{api.ReasoningMinimal: api.StringPtr("low")},
	}
}
