package langfuse

import (
	"time"
)

// TokenUsage captures the input/output/total token counts reported by the
// underlying model, in Langfuse's canonical schema. Set as the value of the
// langfuse.observation.usage_details span attribute (JSON-serialized).
type TokenUsage struct {
	Input      int    `json:"input,omitempty"`
	Output     int    `json:"output,omitempty"`
	Total      int    `json:"total,omitempty"`
	CacheRead  int    `json:"cache_read_input_tokens,omitempty"`
	CacheWrite int    `json:"cache_creation_input_tokens,omitempty"`
	CacheMiss  int    `json:"cache_miss_input_tokens,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

// Langfuse / OpenTelemetry semantic-convention attribute keys mirrored from
// the official langfuse-python v4 SDK (_client/attributes.py). These are set
// as span attributes on the OTLP wire; LiteFuse (and Langfuse v3+) index
// traces/generations off them.
const (
	attrObsType            = "langfuse.observation.type"
	attrObsInput           = "langfuse.observation.input"
	attrObsOutput          = "langfuse.observation.output"
	attrObsMetadata        = "langfuse.observation.metadata"
	attrObsModel           = "langfuse.observation.model.name"
	attrObsModelParams     = "langfuse.observation.model.parameters"
	attrObsUsageDetails    = "langfuse.observation.usage_details"
	attrObsCompletionStart = "langfuse.observation.completion_start_time"
	attrTraceName          = "langfuse.trace.name"
	attrTraceInput         = "langfuse.trace.input"
	attrTraceOutput        = "langfuse.trace.output"
	attrTraceMetadata      = "langfuse.trace.metadata"
	attrTraceTags          = "langfuse.trace.tags"
	attrUserID             = "user.id"
	attrSessionID          = "session.id"
	attrEnvironment        = "langfuse.environment"
	attrRelease            = "langfuse.release"
	attrLangfusePubKey     = "langfuse.public.key"

	// The LiteFuse/Langfuse v3 OTel gate keys the "events_full"
	// direct-write path on the instrumentation scope name. langfuse-python
	// v4 uses "langfuse-sdk"; LiteFuse's getSdkInfoFromResourceSpans only
	// requires the scope name to contain "langfuse" for server-side SDK
	// classification. The actual gate is the x-langfuse-ingestion-version:4
	// HTTP header (see exporter.go).
	langfuseScopeName    = "langfuse-sdk"
	langfuseScopeVersion = "4.0.0"
)

// Observation types carried by the langfuse.observation.type attribute.
const (
	obsTypeTrace      = "trace"
	obsTypeSpan       = "span"
	obsTypeGeneration = "generation"
)

// Specific observation types accepted by Langfuse's OTel ingestion. Pass one
// to SpanOptions.Type or GenerationOptions.Type so the UI groups the
// observation correctly: "agent" drives the Agent Graph, "tool" powers tool
// analytics, "retriever" and "embedding" feed retrieval/cost views, and
// "chain" marks a step that links other observations together. Call sites
// that leave Type empty keep the generic span/generation type.
//
// https://langfuse.com/docs/observability/features/observation-types
const (
	ObsTypeSpan       = obsTypeSpan
	ObsTypeGeneration = obsTypeGeneration
	ObsTypeEvent      = "event"
	ObsTypeAgent      = "agent"
	ObsTypeTool       = "tool"
	ObsTypeChain      = "chain"
	ObsTypeRetriever  = "retriever"
	ObsTypeEmbedding  = "embedding"
	ObsTypeEvaluator  = "evaluator"
)

// spanObsType resolves a requested observation type for a Span. Agent, tool,
// chain, retriever and evaluator are the non-generation types Langfuse
// understands; anything else (including empty) falls back to "span" so a typo
// can never degrade the trace into an unknown observation type.
func spanObsType(requested string) string {
	switch requested {
	case ObsTypeAgent, ObsTypeTool, ObsTypeChain, ObsTypeRetriever, ObsTypeEvaluator:
		return requested
	}
	return obsTypeSpan
}

// generationObsType resolves a requested observation type for a Generation.
// Embedding and evaluator are the generation-bearing specific types; all other
// values fall back to "generation".
func generationObsType(requested string) string {
	switch requested {
	case ObsTypeEmbedding, ObsTypeEvaluator:
		return requested
	}
	return obsTypeGeneration
}

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
