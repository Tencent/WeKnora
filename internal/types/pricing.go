package types

import (
	"database/sql/driver"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Decimal is an exact base-10 value persisted as NUMERIC/DECIMAL. Monetary
// code parses it into math/big.Rat; float64 is never part of the cost path.
type Decimal string

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// Validate checks that the decimal is non-negative and exactly representable.
func (d Decimal) Validate(name string) error {
	if !decimalPattern.MatchString(string(d)) {
		return fmt.Errorf("%s must be a non-negative decimal, got %q", name, d)
	}
	if _, ok := new(big.Rat).SetString(string(d)); !ok {
		return fmt.Errorf("%s is not a valid decimal", name)
	}
	return nil
}

// Value implements driver.Valuer for exact decimal values.
func (d Decimal) Value() (driver.Value, error) {
	if err := d.Validate("decimal"); err != nil {
		return nil, err
	}
	return string(d), nil
}

// Scan implements sql.Scanner for exact decimal values.
func (d *Decimal) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("cannot scan NULL into Decimal")
	}
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		raw = fmt.Sprint(v)
	}
	*d = Decimal(raw)
	return d.Validate("decimal")
}

// BillingMode identifies the metering rule used to calculate model cost.
type BillingMode string

const (
	// BillingModeChatStandardTokens prices standard chat input and output tokens.
	BillingModeChatStandardTokens BillingMode = "chat_standard_tokens"
	// BillingModeChatCacheSplitTokens prices chat tokens with cache-aware rates.
	BillingModeChatCacheSplitTokens BillingMode = "chat_cache_split_tokens"
	// BillingModeEmbeddingInputToken prices embedding input tokens.
	BillingModeEmbeddingInputToken BillingMode = "embedding_input_token"
	// BillingModeEmbeddingTotalToken prices embedding total tokens.
	BillingModeEmbeddingTotalToken BillingMode = "embedding_total_token"
	// BillingModeEmbeddingProviderInput prices embedding provider inputs.
	BillingModeEmbeddingProviderInput BillingMode = "embedding_provider_input"
	// BillingModeEmbeddingProviderRequest prices embedding provider requests.
	BillingModeEmbeddingProviderRequest BillingMode = "embedding_provider_request"
	// BillingModeRerankInputToken prices rerank input tokens.
	BillingModeRerankInputToken BillingMode = "rerank_input_token"
	// BillingModeRerankTotalToken prices rerank total tokens.
	BillingModeRerankTotalToken BillingMode = "rerank_total_token"
	// BillingModeRerankProviderPair prices rerank provider pairs.
	BillingModeRerankProviderPair BillingMode = "rerank_provider_pair"
	// BillingModeRerankProviderRequest prices rerank provider requests.
	BillingModeRerankProviderRequest BillingMode = "rerank_provider_request"
)

// ModelPricing is one versioned pricing fact for an effective model identity.
// NULL rates mean unknown/not-applicable; a non-nil Decimal("0") means free.
type ModelPricing struct {
	ID                string      `gorm:"type:varchar(36);primaryKey"`
	ResolvedProvider  string      `gorm:"type:varchar(64);not null;index:idx_model_pricing_identity_time,priority:1"`
	ResolvedModelName string      `gorm:"type:varchar(255);not null;index:idx_model_pricing_identity_time,priority:2"`
	CallType          CallType    `gorm:"type:varchar(16);not null;index:idx_model_pricing_identity_time,priority:3"`
	Currency          string      `gorm:"type:varchar(16);not null"`
	BillingMode       BillingMode `gorm:"type:varchar(64);not null"`

	InputTokenPrice      *Decimal `gorm:"type:decimal(38,18)"`
	OutputTokenPrice     *Decimal `gorm:"type:decimal(38,18)"`
	TotalTokenPrice      *Decimal `gorm:"type:decimal(38,18)"`
	CacheReadTokenPrice  *Decimal `gorm:"type:decimal(38,18)"`
	CacheWriteTokenPrice *Decimal `gorm:"type:decimal(38,18)"`
	PerRequestPrice      *Decimal `gorm:"type:decimal(38,18)"`
	PerInputPrice        *Decimal `gorm:"type:decimal(38,18)"`
	PerPairPrice         *Decimal `gorm:"type:decimal(38,18)"`
	UnitScale            Decimal  `gorm:"type:decimal(38,9);not null"`

	EffectiveFrom     time.Time `gorm:"not null;index:idx_model_pricing_identity_time,priority:4"`
	EffectiveTo       *time.Time
	PricingVersion    string  `gorm:"type:varchar(128);not null"`
	SourceName        string  `gorm:"type:varchar(128);not null"`
	SourceReference   *string `gorm:"type:text"`
	SourceRetrievedAt *time.Time
	CreatedAt         time.Time `gorm:"not null"`
}

// TableName returns the database table for pricing rules.
func (ModelPricing) TableName() string { return "model_pricing" }

// BeforeCreate assigns an identifier to a new pricing rule.
func (p *ModelPricing) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return nil
}

// Validate checks the pricing rule invariants for its billing mode.
func (p *ModelPricing) Validate() error {
	if p.ResolvedProvider == "" || p.ResolvedModelName == "" {
		return fmt.Errorf("model_pricing: resolved provider and model name are required")
	}
	if p.Currency == "" || p.PricingVersion == "" || p.SourceName == "" {
		return fmt.Errorf("model_pricing: currency, pricing_version and source_name are required")
	}
	if p.EffectiveFrom.IsZero() {
		return fmt.Errorf("model_pricing: effective_from is required")
	}
	if p.EffectiveTo != nil && !p.EffectiveTo.After(p.EffectiveFrom) {
		return fmt.Errorf("model_pricing: effective_to must be after effective_from")
	}
	if err := p.UnitScale.Validate("unit_scale"); err != nil {
		return err
	}
	unit, _ := new(big.Rat).SetString(string(p.UnitScale))
	if unit.Sign() <= 0 {
		return fmt.Errorf("model_pricing: unit_scale must be greater than zero")
	}
	for name, rate := range map[string]*Decimal{
		"input_token_price": p.InputTokenPrice, "output_token_price": p.OutputTokenPrice,
		"total_token_price": p.TotalTokenPrice, "cache_read_token_price": p.CacheReadTokenPrice,
		"cache_write_token_price": p.CacheWriteTokenPrice, "per_request_price": p.PerRequestPrice,
		"per_input_price": p.PerInputPrice, "per_pair_price": p.PerPairPrice,
	} {
		if rate != nil {
			if err := rate.Validate(name); err != nil {
				return err
			}
		}
	}
	switch p.CallType {
	case CallTypeChat:
		if p.BillingMode != BillingModeChatStandardTokens && p.BillingMode != BillingModeChatCacheSplitTokens {
			return fmt.Errorf("model_pricing: invalid chat billing_mode %q", p.BillingMode)
		}
	case CallTypeEmbedding:
		switch p.BillingMode {
		case BillingModeEmbeddingInputToken, BillingModeEmbeddingTotalToken,
			BillingModeEmbeddingProviderInput, BillingModeEmbeddingProviderRequest:
		default:
			return fmt.Errorf("model_pricing: invalid embedding billing_mode %q", p.BillingMode)
		}
	case CallTypeRerank:
		switch p.BillingMode {
		case BillingModeRerankInputToken, BillingModeRerankTotalToken,
			BillingModeRerankProviderPair, BillingModeRerankProviderRequest:
		default:
			return fmt.Errorf("model_pricing: invalid rerank billing_mode %q", p.BillingMode)
		}
	default:
		return fmt.Errorf("model_pricing: invalid call_type %q", p.CallType)
	}
	allowedRates := map[string]bool{}
	switch p.BillingMode {
	case BillingModeChatStandardTokens:
		allowedRates["input_token_price"], allowedRates["output_token_price"] = true, true
	case BillingModeChatCacheSplitTokens:
		for _, name := range []string{
			"input_token_price", "output_token_price",
			"cache_read_token_price", "cache_write_token_price",
		} {
			allowedRates[name] = true
		}
	case BillingModeEmbeddingInputToken, BillingModeRerankInputToken:
		allowedRates["input_token_price"] = true
	case BillingModeEmbeddingTotalToken, BillingModeRerankTotalToken:
		allowedRates["total_token_price"] = true
	case BillingModeEmbeddingProviderInput:
		allowedRates["per_input_price"] = true
	case BillingModeEmbeddingProviderRequest, BillingModeRerankProviderRequest:
		allowedRates["per_request_price"] = true
	case BillingModeRerankProviderPair:
		allowedRates["per_pair_price"] = true
	}
	for name, rate := range map[string]*Decimal{
		"input_token_price": p.InputTokenPrice, "output_token_price": p.OutputTokenPrice,
		"total_token_price": p.TotalTokenPrice, "cache_read_token_price": p.CacheReadTokenPrice,
		"cache_write_token_price": p.CacheWriteTokenPrice, "per_request_price": p.PerRequestPrice,
		"per_input_price": p.PerInputPrice, "per_pair_price": p.PerPairPrice,
	} {
		if rate != nil && !allowedRates[name] {
			return fmt.Errorf("model_pricing: %s is not used by billing_mode %q", name, p.BillingMode)
		}
	}
	return nil
}

// CostStatus describes whether a usage row could be fully priced.
type CostStatus string

const (
	// CostStatusPriced indicates that every applicable meter was priced.
	CostStatusPriced CostStatus = "priced"
	// CostStatusUnpriced indicates that no matching price was available.
	CostStatusUnpriced CostStatus = "unpriced"
	// CostStatusPartial indicates that only some applicable meters were priced.
	CostStatusPartial CostStatus = "partial"
)

// ModelUsageCost stores the immutable derived cost for one usage row.
type ModelUsageCost struct {
	ID                string     `gorm:"type:varchar(36);primaryKey"`
	UsageID           string     `gorm:"type:varchar(36);not null;uniqueIndex"`
	Status            CostStatus `gorm:"type:varchar(16);not null"`
	Currency          *string    `gorm:"type:varchar(16)"`
	TotalCost         *Decimal   `gorm:"type:decimal(38,18)"`
	KnownCost         *Decimal   `gorm:"type:decimal(38,18)"`
	InputCost         *Decimal   `gorm:"type:decimal(38,18)"`
	OutputCost        *Decimal   `gorm:"type:decimal(38,18)"`
	CacheReadCost     *Decimal   `gorm:"type:decimal(38,18)"`
	CacheWriteCost    *Decimal   `gorm:"type:decimal(38,18)"`
	RequestCost       *Decimal   `gorm:"type:decimal(38,18)"`
	ProviderInputCost *Decimal   `gorm:"type:decimal(38,18)"`
	ProviderPairCost  *Decimal   `gorm:"type:decimal(38,18)"`
	PricingRuleID     *string    `gorm:"type:varchar(36)"`
	PricingVersion    *string    `gorm:"type:varchar(128)"`
	PricingSnapshot   JSON       `gorm:"type:jsonb;not null;default:'{}'"`
	CalculatorVersion string     `gorm:"type:varchar(32);not null"`
	CalculatedAt      time.Time  `gorm:"not null"`
}

// TableName returns the database table for derived model usage costs.
func (ModelUsageCost) TableName() string { return "model_usage_cost" }

// BeforeCreate assigns an identifier to a new model usage cost row.
func (c *ModelUsageCost) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}
