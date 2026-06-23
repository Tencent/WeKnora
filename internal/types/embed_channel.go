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
	ID                     string         `json:"id"                  gorm:"type:varchar(36);primaryKey"`
	TenantID               uint64         `json:"tenant_id"           gorm:"not null;index:idx_embed_channels_tenant"`
	AgentID                string         `json:"agent_id"            gorm:"type:varchar(36);not null;index:idx_embed_channels_agent;default:'builtin-quick-answer'"`
	Name                   string         `json:"name"                gorm:"type:varchar(255);not null;default:''"`
	Enabled                bool           `json:"enabled"             gorm:"not null;default:true"`
	PublishToken           string         `json:"-"                   gorm:"type:varchar(64);not null;default:''"`
	AllowedOrigins         JSON           `json:"allowed_origins"     gorm:"type:jsonb;not null;default:'[]'"`
	WelcomeMessage         string         `json:"welcome_message"      gorm:"type:text;not null;default:''"`
	RateLimitPerMinute     int            `json:"rate_limit_per_minute" gorm:"not null;default:30"`
	RateLimitPerDay        int            `json:"rate_limit_per_day"    gorm:"not null;default:10000"`
	PrimaryColor           string         `json:"primary_color"        gorm:"type:varchar(32);not null;default:''"`
	PageTitle              string         `json:"page_title"           gorm:"type:varchar(255);not null;default:''"`
	HeaderTitleMode        string         `json:"header_title_mode"         gorm:"type:varchar(32);not null;default:'channel'"`
	ShowSuggestedQuestions bool           `json:"show_suggested_questions"  gorm:"not null;default:true"`
	WidgetPosition         string         `json:"widget_position"           gorm:"type:varchar(32);not null;default:'bottom-right'"`
	AllowWebSearch         bool           `json:"allow_web_search"          gorm:"not null;default:false"`
	AllowMemory            bool           `json:"allow_memory"              gorm:"not null;default:false"`
	AllowFileUpload        bool           `json:"allow_file_upload"         gorm:"not null;default:false"`
	DefaultLocale          string         `json:"default_locale"            gorm:"type:varchar(16);not null;default:''"`
	WebhookURL             string         `json:"webhook_url"               gorm:"type:varchar(512);not null;default:''"`
	WebhookSecret          string         `json:"-"                         gorm:"type:varchar(128);not null;default:''"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	DeletedAt              gorm.DeletedAt `json:"deleted_at"          gorm:"index"`
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
	if ch.RateLimitPerDay <= 0 {
		ch.RateLimitPerDay = DefaultEmbedRateLimitPerDay
	}
	if ch.WidgetPosition == "" {
		ch.WidgetPosition = DefaultEmbedWidgetPosition
	}
	if ch.HeaderTitleMode == "" {
		ch.HeaderTitleMode = DefaultEmbedHeaderTitleMode
	}
	return nil
}

// DefaultEmbedRateLimitPerDay caps total embed requests per channel per day
// (across all client IPs), bounding cost/abuse when the publicly visible
// publish token is copied and replayed from rotating IPs.
const DefaultEmbedRateLimitPerDay = 10000

const DefaultEmbedWidgetPosition = "bottom-right"
const DefaultEmbedHeaderTitleMode = "channel"
const EmbedHeaderTitleModeSession = "session"

// NormalizeEmbedWidgetPosition returns a supported widget corner or the default.
func NormalizeEmbedWidgetPosition(position string) string {
	switch strings.TrimSpace(position) {
	case "bottom-left", "top-right", "top-left", "bottom-right":
		return strings.TrimSpace(position)
	default:
		return DefaultEmbedWidgetPosition
	}
}

// NormalizeEmbedHeaderTitleMode returns a supported header title source.
func NormalizeEmbedHeaderTitleMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case EmbedHeaderTitleModeSession:
		return EmbedHeaderTitleModeSession
	default:
		return DefaultEmbedHeaderTitleMode
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
	ChannelID              string   `json:"channel_id"`
	Name                   string   `json:"name"`
	DisplayTitle           string   `json:"display_title"`
	KnowledgeBaseIDs       []string `json:"knowledge_base_ids,omitempty"`
	AgentID                string   `json:"agent_id"`
	AgentName              string   `json:"agent_name,omitempty"`
	AgentAvatar            string   `json:"agent_avatar,omitempty"`
	WelcomeMessage         string   `json:"welcome_message"`
	PrimaryColor           string   `json:"primary_color,omitempty"`
	PageTitle              string   `json:"page_title,omitempty"`
	HeaderTitleMode        string   `json:"header_title_mode,omitempty"`
	ShowSuggestedQuestions bool     `json:"show_suggested_questions"`
	AllowedOrigins         []string `json:"allowed_origins,omitempty"`
	WidgetPosition         string   `json:"widget_position,omitempty"`
	AllowWebSearch         bool     `json:"allow_web_search"`
	AllowMemory            bool     `json:"allow_memory"`
	AllowFileUpload        bool     `json:"allow_file_upload"`
	// AgentWebSearchEnabled reflects whether the bound agent has web search configured.
	AgentWebSearchEnabled bool `json:"agent_web_search_enabled"`
	// AgentImageUploadEnabled reflects whether the bound agent supports image upload.
	AgentImageUploadEnabled bool   `json:"agent_image_upload_enabled"`
	DefaultLocale           string `json:"default_locale,omitempty"`
}

// Supported embed UI locales.
var supportedEmbedLocales = map[string]struct{}{
	"zh-CN": {},
	"en-US": {},
	"ko-KR": {},
	"ru-RU": {},
}

// NormalizeEmbedDefaultLocale returns a supported locale tag or empty string
// (meaning follow browser / host widget locale).
func NormalizeEmbedDefaultLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if _, ok := supportedEmbedLocales[locale]; ok {
		return locale
	}
	return ""
}

// EmbedSessionMarkerPrefix tags sessions created through an embed channel.
const EmbedSessionMarkerPrefix = "embed_channel:"
