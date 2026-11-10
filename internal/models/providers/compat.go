package providers

import "github.com/Tencent/WeKnora/internal/models/api"

// VendorCompat carries a vendor's defaults for every protocol it may speak.
type VendorCompat struct {
	OpenAICompletions  api.OpenAICompletionsCompat  `json:"openai_completions,omitempty"`
	OpenAIResponses    api.OpenAIResponsesCompat    `json:"openai_responses,omitempty"`
	AnthropicMessages  api.AnthropicMessagesCompat  `json:"anthropic_messages,omitempty"`
	GoogleGenerativeAI api.GoogleGenerativeAICompat `json:"google_generative_ai,omitempty"`
	// Rerank is the rerank protocol overlay. It has no per-protocol variants:
	// a vendor serves exactly one rerank dialect.
	Rerank api.RerankCompat `json:"rerank,omitempty"`
	// Embeddings is the embedding protocol overlay, likewise one per vendor.
	Embeddings api.EmbeddingsCompat `json:"embeddings,omitempty"`
	// Transcriptions is the speech-to-text overlay, likewise one per vendor.
	Transcriptions api.TranscriptionsCompat `json:"transcriptions,omitempty"`
}
