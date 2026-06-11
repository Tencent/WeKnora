package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EmbedChannel publishes a knowledge-base chat surface for external websites.
type EmbedChannel struct {
	ID                string         `json:"id"                  gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64         `json:"tenant_id"           gorm:"not null;index:idx_embed_channels_tenant"`
	KnowledgeBaseID   string         `json:"knowledge_base_id"   gorm:"type:varchar(36);not null;index:idx_embed_channels_kb"`
	AgentID           string         `json:"agent_id"            gorm:"type:varchar(36);not null;default:'builtin-quick-answer'"`
	Name              string         `json:"name"                gorm:"type:varchar(255);not null;default:''"`
	Enabled           bool           `json:"enabled"             gorm:"not null;default:true"`
	PublishToken      string         `json:"-"                   gorm:"type:varchar(64);not null;default:''"`
	AllowedOrigins    JSON           `json:"allowed_origins"     gorm:"type:jsonb;not null;default:'[]'"`
	WelcomeMessage    string         `json:"welcome_message"     gorm:"type:text;not null;default:''"`
	RateLimitPerMinute int           `json:"rate_limit_per_minute" gorm:"not null;default:30"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"deleted_at"          gorm:"index"`
}

func (EmbedChannel) TableName() string { return "embed_channels" }

func (ch *EmbedChannel) BeforeCreate(tx *gorm.DB) error {
	if ch.ID == "" {
		ch.ID = uuid.New().String()
	}
	if ch.AgentID == "" {
		ch.AgentID = BuiltinQuickAnswerID
	}
	if ch.RateLimitPerMinute <= 0 {
		ch.RateLimitPerMinute = 30
	}
	return nil
}

// AllowedOriginsList decodes the JSON array of origin patterns.
func (ch *EmbedChannel) AllowedOriginsList() []string {
	if len(ch.AllowedOrigins) == 0 {
		return nil
	}
	var origins []string
	if err := json.Unmarshal(ch.AllowedOrigins, &origins); err != nil {
		return nil
	}
	return origins
}

// EmbedChannelPublicConfig is returned to anonymous embed clients (no secrets).
type EmbedChannelPublicConfig struct {
	ChannelID       string   `json:"channel_id"`
	Name            string   `json:"name"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	AgentID         string   `json:"agent_id"`
	WelcomeMessage  string   `json:"welcome_message"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
}

// EmbedSessionMarkerPrefix tags sessions created through an embed channel.
const EmbedSessionMarkerPrefix = "embed_channel:"
