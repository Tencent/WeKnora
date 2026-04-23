package langfuse

import (
	"time"
)

// Langfuse ingestion API event envelope.
// https://api.reference.langfuse.com/#tag/ingestion
type ingestionEvent struct {
	ID        string      `json:"id"`
	Timestamp string      `json:"timestamp"`
	Type      string      `json:"type"`
	Body      interface{} `json:"body"`
}

// TokenUsage captures the input/output/total token counts reported by the
// underlying model, in Langfuse's canonical schema.
type TokenUsage struct {
	Input  int    `json:"input,omitempty"`
	Output int    `json:"output,omitempty"`
	Total  int    `json:"total,omitempty"`
	Unit   string `json:"unit,omitempty"`
}

// traceBody mirrors the /api/public/ingestion trace-create body.
type traceBody struct {
	ID          string                 `json:"id"`
	Timestamp   string                 `json:"timestamp,omitempty"`
	Name        string                 `json:"name,omitempty"`
	UserID      string                 `json:"userId,omitempty"`
	SessionID   string                 `json:"sessionId,omitempty"`
	Release     string                 `json:"release,omitempty"`
	Environment string                 `json:"environment,omitempty"`
	Input       interface{}            `json:"input,omitempty"`
	Output      interface{}            `json:"output,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Public      bool                   `json:"public,omitempty"`
}

// observationBody is shared between span-create / generation-create /
// span-update / generation-update events (different fields are populated
// depending on the event type).
type observationBody struct {
	ID                  string                 `json:"id,omitempty"`
	TraceID             string                 `json:"traceId,omitempty"`
	ParentObservationID string                 `json:"parentObservationId,omitempty"`
	Type                string                 `json:"type,omitempty"` // SPAN, GENERATION, EVENT
	Name                string                 `json:"name,omitempty"`
	StartTime           string                 `json:"startTime,omitempty"`
	EndTime             string                 `json:"endTime,omitempty"`
	Input               interface{}            `json:"input,omitempty"`
	Output              interface{}            `json:"output,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	Level               string                 `json:"level,omitempty"`         // DEFAULT | ERROR | WARNING
	StatusMessage       string                 `json:"statusMessage,omitempty"` // free-form

	// Generation-specific fields
	Model           string                 `json:"model,omitempty"`
	ModelParameters map[string]interface{} `json:"modelParameters,omitempty"`
	Usage           *TokenUsage            `json:"usage,omitempty"`
	CompletionStart string                 `json:"completionStartTime,omitempty"`
}

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
