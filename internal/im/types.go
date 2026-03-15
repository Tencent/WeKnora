package im

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChannelSession maps an IM channel (user+chat combination) to a WeKnora session.
// This allows the IM integration to maintain conversation continuity.
type ChannelSession struct {
	ID        string         `json:"id"         gorm:"type:varchar(36);primaryKey;default:uuid_generate_v4()"`
	Platform  string         `json:"platform"   gorm:"type:varchar(20);not null"`
	UserID    string         `json:"user_id"    gorm:"type:varchar(128);not null"`
	ChatID    string         `json:"chat_id"    gorm:"type:varchar(128);not null;default:''"`
	SessionID string         `json:"session_id" gorm:"type:varchar(36);not null;index"`
	TenantID  uint64         `json:"tenant_id"  gorm:"not null;index"`
	AgentID   string         `json:"agent_id"   gorm:"type:varchar(36);default:''"`
	Status    string         `json:"status"     gorm:"type:varchar(20);not null;default:'active'"`
	Metadata  types.JSON     `json:"metadata"   gorm:"type:jsonb;default:'{}'"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
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
