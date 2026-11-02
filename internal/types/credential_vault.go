package types

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	CredentialKindLogin        = "login"
	CredentialVaultMaxItems    = 500
	CredentialVaultMaxTitleLen = 100
)

var (
	ErrCredentialConflict = errors.New("a saved credential for this site and username already exists")
	ErrCredentialNotFound = errors.New("saved credential not found")
)

// CredentialVaultItem is one saved login of one user in one workspace. The
// password lives only in SecretCiphertext, sealed together with the row's ID
// and owner so a ciphertext copied onto another row fails to open.
type CredentialVaultItem struct {
	ID               string         `json:"id"            gorm:"type:varchar(36);primaryKey"`
	TenantID         uint64         `json:"-"             gorm:"not null"`
	UserID           string         `json:"-"             gorm:"type:varchar(128);not null"`
	Kind             string         `json:"kind"          gorm:"type:varchar(16);not null;default:'login'"`
	Title            string         `json:"title"         gorm:"type:varchar(200);not null"`
	Site             string         `json:"site"          gorm:"type:varchar(255);not null"`
	Domains          StringArray    `json:"domains"       gorm:"type:text;not null"`
	Username         string         `json:"username"      gorm:"type:varchar(256);not null"`
	SecretCiphertext string         `json:"-"             gorm:"type:text;not null"`
	LastUsedAt       *time.Time     `json:"last_used_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `json:"-"             gorm:"index"`
}

// TableName pins the table name.
func (CredentialVaultItem) TableName() string { return "credential_vault_items" }
