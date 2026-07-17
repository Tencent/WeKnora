package langfuse

import (
	"time"
)

// TokenUsage captures the input/output/total token counts reported by the
// underlying model, in Langfuse's canonical schema. Set as the value of the
// langfuse.observation.usage_details span attribute (JSON-serialized).
type TokenUsage struct {
	Input  int    `json:"input,omitempty"`
	Output int    `json:"output,omitempty"`
	Total  int    `json:"total,omitempty"`
	Unit   string `json:"unit,omitempty"`
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

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
