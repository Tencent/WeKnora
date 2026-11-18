package types

import (
	"encoding/json"
	"testing"
)

func TestNormalizePaddleOCRVLHeaders(t *testing.T) {
	got := NormalizePaddleOCRVLHeaders(map[string]string{
		"  x-gateway-token  ": " secret ",
		"   ":                 "dropped",
		"X-Trace":             "  ",
	})
	want := map[string]string{
		"X-Gateway-Token": "secret",
		"X-Trace":         "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("header %q = %q, want %q (all: %v)", name, got[name], value, got)
		}
	}
	if NormalizePaddleOCRVLHeaders(nil) != nil {
		t.Fatal("nil input must stay nil")
	}
	if got := NormalizePaddleOCRVLHeaders(map[string]string{" ": "x"}); got != nil {
		t.Fatalf("blank-only headers must normalize to nil, got %v", got)
	}
}

func TestValidatePaddleOCRVLHeaders(t *testing.T) {
	valid := map[string]string{
		"Authorization":   "Bearer abc",
		"X-Gateway-Token": "t-1",
		"X-Empty":         "",
		"X-Tabbed":        "a\tb",
	}
	if err := ValidatePaddleOCRVLHeaders(valid); err != nil {
		t.Fatalf("valid headers rejected: %v", err)
	}

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"invalid name", map[string]string{"bad header": "v"}},
		{"empty name", map[string]string{" ": "v"}},
		{"content type", map[string]string{"content-type": "text/plain"}},
		{"host", map[string]string{"Host": "evil.internal"}},
		{"transfer encoding", map[string]string{"Transfer-Encoding": "chunked"}},
		{"newline in value", map[string]string{"X-Token": "a\r\nInjected: 1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePaddleOCRVLHeaders(tc.headers); err == nil {
				t.Fatalf("expected %v to be rejected", tc.headers)
			}
		})
	}
}

func TestParserEngineConfigHeadersOverride(t *testing.T) {
	cfg := &ParserEngineConfig{
		PaddleOCRVLEndpoint: "http://paddleocr-vl:8080",
		PaddleOCRVLHeaders:  map[string]string{"x-gateway-token": "abc"},
	}
	overrides := cfg.ToOverridesMap()
	raw := overrides["paddleocr_vl_headers"]
	if raw == "" {
		t.Fatalf("headers missing from overrides: %v", overrides)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("headers are not a JSON object: %v", err)
	}
	if decoded["X-Gateway-Token"] != "abc" {
		t.Fatalf("canonical header missing: %v", decoded)
	}

	empty := (&ParserEngineConfig{PaddleOCRVLEndpoint: "http://x"}).ToOverridesMap()
	if _, ok := empty["paddleocr_vl_headers"]; ok {
		t.Fatalf("empty header set must not be exported: %v", empty)
	}
}

func TestParserEngineConfigHeadersRedaction(t *testing.T) {
	cfg := &ParserEngineConfig{
		PaddleOCRVLHeaders: map[string]string{"X-Gateway-Token": "secret"},
	}
	redacted := ParserEngineConfigForResponse(cfg, true)
	if redacted.PaddleOCRVLHeaders["X-Gateway-Token"] != RedactedSecretPlaceholder {
		t.Fatalf("header value not redacted: %v", redacted.PaddleOCRVLHeaders)
	}
	if cfg.PaddleOCRVLHeaders["X-Gateway-Token"] != "secret" {
		t.Fatal("redaction must not mutate the stored config")
	}
	plain := ParserEngineConfigForResponse(cfg, false)
	if plain.PaddleOCRVLHeaders["X-Gateway-Token"] != "secret" {
		t.Fatalf("maskSecrets=false must keep values: %v", plain.PaddleOCRVLHeaders)
	}
}

func TestMergeParserEngineConfigHeaders(t *testing.T) {
	existing := &ParserEngineConfig{
		PaddleOCRVLHeaders: map[string]string{
			"X-Gateway-Token": "stored",
			"X-Dropped":       "old",
		},
	}

	t.Run("placeholder keeps stored value and deletion sticks", func(t *testing.T) {
		merged := MergeParserEngineConfigForUpdate(&ParserEngineConfig{
			PaddleOCRVLHeaders: map[string]string{"x-gateway-token": RedactedSecretPlaceholder},
		}, existing)
		if merged.PaddleOCRVLHeaders["X-Gateway-Token"] != "stored" {
			t.Fatalf("stored secret lost: %v", merged.PaddleOCRVLHeaders)
		}
		if _, ok := merged.PaddleOCRVLHeaders["X-Dropped"]; ok {
			t.Fatalf("removed header resurrected: %v", merged.PaddleOCRVLHeaders)
		}
	})

	t.Run("explicit value replaces stored", func(t *testing.T) {
		merged := MergeParserEngineConfigForUpdate(&ParserEngineConfig{
			PaddleOCRVLHeaders: map[string]string{"X-Gateway-Token": "rotated"},
		}, existing)
		if merged.PaddleOCRVLHeaders["X-Gateway-Token"] != "rotated" {
			t.Fatalf("new value ignored: %v", merged.PaddleOCRVLHeaders)
		}
	})

	t.Run("omitted clears", func(t *testing.T) {
		merged := MergeParserEngineConfigForUpdate(&ParserEngineConfig{}, existing)
		if len(merged.PaddleOCRVLHeaders) != 0 {
			t.Fatalf("omitted headers must clear, got %v", merged.PaddleOCRVLHeaders)
		}
	})
}
