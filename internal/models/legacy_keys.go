package models

// Extra-config keys honoured by Resolve. They predate the catalog and stay
// supported so existing model rows keep working unchanged.
const (
	// ExtraAPI forces a protocol ("openai-responses", "anthropic-messages", ...).
	ExtraAPI = "api"
	// ExtraRemoteModelName sends a different model id on the wire.
	ExtraRemoteModelName = "remote_model_name"
	// ExtraAPIVersion is the Azure api-version query parameter.
	ExtraAPIVersion = "api_version"
	// ExtraThinkingControl is the legacy thinking encoding selector written
	// by older UIs: none | enable_thinking | thinking_type | chat_template_kwargs.
	ExtraThinkingControl = "thinking_control"
	// ExtraTruncatePromptTokens is the vLLM-only server-side truncation
	// budget for rerank, opt-in per row. It is never sent unless the operator
	// set it: vendors that do not implement it reject the unknown field.
	ExtraTruncatePromptTokens = "truncate_prompt_tokens"
	// LegacyTruncatePromptTokens is the embedding truncation budget the
	// pre-catalog OpenAI-compatible client sent whenever a row left it at 0.
	LegacyTruncatePromptTokens = 511
	// ExtraScoreScale overrides the vendor rerank score scale for one row.
	// A self-hosted gateway serves whatever reranker was deployed behind it
	// and the two families disagree: BGE-class models answer a 0..1
	// probability, Qwen3-Reranker-class models an unbounded score. A vendor
	// can only state what its own documentation shows.
	ExtraScoreScale = "score_scale"
)
