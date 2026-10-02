package paperless

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type config struct {
	BaseURL            string
	Token              string
	CustomFieldFilters []customFieldFilter
	IncludeArchived    bool
}

type customFieldFilter struct {
	FieldID  int         `json:"field_id"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	cfg := &config{}
	if ds.Credentials != nil {
		cfg.BaseURL, _ = ds.Credentials["base_url"].(string)
		cfg.Token, _ = ds.Credentials["api_token"].(string)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL, _ = ds.Settings["base_url"].(string)
	}
	if cfg.Token == "" {
		cfg.Token, _ = ds.Settings["api_token"].(string)
	}
	filters, err := parseCustomFieldFilters(ds.Settings["custom_field_filters"])
	if err != nil {
		return nil, err
	}
	cfg.CustomFieldFilters = filters
	cfg.IncludeArchived, _ = ds.Settings["include_archived"].(bool)

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.BaseURL == "" || cfg.Token == "" {
		return nil, fmt.Errorf("paperless: base_url and api_token are required")
	}
	return cfg, nil
}

func parseCustomFieldFilters(raw interface{}) ([]customFieldFilter, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("paperless: encode custom_field_filters: %w", err)
	}
	if string(data) == "null" {
		return nil, nil
	}
	var filters []customFieldFilter
	if err := json.Unmarshal(data, &filters); err != nil {
		return nil, fmt.Errorf("paperless: custom_field_filters must be a list: %w", err)
	}
	if len(filters) > 20 {
		return nil, fmt.Errorf("paperless: custom_field_filters cannot contain more than 20 conditions")
	}
	valid := filters[:0]
	for _, filter := range filters {
		if filter.FieldID <= 0 || filter.Value == nil {
			return nil, fmt.Errorf("paperless: each custom field filter requires a positive field_id and value")
		}
		if filter.Operator == "" {
			filter.Operator = "exact"
		}
		switch filter.Operator {
		case "exact", "icontains", "istartswith", "iendswith", "gt", "gte", "lt", "lte":
			valid = append(valid, filter)
		default:
			return nil, fmt.Errorf("paperless: unsupported custom field operator %q", filter.Operator)
		}
	}
	return valid, nil
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
