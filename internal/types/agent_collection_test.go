package types

import (
	"strings"
	"testing"
)

func validCollectionConfig() CustomAgentConfig {
	return CustomAgentConfig{
		CollectionEnabled:             true,
		CollectionExtractionThreshold: 0.85,
		CollectionFields: []AgentCollectionField{
			{
				Key: "employment_status", Label: "当前就业状态", Type: AgentCollectionSingleChoice,
				Required: true, Enabled: true, Order: 10,
				Options: []AgentCollectionOption{{ID: "employed", Label: "在职"}, {ID: "dismissed", Label: "被辞退"}},
			},
			{
				Key: "dismissal_date", Label: "辞退日期", Type: AgentCollectionDate,
				Required: true, Enabled: true, Order: 20,
				VisibleWhen: &AgentCollectionCondition{Field: "employment_status", Operator: "equals", Value: "dismissed"},
			},
		},
	}
}

func TestValidateAgentCollectionConfigAcceptsConditionalSchema(t *testing.T) {
	cfg := validCollectionConfig()
	if err := ValidateAgentCollectionConfig(cfg); err != nil {
		t.Fatalf("ValidateAgentCollectionConfig() error = %v", err)
	}
}

func TestNormalizeAgentCollectionConfigSetsThreshold(t *testing.T) {
	cfg := CustomAgentConfig{CollectionEnabled: true}
	NormalizeAgentCollectionConfig(&cfg)
	if cfg.CollectionExtractionThreshold != DefaultCollectionExtractionThreshold {
		t.Fatalf("threshold = %v, want %v", cfg.CollectionExtractionThreshold, DefaultCollectionExtractionThreshold)
	}
}

func TestValidateAgentCollectionConfigRejectsSensitiveAndInvalidSchemas(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CustomAgentConfig)
		want string
	}{
		{name: "duplicate key", edit: func(c *CustomAgentConfig) { c.CollectionFields[1].Key = c.CollectionFields[0].Key }, want: "unique"},
		{name: "sensitive key", edit: func(c *CustomAgentConfig) { c.CollectionFields[1].Key = "api_key" }, want: "sensitive"},
		{name: "sensitive label", edit: func(c *CustomAgentConfig) { c.CollectionFields[1].Label = "请输入密码" }, want: "sensitive"},
		{name: "unknown dependency", edit: func(c *CustomAgentConfig) { c.CollectionFields[1].VisibleWhen.Field = "missing" }, want: "unknown"},
		{name: "forward dependency", edit: func(c *CustomAgentConfig) {
			c.CollectionFields[0].VisibleWhen = &AgentCollectionCondition{Field: "dismissal_date", Operator: "not_empty"}
		}, want: "earlier"},
		{name: "invalid threshold", edit: func(c *CustomAgentConfig) { c.CollectionExtractionThreshold = 1.1 }, want: "threshold"},
		{name: "choice options", edit: func(c *CustomAgentConfig) { c.CollectionFields[0].Options = c.CollectionFields[0].Options[:1] }, want: "options"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCollectionConfig()
			tt.edit(&cfg)
			err := ValidateAgentCollectionConfig(cfg)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateAgentCollectionConfigRejectsMoreThanMaximumFields(t *testing.T) {
	cfg := validCollectionConfig()
	cfg.CollectionFields = make([]AgentCollectionField, MaxAgentCollectionFields+1)
	for i := range cfg.CollectionFields {
		cfg.CollectionFields[i] = AgentCollectionField{
			Key:   "field_" + strings.Repeat("x", i/10) + string(rune('a'+i%10)),
			Label: "字段", Type: AgentCollectionShortText, Enabled: true, Order: i,
		}
	}
	if err := ValidateAgentCollectionConfig(cfg); err == nil {
		t.Fatal("expected maximum field validation error")
	}
}

func TestVisibleCollectionFieldsEvaluatesConditions(t *testing.T) {
	cfg := validCollectionConfig()
	visible := VisibleCollectionFields(cfg.CollectionFields, JSONMap{"employment_status": "dismissed"})
	if len(visible) != 2 {
		t.Fatalf("visible fields = %d, want 2", len(visible))
	}
	visible = VisibleCollectionFields(cfg.CollectionFields, JSONMap{"employment_status": "employed"})
	if len(visible) != 1 || visible[0].Key != "employment_status" {
		t.Fatalf("visible fields = %#v, want only employment_status", visible)
	}
}

func TestValidateCollectionValueCoversAllFieldTypes(t *testing.T) {
	min, max := 1, 5
	minNumber, maxNumber := 1.0, 10.0
	tests := []struct {
		field AgentCollectionField
		value any
		valid bool
	}{
		{field: AgentCollectionField{Type: AgentCollectionSingleChoice, Options: []AgentCollectionOption{{ID: "a"}, {ID: "b"}}}, value: "a", valid: true},
		{field: AgentCollectionField{Type: AgentCollectionMultipleChoice, Options: []AgentCollectionOption{{ID: "a"}, {ID: "b"}}}, value: []string{"a", "b"}, valid: true},
		{field: AgentCollectionField{Type: AgentCollectionShortText, Validation: AgentCollectionValidation{MinLength: &min, MaxLength: &max}}, value: "abcdef", valid: false},
		{field: AgentCollectionField{Type: AgentCollectionLongText}, value: "说明", valid: true},
		{field: AgentCollectionField{Type: AgentCollectionNumber, Validation: AgentCollectionValidation{MinNumber: &minNumber, MaxNumber: &maxNumber}}, value: 11.0, valid: false},
		{field: AgentCollectionField{Type: AgentCollectionDate, Validation: AgentCollectionValidation{MinDate: "2026-01-01", MaxDate: "2026-12-31"}}, value: "2026-07-22", valid: true},
		{field: AgentCollectionField{Type: AgentCollectionDate}, value: "22/07/2026", valid: false},
	}
	for i, tt := range tests {
		err := ValidateCollectionValue(tt.field, tt.value)
		if (err == nil) != tt.valid {
			t.Fatalf("case %d error = %v, valid = %v", i, err, tt.valid)
		}
	}
}
