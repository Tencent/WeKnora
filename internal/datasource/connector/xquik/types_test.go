package xquik

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

func testConfig(queries string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeXquik,
		Credentials: map[string]interface{}{
			"api_key": "xq_test_key",
		},
		Settings: map[string]interface{}{
			"queries": queries,
		},
		ResourceIDs: strings.Split(queries, "\n"),
	}
}

func TestParseConfig(t *testing.T) {
	input := testConfig(" from:weknora  \nrag lang:en\nfrom:weknora\n")
	input.Settings["results_per_query"] = "250"

	cfg, err := parseConfig(input)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.APIKey != "xq_test_key" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if got := strings.Join(cfg.Queries, "|"); got != "from:weknora|rag lang:en" {
		t.Fatalf("Queries = %q", got)
	}
	if cfg.ResultsPerQuery != 250 {
		t.Fatalf("ResultsPerQuery = %d", cfg.ResultsPerQuery)
	}
}

func TestParseConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.DataSourceConfig)
	}{
		{name: "nil config", mutate: func(*types.DataSourceConfig) {}},
		{name: "missing API key", mutate: func(cfg *types.DataSourceConfig) {
			delete(cfg.Credentials, "api_key")
		}},
		{name: "missing queries", mutate: func(cfg *types.DataSourceConfig) {
			cfg.Settings["queries"] = " "
		}},
		{name: "invalid queries type", mutate: func(cfg *types.DataSourceConfig) {
			cfg.Settings["queries"] = []string{"xquik"}
		}},
		{name: "invalid result type", mutate: func(cfg *types.DataSourceConfig) {
			cfg.Settings["results_per_query"] = "many"
		}},
		{name: "invalid result count", mutate: func(cfg *types.DataSourceConfig) {
			cfg.Settings["results_per_query"] = maxResultsPerQuery + 1
		}},
		{name: "long query", mutate: func(cfg *types.DataSourceConfig) {
			cfg.Settings["queries"] = strings.Repeat("界", maxQueryRunes+1)
		}},
		{name: "too many queries", mutate: func(cfg *types.DataSourceConfig) {
			queries := make([]string, maxQueries+1)
			for i := range queries {
				queries[i] = strings.Repeat("q", i+1)
			}
			cfg.Settings["queries"] = strings.Join(queries, "\n")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var input *types.DataSourceConfig
			if test.name != "nil config" {
				input = testConfig("xquik")
				test.mutate(input)
			}
			if _, err := parseConfig(input); err == nil {
				t.Fatal("parseConfig() error = nil")
			}
		})
	}
}

func TestParseConfigSupportsValidationCredentials(t *testing.T) {
	input := &types.DataSourceConfig{Credentials: map[string]interface{}{
		"api_key":           "key",
		"queries":           "xquik api",
		"results_per_query": float64(20),
	}}
	cfg, err := parseConfig(input)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if len(cfg.Queries) != 1 || cfg.Queries[0] != "xquik api" || cfg.ResultsPerQuery != 20 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestSelectedQueries(t *testing.T) {
	cfg := &config{Queries: []string{"one", "two"}}
	selected, err := cfg.selectedQueries([]string{"two", "missing", "two"})
	if err != nil {
		t.Fatalf("selectedQueries() error = %v", err)
	}
	if len(selected) != 1 || selected[0] != "two" {
		t.Fatalf("selectedQueries() = %v", selected)
	}
	if _, err := cfg.selectedQueries([]string{"missing"}); err == nil {
		t.Fatal("selectedQueries() error = nil for an unknown query")
	}
	if _, err := cfg.selectedQueries(nil); err == nil {
		t.Fatal("selectedQueries() error = nil for an empty selection")
	}
}

func TestFlexibleTimeAliases(t *testing.T) {
	var text flexibleTime
	if err := json.Unmarshal([]byte(`"2026-08-26T10:11:12.123Z"`), &text); err != nil {
		t.Fatalf("Unmarshal string error = %v", err)
	}
	if got := text.Format(time.RFC3339Nano); got != "2026-08-26T10:11:12.123Z" {
		t.Fatalf("string time = %s", got)
	}

	var unix flexibleTime
	if err := json.Unmarshal([]byte(`1756203072.5`), &unix); err != nil {
		t.Fatalf("Unmarshal Unix time error = %v", err)
	}
	if unix.Nanosecond() != 500_000_000 {
		t.Fatalf("Unix nanoseconds = %d", unix.Nanosecond())
	}

	post := tweet{CreatedAtSnake: unix}
	if !post.createdTime().Equal(unix.Time) {
		t.Fatalf("createdTime() = %s", post.createdTime())
	}
	if utf8.RuneCountInString(truncateRunes(strings.Repeat("界", 20), 8)) != 8 {
		t.Fatalf("truncateRunes() did not preserve the rune limit")
	}
}

func TestSearchPageAliases(t *testing.T) {
	page := searchPage{LegacyHasMore: true, LegacyNextCursor: "legacy"}
	if !page.hasMore() || page.nextCursor() != "legacy" {
		t.Fatalf("legacy page aliases were not used: %#v", page)
	}
	page.NextCursor = "current"
	if page.nextCursor() != "current" {
		t.Fatalf("nextCursor() = %q", page.nextCursor())
	}
}
