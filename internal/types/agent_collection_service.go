package types

import "time"

type PrepareCollectionInput struct {
	TenantID      uint64
	AgentTenantID uint64
	AgentID       string
	UserID        string
	Config        CustomAgentConfig
}

type PreparedCollection struct {
	Profile        *AgentCollectionProfile `json:"profile,omitempty"`
	VisibleFields  []AgentCollectionField  `json:"visible_fields"`
	MissingFields  []AgentCollectionField  `json:"missing_fields"`
	CompletedCount int                     `json:"completed_count"`
	RemainingCount int                     `json:"remaining_count"`
}

type StructuredCollectionAnswerInput struct {
	PrepareCollectionInput
	FieldKey        string
	SchemaVersion   int64
	Value           any
	SourceMessageID string
}

type ExtractedCollectionValue struct {
	FieldKey   string  `json:"field_key"`
	Value      any     `json:"value"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence,omitempty"`
}

type ExtractedCollectionValuesInput struct {
	PrepareCollectionInput
	Values          []ExtractedCollectionValue
	SourceMessageID string
	SourceMessageAt *time.Time
}

type SystemAdminCollectionUpdateInput struct {
	ProfileID    string
	Config       CustomAgentConfig
	FieldKey     string
	Value        any
	ActorUserID  string
	ChangeReason string
}
