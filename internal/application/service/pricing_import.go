package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// PricingSourceSchemaVersion is the supported pricing source schema version.
const PricingSourceSchemaVersion = 1

// PricingFileImportResult summarizes one pricing file import.
type PricingFileImportResult struct {
	PricingVersion string
	SourceName     string
	Inserted       int
	NoOp           int
	Closed         int
}

// PricingImporter validates and imports versioned pricing files.
type PricingImporter struct {
	repository interfaces.PricingRepository
}

// NewPricingImporter creates a pricing file importer.
func NewPricingImporter(repository interfaces.PricingRepository) *PricingImporter {
	return &PricingImporter{repository: repository}
}

// ImportFile validates and imports pricing rules from path.
func (i *PricingImporter) ImportFile(ctx context.Context, path string) (*PricingFileImportResult, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("pricing import: file path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pricing import: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	source, rules, err := parsePricingSource(f)
	if err != nil {
		return nil, fmt.Errorf("pricing import: parse %s: %w", path, err)
	}
	result, err := i.repository.ImportPricingBatch(ctx, rules)
	if err != nil {
		return nil, fmt.Errorf("pricing import: import %s: %w", path, err)
	}
	return &PricingFileImportResult{
		PricingVersion: source.PricingVersion,
		SourceName:     source.Source.Name,
		Inserted:       result.Inserted,
		NoOp:           result.NoOp,
		Closed:         result.Closed,
	}, nil
}

type pricingSource struct {
	SchemaVersion  int                   `yaml:"schema_version"`
	PricingVersion string                `yaml:"pricing_version"`
	Source         pricingSourceMetadata `yaml:"source"`
	Rules          []pricingSourceRule   `yaml:"rules"`
}

type pricingSourceMetadata struct {
	Name        string          `yaml:"name"`
	Reference   *string         `yaml:"reference"`
	RetrievedAt strictTimestamp `yaml:"retrieved_at"`
}

type pricingSourceRule struct {
	ID                string            `yaml:"id"`
	ResolvedProvider  string            `yaml:"resolved_provider"`
	ResolvedModelName string            `yaml:"resolved_model_name"`
	CallType          types.CallType    `yaml:"call_type"`
	BillingMode       types.BillingMode `yaml:"billing_mode"`
	Currency          string            `yaml:"currency"`
	UnitScale         strictDecimal     `yaml:"unit_scale"`

	InputTokenPrice      *strictDecimal `yaml:"input_token_price"`
	OutputTokenPrice     *strictDecimal `yaml:"output_token_price"`
	TotalTokenPrice      *strictDecimal `yaml:"total_token_price"`
	CacheReadTokenPrice  *strictDecimal `yaml:"cache_read_token_price"`
	CacheWriteTokenPrice *strictDecimal `yaml:"cache_write_token_price"`
	PerRequestPrice      *strictDecimal `yaml:"per_request_price"`
	PerInputPrice        *strictDecimal `yaml:"per_input_price"`
	PerPairPrice         *strictDecimal `yaml:"per_pair_price"`

	EffectiveFrom strictTimestamp  `yaml:"effective_from"`
	EffectiveTo   *strictTimestamp `yaml:"effective_to"`
	ClosesRuleID  *string          `yaml:"closes_rule_id"`
}

type strictDecimal string

func (d *strictDecimal) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag != "!!str" {
		return fmt.Errorf("decimal must be a YAML string, got %s", node.ShortTag())
	}
	*d = strictDecimal(node.Value)
	return nil
}

type strictTimestamp struct{ time.Time }

func (t *strictTimestamp) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag != "!!str" {
		return fmt.Errorf("timestamp must be an RFC3339 YAML string, got %s", node.ShortTag())
	}
	parsed, err := time.Parse(time.RFC3339Nano, node.Value)
	if err != nil {
		return fmt.Errorf("invalid RFC3339 timestamp %q: %w", node.Value, err)
	}
	t.Time = parsed
	return nil
}

// parsePricingSource strictly decodes and validates one YAML document. It
// returns repository-ready immutable facts; no current time or inferred model
// identity is introduced during conversion.
func parsePricingSource(r io.Reader) (*pricingSource, []types.PricingImportRule, error) {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	var source pricingSource
	if err := decoder.Decode(&source); err != nil {
		return nil, nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("multiple YAML documents are not allowed")
		}
		return nil, nil, err
	}
	if source.SchemaVersion != PricingSourceSchemaVersion {
		return nil, nil, fmt.Errorf("schema_version must be %d", PricingSourceSchemaVersion)
	}
	if strings.TrimSpace(source.PricingVersion) == "" {
		return nil, nil, fmt.Errorf("pricing_version is required")
	}
	if strings.TrimSpace(source.Source.Name) == "" {
		return nil, nil, fmt.Errorf("source.name is required")
	}
	if source.Source.Reference != nil && strings.TrimSpace(*source.Source.Reference) == "" {
		return nil, nil, fmt.Errorf("source.reference must be non-empty when provided")
	}
	if source.Source.RetrievedAt.IsZero() {
		return nil, nil, fmt.Errorf("source.retrieved_at is required")
	}
	if err := validateDatabaseTimestamp("source.retrieved_at", source.Source.RetrievedAt.Time); err != nil {
		return nil, nil, err
	}
	if utf8.RuneCountInString(source.PricingVersion) > 128 || utf8.RuneCountInString(source.Source.Name) > 128 {
		return nil, nil, fmt.Errorf("pricing_version and source.name must not exceed 128 characters")
	}
	if len(source.Rules) == 0 {
		return nil, nil, fmt.Errorf("at least one pricing rule is required")
	}

	converted := make([]types.PricingImportRule, 0, len(source.Rules))
	seen := make(map[string]struct{}, len(source.Rules))
	closed := make(map[string]string)
	for index := range source.Rules {
		raw := &source.Rules[index]
		if strings.TrimSpace(raw.ResolvedProvider) == "" || strings.TrimSpace(raw.ResolvedModelName) == "" {
			return nil, nil, fmt.Errorf("rules[%d]: resolved_provider and resolved_model_name are required", index)
		}
		if utf8.RuneCountInString(raw.ResolvedProvider) > 64 || utf8.RuneCountInString(raw.ResolvedModelName) > 255 ||
			utf8.RuneCountInString(raw.Currency) > 16 {
			return nil, nil, fmt.Errorf(
				"rules[%d]: runtime identity or currency exceeds model_pricing column limits", index,
			)
		}
		if strings.TrimSpace(raw.Currency) == "" {
			return nil, nil, fmt.Errorf("rules[%d].currency must not be empty or whitespace", index)
		}
		if err := validateCanonicalUUID(fmt.Sprintf("rules[%d].id", index), raw.ID); err != nil {
			return nil, nil, err
		}
		if _, duplicate := seen[raw.ID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate pricing rule id %q", raw.ID)
		}
		seen[raw.ID] = struct{}{}
		if raw.ClosesRuleID != nil {
			if err := validateCanonicalUUID(
				fmt.Sprintf("rules[%d].closes_rule_id", index), *raw.ClosesRuleID,
			); err != nil {
				return nil, nil, err
			}
			if previous, duplicate := closed[*raw.ClosesRuleID]; duplicate {
				return nil, nil, fmt.Errorf(
					"pricing rules %q and %q both close rule %q", previous, raw.ID, *raw.ClosesRuleID,
				)
			}
			closed[*raw.ClosesRuleID] = raw.ID
		}

		pricing := types.ModelPricing{
			ID: raw.ID, ResolvedProvider: raw.ResolvedProvider, ResolvedModelName: raw.ResolvedModelName,
			CallType: raw.CallType, BillingMode: raw.BillingMode, Currency: raw.Currency,
			UnitScale:            decimalValue(raw.UnitScale),
			InputTokenPrice:      decimalPointer(raw.InputTokenPrice),
			OutputTokenPrice:     decimalPointer(raw.OutputTokenPrice),
			TotalTokenPrice:      decimalPointer(raw.TotalTokenPrice),
			CacheReadTokenPrice:  decimalPointer(raw.CacheReadTokenPrice),
			CacheWriteTokenPrice: decimalPointer(raw.CacheWriteTokenPrice),
			PerRequestPrice:      decimalPointer(raw.PerRequestPrice),
			PerInputPrice:        decimalPointer(raw.PerInputPrice), PerPairPrice: decimalPointer(raw.PerPairPrice),
			EffectiveFrom: raw.EffectiveFrom.Time, EffectiveTo: timestampPointer(raw.EffectiveTo),
			PricingVersion: source.PricingVersion, SourceName: source.Source.Name,
			SourceReference: source.Source.Reference, SourceRetrievedAt: &source.Source.RetrievedAt.Time,
		}
		if err := pricing.Validate(); err != nil {
			return nil, nil, fmt.Errorf("rules[%d] (%s): %w", index, raw.ID, err)
		}
		if err := validatePricingStorageBounds(index, &pricing); err != nil {
			return nil, nil, err
		}
		converted = append(converted, types.PricingImportRule{Pricing: pricing, ClosesRuleID: raw.ClosesRuleID})
	}
	if err := validatePricingSourceIntervals(converted); err != nil {
		return nil, nil, err
	}
	return &source, converted, nil
}

func validateCanonicalUUID(name, raw string) error {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s must be a canonical lowercase UUID: %w", name, err)
	}
	if parsed.String() != raw {
		return fmt.Errorf("%s must be a canonical lowercase UUID", name)
	}
	return nil
}

func validatePricingStorageBounds(index int, pricing *types.ModelPricing) error {
	if err := validateStoredDecimal("unit_scale", pricing.UnitScale, 29, 9); err != nil {
		return fmt.Errorf("rules[%d] (%s): %w", index, pricing.ID, err)
	}
	for name, value := range map[string]*types.Decimal{
		"input_token_price": pricing.InputTokenPrice, "output_token_price": pricing.OutputTokenPrice,
		"total_token_price": pricing.TotalTokenPrice, "cache_read_token_price": pricing.CacheReadTokenPrice,
		"cache_write_token_price": pricing.CacheWriteTokenPrice, "per_request_price": pricing.PerRequestPrice,
		"per_input_price": pricing.PerInputPrice, "per_pair_price": pricing.PerPairPrice,
	} {
		if value != nil {
			if err := validateStoredDecimal(name, *value, 20, 18); err != nil {
				return fmt.Errorf("rules[%d] (%s): %w", index, pricing.ID, err)
			}
		}
	}
	if err := validateDatabaseTimestamp("effective_from", pricing.EffectiveFrom); err != nil {
		return fmt.Errorf("rules[%d] (%s): %w", index, pricing.ID, err)
	}
	if pricing.EffectiveTo != nil {
		if err := validateDatabaseTimestamp("effective_to", *pricing.EffectiveTo); err != nil {
			return fmt.Errorf("rules[%d] (%s): %w", index, pricing.ID, err)
		}
	}
	return nil
}

func validateStoredDecimal(name string, value types.Decimal, maxIntegerDigits, maxFractionDigits int) error {
	integer, fraction, _ := strings.Cut(string(value), ".")
	if len(integer) > maxIntegerDigits || len(fraction) > maxFractionDigits {
		return fmt.Errorf(
			"%s exceeds database precision (%d integer, %d fractional digits)",
			name, maxIntegerDigits, maxFractionDigits,
		)
	}
	return nil
}

func validateDatabaseTimestamp(name string, value time.Time) error {
	if value.Nanosecond()%1_000 != 0 {
		return fmt.Errorf("%s must not have sub-microsecond precision", name)
	}
	return nil
}

func decimalValue(value strictDecimal) types.Decimal { return types.Decimal(value) }

func decimalPointer(value *strictDecimal) *types.Decimal {
	if value == nil {
		return nil
	}
	decimal := types.Decimal(*value)
	return &decimal
}

func timestampPointer(value *strictTimestamp) *time.Time {
	if value == nil {
		return nil
	}
	result := value.Time
	return &result
}

func validatePricingSourceIntervals(rules []types.PricingImportRule) error {
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			a, b := &rules[i].Pricing, &rules[j].Pricing
			if a.ResolvedProvider != b.ResolvedProvider ||
				a.ResolvedModelName != b.ResolvedModelName || a.CallType != b.CallType {
				continue
			}
			if intervalsOverlap(a.EffectiveFrom, a.EffectiveTo, b.EffectiveFrom, b.EffectiveTo) {
				return fmt.Errorf("pricing rules %q and %q have overlapping effective intervals", a.ID, b.ID)
			}
		}
	}
	return nil
}

func intervalsOverlap(aFrom time.Time, aTo *time.Time, bFrom time.Time, bTo *time.Time) bool {
	return (bTo == nil || aFrom.Before(*bTo)) && (aTo == nil || bFrom.Before(*aTo))
}
