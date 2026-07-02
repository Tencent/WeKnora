package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	VLMImageResultTypeOCR     = "ocr"
	VLMImageResultTypeCaption = "caption"

	VLMOCRPromptVersion       = "ocr_prompt_v1"
	VLMCaptionPromptVersion   = "caption_prompt_v1"
	VLMOCRCanonicalizerV1     = "ocr_sanitize_v1"
	VLMCaptionCanonicalizerV1 = "caption_trim_v1"
)

type VLMImageResultCache struct {
	ID                         string    `json:"id"                           gorm:"type:varchar(36);primaryKey"`
	TenantID                   uint64    `json:"tenant_id"                    gorm:"not null;index:idx_vlm_image_cache_tenant_key,unique"`
	CacheKey                   string    `json:"cache_key"                    gorm:"type:varchar(64);not null;index:idx_vlm_image_cache_tenant_key,unique"`
	ImageHash                  string    `json:"image_hash"                   gorm:"type:varchar(64);not null;index"`
	ModelFingerprint           string    `json:"model_fingerprint"            gorm:"type:varchar(64);not null;index"`
	ResultType                 string    `json:"result_type"                  gorm:"type:varchar(32);not null;index"`
	PromptVersion              string    `json:"prompt_version"               gorm:"type:varchar(64);not null"`
	PromptHash                 string    `json:"prompt_hash"                  gorm:"type:varchar(64);not null"`
	ResultCanonicalizerVersion string    `json:"result_canonicalizer_version" gorm:"type:varchar(64);not null"`
	Content                    string    `json:"content"                      gorm:"type:text;not null"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

func (VLMImageResultCache) TableName() string {
	return "vlm_image_result_cache"
}

func (c *VLMImageResultCache) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return nil
}

type VLMFingerprintPayload struct {
	ConfigSource    string            `json:"config_source"`
	ModelID         string            `json:"model_id,omitempty"`
	ModelName       string            `json:"model_name,omitempty"`
	RemoteModelName string            `json:"remote_model_name,omitempty"`
	Source          string            `json:"source,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	InterfaceType   string            `json:"interface_type,omitempty"`
	BaseURL         string            `json:"base_url,omitempty"`
	ExtraConfig     map[string]string `json:"extra_config,omitempty"`
	CustomHeaders   map[string]string `json:"custom_headers,omitempty"`
}

type VLMImageResultCacheKeyPayload struct {
	ImageHash                  string `json:"image_hash"`
	ModelFingerprint           string `json:"model_fingerprint"`
	ResultType                 string `json:"result_type"`
	PromptVersion              string `json:"prompt_version"`
	PromptHash                 string `json:"prompt_hash"`
	ResultCanonicalizerVersion string `json:"result_canonicalizer_version"`
}

func BuildVLMFingerprintPayloadFromModel(model *Model) VLMFingerprintPayload {
	if model == nil {
		return VLMFingerprintPayload{ConfigSource: "model"}
	}
	params := model.Parameters
	return VLMFingerprintPayload{
		ConfigSource:    "model",
		ModelID:         model.ID,
		ModelName:       model.Name,
		RemoteModelName: strings.TrimSpace(params.ExtraConfig["remote_model_name"]),
		Source:          string(model.Source),
		Provider:        strings.TrimSpace(params.Provider),
		InterfaceType:   strings.TrimSpace(params.InterfaceType),
		BaseURL:         strings.TrimSpace(params.BaseURL),
		ExtraConfig:     sanitizeStringMapForFingerprint(params.ExtraConfig),
		CustomHeaders:   sanitizeStringMapForFingerprint(params.CustomHeaders),
	}
}

func BuildVLMFingerprintPayloadFromConfig(cfg VLMConfig) VLMFingerprintPayload {
	ifType := strings.TrimSpace(cfg.InterfaceType)
	if ifType == "" {
		ifType = "openai"
	}
	return VLMFingerprintPayload{
		ConfigSource:  "legacy_inline",
		ModelName:     strings.TrimSpace(cfg.ModelName),
		Provider:      "",
		InterfaceType: ifType,
		BaseURL:       strings.TrimSpace(cfg.BaseURL),
	}
}

func (p VLMFingerprintPayload) Fingerprint() (string, error) {
	b, err := canonicalJSON(p)
	if err != nil {
		return "", err
	}
	return SHA256Hex(b), nil
}

func BuildVLMImageResultCacheKey(payload VLMImageResultCacheKeyPayload) (string, error) {
	b, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	return SHA256Hex(b), nil
}

func HashVLMImageBytes(imgBytes []byte) string {
	return SHA256Hex(imgBytes)
}

func HashVLMPrompt(prompt string) string {
	return SHA256Hex([]byte(prompt))
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func sanitizeStringMapForFingerprint(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" || isSensitiveFingerprintKey(key) {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSensitiveFingerprintKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "authorization", "cookie", "api_key", "app_secret", "x_api_key":
		return true
	}
	return strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "key")
}
