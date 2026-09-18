package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
)

const (
	DesensitizationEngineBuiltin  = "builtin"
	DesensitizationEnginePresidio = "presidio"
	DesensitizationEngineLLM      = "llm"

	DesensitizationMaskReplace = "replace"
	DesensitizationMaskPartial = "partial"

	DesensitizationEntityCNIDCard   = "cn_id_card"
	DesensitizationEntityCNMobile   = "cn_mobile"
	DesensitizationEntityCNLandline = "cn_landline"
	DesensitizationEntityCNBankCard = "cn_bank_card"
	DesensitizationEntityCNUSCC     = "cn_uscc"
	DesensitizationEntityCNPlate    = "cn_plate"
	DesensitizationEntityEmail      = "email"
)

// CloudParserEngineNames must not run when desensitization is enabled —
// the original file would leave the trust boundary before any text masking.
var CloudParserEngineNames = map[string]struct{}{
	"weknoracloud":       {},
	"mineru_cloud":       {},
	"paddleocr_vl_cloud": {},
}

// KnownDesensitizationEntityTypes is the builtin preset list for mainland China.
var KnownDesensitizationEntityTypes = []string{
	DesensitizationEntityCNIDCard,
	DesensitizationEntityCNMobile,
	DesensitizationEntityCNLandline,
	DesensitizationEntityCNBankCard,
	DesensitizationEntityCNUSCC,
	DesensitizationEntityCNPlate,
	DesensitizationEntityEmail,
}

const (
	maxDesensitizationRules        = 50
	maxDesensitizationPatternRunes = 500
)

// DesensitizationConfig is opt-in per knowledge base. Disabled by default so
// existing deployments do not rewrite ingested text.
type DesensitizationConfig struct {
	Enabled     bool                  `yaml:"enabled" json:"enabled"`
	Engine      string                `yaml:"engine,omitempty" json:"engine,omitempty"`
	MaskStyle   string                `yaml:"mask_style,omitempty" json:"mask_style,omitempty"`
	EntityTypes []string              `yaml:"entity_types,omitempty" json:"entity_types,omitempty"`
	Rules       []DesensitizationRule `yaml:"rules,omitempty" json:"rules,omitempty"`
	LLMModelID  string                `yaml:"llm_model_id,omitempty" json:"llm_model_id,omitempty"`
}

// DesensitizationRule is a user-supplied regexp applied after preset types.
type DesensitizationRule struct {
	Name        string `yaml:"name" json:"name"`
	Pattern     string `yaml:"pattern" json:"pattern"`
	Replacement string `yaml:"replacement,omitempty" json:"replacement,omitempty"`
}

// IsEnabled reports whether masking should run. A nil receiver is off.
func (c *DesensitizationConfig) IsEnabled() bool {
	return c != nil && c.Enabled
}

// Value serializes the configuration for database storage.
func (c DesensitizationConfig) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan deserializes the configuration from a database value.
func (c *DesensitizationConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if s, ok := value.(string); ok {
			b = []byte(s)
		} else {
			return nil
		}
	}
	return json.Unmarshal(b, c)
}

// Normalize applies defaults and drops unknown entity types / empty rules.
func (c *DesensitizationConfig) Normalize() {
	if c == nil {
		return
	}
	if c.Engine == "" {
		c.Engine = DesensitizationEngineBuiltin
	}
	if c.MaskStyle == "" {
		c.MaskStyle = DesensitizationMaskReplace
	}
	if c.MaskStyle != DesensitizationMaskReplace && c.MaskStyle != DesensitizationMaskPartial {
		c.MaskStyle = DesensitizationMaskReplace
	}
	if c.Engine != DesensitizationEngineBuiltin &&
		c.Engine != DesensitizationEnginePresidio &&
		c.Engine != DesensitizationEngineLLM {
		c.Engine = DesensitizationEngineBuiltin
	}

	allowed := make(map[string]struct{}, len(KnownDesensitizationEntityTypes))
	for _, t := range KnownDesensitizationEntityTypes {
		allowed[t] = struct{}{}
	}
	filtered := make([]string, 0, len(c.EntityTypes))
	seen := make(map[string]struct{}, len(c.EntityTypes))
	for _, raw := range c.EntityTypes {
		t := strings.TrimSpace(raw)
		if _, ok := allowed[t]; !ok {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		filtered = append(filtered, t)
	}
	c.EntityTypes = filtered

	if len(c.Rules) > maxDesensitizationRules {
		c.Rules = c.Rules[:maxDesensitizationRules]
	}
	trimmed := make([]DesensitizationRule, 0, len(c.Rules))
	for _, rule := range c.Rules {
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Pattern = strings.TrimSpace(rule.Pattern)
		rule.Replacement = strings.TrimSpace(rule.Replacement)
		if rule.Pattern == "" {
			continue
		}
		if runeLen(rule.Pattern) > maxDesensitizationPatternRunes {
			continue
		}
		if rule.Name == "" {
			rule.Name = "custom"
		}
		trimmed = append(trimmed, rule)
	}
	c.Rules = trimmed
	c.LLMModelID = strings.TrimSpace(c.LLMModelID)
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// ParserEngineIsCloud reports whether a parser engine name sends the original
// file to a hosted third-party service.
func ParserEngineIsCloud(name string) bool {
	_, ok := CloudParserEngineNames[strings.TrimSpace(name)]
	return ok
}

// HasCloudParserEngine reports whether any configured parser rule uses a
// hosted cloud engine.
func (c ChunkingConfig) HasCloudParserEngine() bool {
	for _, rule := range c.ParserEngineRules {
		if ParserEngineIsCloud(rule.Engine) {
			return true
		}
	}
	return false
}
