package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	SuggestionPlacementAfterAnswer = "after_answer"

	SuggestionStatusGenerating = "generating"
	SuggestionStatusReady      = "ready"
	SuggestionStatusSuppressed = "suppressed"
	SuggestionStatusFailed     = "failed"

	SuggestionEventImpression = "impression"
	SuggestionEventClick      = "click"
	SuggestionEventDismiss    = "dismiss"
	SuggestionEventRegenerate = "regenerate"
)

// SuggestionAttribution is carried by the next user message after a click, so
// analytics can distinguish a click from a question that was actually sent.
type SuggestionAttribution struct {
	SuggestionSetID string `json:"suggestion_set_id"`
	QuestionID      string `json:"question_id"`
}

// SuggestionItem is a stable, attributable question rendered to an end user.
type SuggestionItem struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Category         string   `json:"category,omitempty"`
	Source           string   `json:"source"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
}

type SuggestionItems []SuggestionItem

func (s SuggestionItems) Value() (driver.Value, error) {
	if s == nil {
		s = SuggestionItems{}
	}
	return json.Marshal(s)
}

func (s *SuggestionItems) Scan(value interface{}) error {
	if value == nil {
		*s = SuggestionItems{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*s = SuggestionItems{}
		return nil
	}
	return json.Unmarshal(b, s)
}

// MessageSuggestionSet is the durable generation/cache record for one
// assistant message and one effective agent configuration.
type MessageSuggestionSet struct {
	ID                 string          `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64          `json:"tenant_id" gorm:"not null;index"`
	SessionID          string          `json:"session_id" gorm:"type:varchar(36);not null;index"`
	AssistantMessageID string          `json:"assistant_message_id" gorm:"type:varchar(36);not null;index"`
	AgentID            string          `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	AgentTenantID      uint64          `json:"-" gorm:"not null;default:0"`
	Placement          string          `json:"placement" gorm:"type:varchar(32);not null"`
	ConfigHash         string          `json:"config_hash" gorm:"type:varchar(64);not null"`
	Locale             string          `json:"locale" gorm:"type:varchar(16);not null;default:''"`
	Status             string          `json:"status" gorm:"type:varchar(16);not null;index"`
	AllowRegenerate    bool            `json:"allow_regenerate" gorm:"not null;default:false"`
	SuppressionReason  string          `json:"suppression_reason,omitempty" gorm:"type:varchar(64);not null;default:''"`
	Questions          SuggestionItems `json:"questions" gorm:"type:jsonb;not null"`
	ModelID            string          `json:"model_id,omitempty" gorm:"type:varchar(64);not null;default:''"`
	PromptTokens       int             `json:"prompt_tokens,omitempty" gorm:"not null;default:0"`
	CompletionTokens   int             `json:"completion_tokens,omitempty" gorm:"not null;default:0"`
	LatencyMs          int64           `json:"latency_ms,omitempty" gorm:"not null;default:0"`
	ErrorCode          string          `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	LeaseUntil         *time.Time      `json:"-"`
	GeneratedAt        *time.Time      `json:"generated_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func (MessageSuggestionSet) TableName() string { return "message_suggestion_sets" }

func (s *MessageSuggestionSet) BeforeCreate(_ *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Questions == nil {
		s.Questions = SuggestionItems{}
	}
	return nil
}

// MessageSuggestionEvent stores product analytics separately from the
// security audit log. It references question IDs rather than copying text.
type MessageSuggestionEvent struct {
	ID              uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	SuggestionSetID string    `json:"suggestion_set_id" gorm:"type:varchar(36);not null;index"`
	QuestionID      string    `json:"question_id,omitempty" gorm:"type:varchar(64);not null;default:''"`
	EventType       string    `json:"event_type" gorm:"type:varchar(32);not null;index"`
	ActorID         string    `json:"-" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt       time.Time `json:"created_at" gorm:"index"`
}

func (MessageSuggestionEvent) TableName() string { return "message_suggestion_events" }
