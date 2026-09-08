package dbinspector

import (
	"testing"
)

func TestGuidedTypesRegistered(t *testing.T) {
	types := GuidedTypes()
	if len(types) != 4 {
		t.Fatalf("expected 4 guided types, got %d: %v", len(types), types)
	}
	want := map[string]bool{"mysql": true, "postgres": true, "clickhouse": true, "sqlserver": true}
	for _, tp := range types {
		if !want[tp] {
			t.Errorf("unexpected type %q", tp)
		}
	}
}

func TestGetAdapter(t *testing.T) {
	for _, tp := range []string{"mysql", "postgres", "clickhouse", "sqlserver"} {
		a, ok := Get(tp)
		if !ok || a == nil {
			t.Errorf("Get(%q) returned nil or not found", tp)
		}
		if a.Name() != tp {
			t.Errorf("adapter name mismatch: got %q, want %q", a.Name(), tp)
		}
	}
	// passthrough type has no adapter
	if _, ok := Get("snowflake"); ok {
		t.Error("snowflake should not have a guided adapter")
	}
}

func TestDefaultPorts(t *testing.T) {
	cases := map[string]int{
		"mysql":      3306,
		"postgres":   5432,
		"clickhouse": 8123,
		"sqlserver":  1433,
	}
	for tp, port := range cases {
		a, ok := Get(tp)
		if !ok {
			t.Fatalf("Get(%q) failed", tp)
		}
		if a.DefaultPort() != port {
			t.Errorf("DefaultPort(%q) = %d, want %d", tp, a.DefaultPort(), port)
		}
	}
}

func TestExtraString(t *testing.T) {
	cfg := &ConnectionConfig{Extra: map[string]interface{}{
		"sslmode": "require",
		"count":   42,
		"secure":  true,
		"missing": nil,
	}}
	if cfg.ExtraString("sslmode") != "require" {
		t.Errorf("ExtraString(sslmode) wrong")
	}
	if cfg.ExtraString("count") != "" {
		t.Errorf("ExtraString(count) should be empty for non-string")
	}
	if cfg.ExtraString("secure") != "" {
		t.Errorf("ExtraString(secure) should be empty for non-string")
	}
	if cfg.ExtraString("missing") != "" {
		t.Errorf("ExtraString(missing) should be empty for nil")
	}
	if cfg.ExtraString("nonexistent") != "" {
		t.Errorf("ExtraString(nonexistent) should be empty")
	}
}

func TestUnwrapWrapper(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"Nullable(String)", "String", true},
		{"LowCardinality(String)", "String", true},
		{"DateTime64(UTC)", "UTC", true},
		{"String", "", false},
		{"Array(String)", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := unwrapWrapper(tc.input)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("unwrapWrapper(%q) = (%q, %v), want (%q, %v)",
				tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"users", "users"},
		{"user's", "users"},
		{"safe_name", "safe_name"},
		{"`injected`", "injected"},
	}
	for _, tc := range cases {
		if got := quoteIdent(tc.input); got != tc.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
