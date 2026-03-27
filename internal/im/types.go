package im

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IMChannel represents an IM channel configuration stored in the database.
// Each channel binds to an agent and contains platform-specific credentials.
type IMChannel struct {
	ID              string         `json:"id"          gorm:"type:varchar(36);primaryKey;default:uuid_generate_v4()"`
	TenantID        uint64         `json:"tenant_id"   gorm:"not null;index:idx_im_channels_tenant"`
	AgentID         string         `json:"agent_id"    gorm:"type:varchar(36);not null;index:idx_im_channels_agent"`
	Platform        string         `json:"platform"    gorm:"type:varchar(20);not null"`
	Name            string         `json:"name"        gorm:"type:varchar(255);not null;default:''"`
	Enabled         bool           `json:"enabled"     gorm:"not null;default:true"`
	Mode            string         `json:"mode"        gorm:"type:varchar(20);not null;default:'websocket'"`
	OutputMode      string         `json:"output_mode"       gorm:"type:varchar(20);not null;default:'stream'"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);default:''"`
	BotIdentity     string         `json:"bot_identity"      gorm:"type:varchar(255);not null;default:'';uniqueIndex:idx_im_channels_bot_identity,where:deleted_at IS NULL AND bot_identity != ''"`
	SessionMode     string         `json:"session_mode"      gorm:"type:varchar(20);not null;default:'user'"`
	Credentials     types.JSON     `json:"credentials"       gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at"  gorm:"index"`
}

func (IMChannel) TableName() string {
	return "im_channels"
}

func (ch *IMChannel) BeforeCreate(tx *gorm.DB) error {
	if ch.ID == "" {
		ch.ID = uuid.New().String()
	}
	if ch.Mode == "" {
		if ch.Platform == "mattermost" {
			ch.Mode = "webhook"
		} else {
			ch.Mode = "websocket"
		}
	}
	if ch.OutputMode == "" {
		ch.OutputMode = "stream"
	}
	if ch.SessionMode == "" {
		ch.SessionMode = string(SessionModeUser)
	}
	if err := ch.validateSessionMode(); err != nil {
		return err
	}
	ch.BotIdentity = ch.computeBotIdentity()
	return nil
}

// BeforeSave ensures bot_identity is recomputed and session_mode is validated
// on every save (create + update).
func (ch *IMChannel) BeforeSave(tx *gorm.DB) error {
	if ch.SessionMode == "" {
		ch.SessionMode = string(SessionModeUser)
	}
	if err := ch.validateSessionMode(); err != nil {
		return err
	}
	ch.BotIdentity = ch.computeBotIdentity()
	return nil
}

// validateSessionMode checks that SessionMode holds a supported value.
func (ch *IMChannel) validateSessionMode() error {
	switch SessionMode(ch.SessionMode) {
	case SessionModeUser, SessionModeThread:
		return nil
	default:
		return fmt.Errorf("invalid session_mode: %s", ch.SessionMode)
	}
}

// computeBotIdentity derives a unique bot identity string from the channel's
// platform, mode, and credentials. Returns "" if no identity can be extracted.
func (ch *IMChannel) computeBotIdentity() string {
	creds := make(map[string]interface{})
	if err := json.Unmarshal([]byte(ch.Credentials), &creds); err != nil {
		return ""
	}

	str := func(key string) string {
		if v, ok := creds[key]; ok {
			switch val := v.(type) {
			case string:
				return val
			case float64:
				return fmt.Sprintf("%.0f", val)
			}
		}
		return ""
	}

	switch ch.Platform {
	case "wecom":
		switch ch.Mode {
		case "websocket":
			if botID := str("bot_id"); botID != "" {
				return "wecom:ws:" + botID
			}
		case "webhook":
			corpID := str("corp_id")
			agentID := str("corp_agent_id")
			if corpID != "" && agentID != "" {
				return "wecom:wh:" + corpID + ":" + agentID
			}
		}
	case "feishu":
		if appID := str("app_id"); appID != "" {
			return "feishu:" + appID
		}
	case "telegram":
		if botToken := str("bot_token"); botToken != "" {
			// Use the bot ID part (before the colon) as identity.
			if idx := strings.Index(botToken, ":"); idx > 0 {
				return "telegram:" + botToken[:idx]
			}
			return "telegram:" + botToken
		}
	case "dingtalk":
		if clientID := str("client_id"); clientID != "" {
			return "dingtalk:" + clientID
		}
	case "mattermost":
		if tok := str("outgoing_token"); tok != "" {
			return "mattermost:wh:" + tok
		}
	}
	return ""
}

// ChannelSession maps an IM channel (user+chat combination) to a WeKnora session.
// This allows the IM integration to maintain conversation continuity.
type ChannelSession struct {
	ID          string         `json:"id"            gorm:"type:varchar(36);primaryKey;default:uuid_generate_v4()"`
	Platform    string         `json:"platform"      gorm:"type:varchar(20);not null"`
	UserID      string         `json:"user_id"       gorm:"type:varchar(128);not null"`
	ChatID      string         `json:"chat_id"       gorm:"type:varchar(128);not null;default:''"`
	ThreadID    string         `json:"thread_id"     gorm:"type:varchar(128);not null;default:''"`
	SessionID   string         `json:"session_id"    gorm:"type:varchar(36);not null;index"`
	TenantID    uint64         `json:"tenant_id"     gorm:"not null;index"`
	AgentID     string         `json:"agent_id"      gorm:"type:varchar(36);default:''"`
	IMChannelID string         `json:"im_channel_id" gorm:"type:varchar(36);default:''"`
	Status      string         `json:"status"        gorm:"type:varchar(20);not null;default:'active'"`
	Metadata    types.JSON     `json:"metadata"      gorm:"type:jsonb;default:'{}'"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at"    gorm:"index"`
}

func (ChannelSession) TableName() string {
	return "im_channel_sessions"
}

func (cs *ChannelSession) BeforeCreate(tx *gorm.DB) error {
	if cs.ID == "" {
		cs.ID = uuid.New().String()
	}
	if cs.Status == "" {
		cs.Status = "active"
	}
	return nil
}
