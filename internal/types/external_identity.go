package types

import "time"

// ExternalIdentity maps only provider-verified subjects, never display names
// or client-supplied profile data. Rows survive account soft deletion so the
// same external identity cannot silently create a replacement account.
type ExternalIdentity struct {
	Provider  string `gorm:"primaryKey;type:varchar(32)"`
	Namespace string `gorm:"primaryKey;type:varchar(128)"`
	Subject   string `gorm:"primaryKey;type:varchar(128)"`
	UserID    string `gorm:"type:varchar(36);not null;index"`
	CreatedAt time.Time
}

// WeChatLoginAttempt binds one authorization to one client and verifier.
// Only hashes of state and the client verifier are stored.
type WeChatLoginAttempt struct {
	ConfigHash string    `gorm:"type:varchar(64);not null;default:''"`
	ID         string    `gorm:"primaryKey;type:varchar(36)"`
	Client     string    `gorm:"type:varchar(16);not null"`
	AppID      string    `gorm:"type:varchar(128);not null"`
	StateHash  string    `gorm:"type:varchar(64);not null"`
	Challenge  string    `gorm:"type:varchar(43);not null"`
	ExpiresAt  time.Time `gorm:"index;not null"`
	// Result is used only by a short-lived web completion ticket. Like
	// auth_tokens, it is server-side credential storage, never a public DTO.
	Result string `gorm:"type:text;not null;default:''"`
}

// WeChatAttemptResponse contains the public parameters for a new authorization attempt.
type WeChatAttemptResponse struct {
	CallbackURL string    `json:"-"`
	AttemptID   string    `json:"attempt_id"`
	State       string    `json:"state"`
	AppID       string    `json:"app_id"`
	Scope       string    `json:"scope"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// WeChatExchangeRequest carries the provider code and the original verifier.
type WeChatExchangeRequest struct {
	AttemptID    string `json:"attempt_id"    binding:"required,max=36"`
	State        string `json:"state"         binding:"required,max=128"`
	Code         string `json:"code"          binding:"required,max=512"`
	CodeVerifier string `json:"code_verifier" binding:"required,min=43,max=128"`
}
