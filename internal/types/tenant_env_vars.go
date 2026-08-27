package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrEnvVarNotFound reports that a principal has no own value stored for the
// requested variable. Callers turn it into a 404 rather than a failure.
var ErrEnvVarNotFound = errors.New("env var not found")

// SkillEnvVar is one environment variable a skill needs, as declared by the
// installer agent, optionally carrying the workspace-wide value an admin
// supplied. Everything except Value stays plaintext in the column so the UI can
// render the declaration even when SYSTEM_AES_KEY is unavailable.
type SkillEnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Value is the workspace-wide admin value. It is AES-GCM encrypted at rest
	// by SkillEnvVars.Value and decrypted by SkillEnvVars.Scan. json:"-" makes
	// it unserialisable by construction: the column round-trip goes through
	// skillEnvVarRow, so no response body can carry it by forgetting to strip
	// it in a DTO.
	Value string `json:"-"`
}

// skillEnvVarRow is the on-column shape of one declaration. It exists only so
// SkillEnvVars.Value and SkillEnvVars.Scan can persist the encrypted value
// while SkillEnvVar itself stays unserialisable.
type skillEnvVarRow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Value       string `json:"value,omitempty"`
}

// SkillEnvVars is the whole declaration list, stored as one JSON column.
// It follows TenantSandboxConfig's driver.Valuer/sql.Scanner pattern rather
// than GORM hooks because the encrypted field lives inside a JSON document.
type SkillEnvVars []SkillEnvVar

// Get returns the declaration for name, if the skill declares it.
func (v SkillEnvVars) Get(name string) (SkillEnvVar, bool) {
	for _, e := range v {
		if e.Name == name {
			return e, true
		}
	}
	return SkillEnvVar{}, false
}

// Value implements driver.Valuer. The receiver is never mutated: the caller
// holds plaintext, so encryption happens on a copy of the slice.
func (v SkillEnvVars) Value() (driver.Value, error) {
	if v == nil {
		return nil, nil
	}
	rows := make([]skillEnvVarRow, len(v))
	for i, entry := range v {
		encrypted, err := encryptEnvValue(entry.Value)
		if err != nil {
			return nil, fmt.Errorf("encrypt skill_env_vars.%s: %w", entry.Name, err)
		}
		rows[i] = skillEnvVarRow{
			Name:        entry.Name,
			Description: entry.Description,
			Required:    entry.Required,
			Value:       encrypted,
		}
	}
	return json.Marshal(rows)
}

// Scan implements sql.Scanner. A value that cannot be decrypted (missing or
// rotated SYSTEM_AES_KEY) is reported as unset rather than failing the load,
// matching TenantSandboxConfig.Scan: the skill must stay listable and editable
// even when its stored credential became unreadable.
func (v *SkillEnvVars) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch raw := value.(type) {
	case []byte:
		b = raw
	case string:
		b = []byte(raw)
	default:
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	var rows []skillEnvVarRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return err
	}
	if rows == nil {
		*v = nil
		return nil
	}
	out := make(SkillEnvVars, len(rows))
	for i, row := range rows {
		out[i] = SkillEnvVar{
			Name:        row.Name,
			Description: row.Description,
			Required:    row.Required,
			Value:       decryptEnvValue("skill_env_vars", row.Name, row.Value),
		}
	}
	*v = out
	return nil
}

// TenantUserEnvVar is one principal's own environment variable.
//
// An empty SkillID means the variable belongs to the whole sandbox config and
// is injected into every execution on it. A non-empty SkillID scopes it to that
// skill's declaration, injected only when a tool names the skill. The storage is
// the same either way; only the load timing differs.
//
// It is keyed by principal rather than user id because the IM path puts a
// synthetic "system-<tenantID>" account in the user id, which would make every
// IM user of a workspace share a single credential.
type TenantUserEnvVar struct {
	ID       string `gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `gorm:"uniqueIndex:uq_user_env_var,priority:1;index:idx_user_env_var_skill,priority:1;index:idx_user_env_var_config,priority:1"`

	PrincipalType string `gorm:"type:varchar(32);not null;uniqueIndex:uq_user_env_var,priority:2"`
	PrincipalID   string `gorm:"type:varchar(512);not null;uniqueIndex:uq_user_env_var,priority:3"`

	SandboxConfigID string `gorm:"type:varchar(36);not null;uniqueIndex:uq_user_env_var,priority:4;index:idx_user_env_var_config,priority:2"`
	SkillID         string `gorm:"type:varchar(36);not null;uniqueIndex:uq_user_env_var,priority:5;index:idx_user_env_var_skill,priority:2"`
	Name            string `gorm:"type:varchar(255);not null;uniqueIndex:uq_user_env_var,priority:6"`

	// Value is AES-GCM encrypted at rest by BeforeSave and decrypted by
	// AfterFind. json:"-" makes it unserialisable by construction, so no
	// endpoint can leak it by forgetting to strip it.
	Value string `json:"-" gorm:"type:text"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (TenantUserEnvVar) TableName() string { return "tenant_user_env_vars" }

func (e *TenantUserEnvVar) BeforeCreate(tx *gorm.DB) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	return e.encryptValue()
}

func (e *TenantUserEnvVar) BeforeSave(tx *gorm.DB) error {
	return e.encryptValue()
}

// encryptValue is idempotent: EncryptAESGCM returns an already-prefixed string
// unchanged, so the BeforeSave/BeforeCreate pair GORM runs on a create does not
// encrypt twice.
func (e *TenantUserEnvVar) encryptValue() error {
	encrypted, err := encryptEnvValue(e.Value)
	if err != nil {
		return fmt.Errorf("encrypt tenant_user_env_vars.%s: %w", e.Name, err)
	}
	e.Value = encrypted
	return nil
}

func (e *TenantUserEnvVar) AfterFind(tx *gorm.DB) error {
	e.Value = decryptEnvValue("tenant_user_env_vars", e.Name, e.Value)
	return nil
}

// encryptEnvValue returns the ciphertext, or the input unchanged when there is
// no key configured to encrypt with — the same deployment-wide degradation
// every other secret column accepts.
//
// A key that exists but fails to encrypt is different: it means something is
// wrong with the cipher, and writing the plaintext into a queryable JSONB
// column instead would turn that into a silent leak. The error aborts the
// write, matching TenantAPIKey.BeforeSave.
func encryptEnvValue(value string) (string, error) {
	key := utils.GetAESKey()
	if key == nil || value == "" {
		return value, nil
	}
	encrypted, err := utils.EncryptAESGCM(value, key)
	if err != nil {
		return "", err
	}
	return encrypted, nil
}

// decryptEnvValue reports an unreadable secret as unset rather than failing the
// load, so a row whose key was rotated away stays listable and the member can
// see they have to enter it again.
func decryptEnvValue(table, name, stored string) string {
	if stored == "" {
		return ""
	}
	if plain, ok := utils.DecryptStoredSecretLenient(stored); ok {
		return plain
	}
	log.Printf("[crypto] %s.%s: decrypt failed "+
		"(SYSTEM_AES_KEY missing/rotated?), treating as unset", table, name)
	return ""
}
