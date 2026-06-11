package types

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EmbedChannel publishes an agent chat surface for external websites.
type EmbedChannel struct {
	ID                string         `json:"id"                  gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64         `json:"tenant_id"           gorm:"not null;index:idx_embed_channels_tenant"`
	KnowledgeBaseID   string         `json:"knowledge_base_id,omitempty" gorm:"type:varchar(36);default:'';index:idx_embed_channels_kb"`
	AgentID           string         `json:"agent_id"            gorm:"type:varchar(36);not null;index:idx_embed_channels_agent;default:'builtin-quick-answer'"`
	Name              string         `json:"name"                gorm:"type:varchar(255);not null;default:''"`
	Enabled           bool           `json:"enabled"             gorm:"not null;default:true"`
	PublishToken      string         `json:"-"                   gorm:"type:varchar(64);not null;default:''"`
	AllowedOrigins    JSON           `json:"allowed_origins"     gorm:"type:jsonb;not null;default:'[]'"`
	WelcomeMessage     string         `json:"welcome_message"      gorm:"type:text;not null;default:''"`
	RateLimitPerMinute int            `json:"rate_limit_per_minute" gorm:"not null;default:30"`
	PrimaryColor       string         `json:"primary_color"        gorm:"type:varchar(32);not null;default:''"`
	PageTitle          string         `json:"page_title"           gorm:"type:varchar(255);not null;default:''"`
	WidgetPosition     string         `json:"widget_position"      gorm:"type:varchar(32);not null;default:'bottom-right'"`
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
	if ch.WidgetPosition == "" {
		ch.WidgetPosition = DefaultEmbedWidgetPosition
	}
	return nil
}

const DefaultEmbedWidgetPosition = "bottom-right"

// NormalizeEmbedWidgetPosition returns a supported widget corner or the default.
func NormalizeEmbedWidgetPosition(position string) string {
	switch strings.TrimSpace(position) {
	case "bottom-left", "top-right", "top-left", "bottom-right":
		return strings.TrimSpace(position)
	default:
		return DefaultEmbedWidgetPosition
	}
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
	ChannelID        string   `json:"channel_id"`
	Name             string   `json:"name"`
	KnowledgeBaseID  string   `json:"knowledge_base_id,omitempty"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
	AgentID          string   `json:"agent_id"`
	WelcomeMessage  string   `json:"welcome_message"`
	PrimaryColor    string   `json:"primary_color,omitempty"`
	PageTitle       string   `json:"page_title,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
}

// EmbedSessionMarkerPrefix tags sessions created through an embed channel.
const EmbedSessionMarkerPrefix = "embed_channel:"
