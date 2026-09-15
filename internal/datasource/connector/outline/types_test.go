package outline

import (
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func TestGetBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", DefaultBaseURL},
		{"  ", DefaultBaseURL},
		{"docs.example.com", "https://docs.example.com"},
		{"https://docs.example.com/", "https://docs.example.com"},
		{"https://docs.example.com///", "https://docs.example.com"},
		{"http://localhost:3000", "http://localhost:3000"},
	}
	for _, c := range cases {
		got := (&Config{BaseURL: c.in}).GetBaseURL()
		if got != c.want {
			t.Errorf("GetBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseOutlineConfig_RequiresToken(t *testing.T) {
	_, err := parseOutlineConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": "https://docs.example.com"},
	})
	if err == nil {
		t.Fatal("expected error when api_token is missing")
	}
}

func TestParseOutlineConfig_NilConfig(t *testing.T) {
	if _, err := parseOutlineConfig(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

// ValidateConnectorBaseURL resolves the host for real, so the base URL here has
// to be one TestMain whitelisted rather than a made-up domain.
func TestParseOutlineConfig_OK(t *testing.T) {
	cfg, err := parseOutlineConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"api_token": "ol_api_secret",
			"base_url":  "http://localhost:3000/",
		},
	})
	if err != nil {
		t.Fatalf("parseOutlineConfig: %v", err)
	}
	if cfg.APIToken != "ol_api_secret" {
		t.Errorf("APIToken = %q", cfg.APIToken)
	}
	if cfg.GetBaseURL() != "http://localhost:3000" {
		t.Errorf("GetBaseURL = %q", cfg.GetBaseURL())
	}
}

// A base URL outside the SSRF whitelist must be rejected at parse time, before
// any request is issued.
func TestParseOutlineConfig_RejectsUnsafeBaseURL(t *testing.T) {
	_, err := parseOutlineConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"api_token": "ol_api_secret",
			"base_url":  "http://169.254.169.254",
		},
	})
	if err == nil {
		t.Fatal("expected SSRF validation to reject a link-local base URL")
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName(""); got != "untitled" {
		t.Errorf("empty name = %q, want untitled", got)
	}
	if got := sanitizeFileName("a/b:c*d?e"); got != "a_b_c_d_e" {
		t.Errorf("illegal chars = %q", got)
	}
	long := sanitizeFileName(string(make([]byte, 400)))
	if len(long) > 200 {
		t.Errorf("length = %d, want <= 200", len(long))
	}
}

func TestRedactToken(t *testing.T) {
	if got := redactToken("short"); got != "***" {
		t.Errorf("short token = %q", got)
	}
	got := redactToken("ol_api_0123456789abcdef")
	if got == "ol_api_0123456789abcdef" {
		t.Error("redactToken returned the raw token")
	}
}

func TestParseOutlineTime(t *testing.T) {
	// Outline emits RFC3339 with milliseconds.
	if parseOutlineTime("2026-09-11T07:11:10.199Z").IsZero() {
		t.Error("valid timestamp parsed as zero")
	}
	if !parseOutlineTime("not a time").IsZero() {
		t.Error("invalid timestamp should parse as zero")
	}
	if !parseOutlineTime("").IsZero() {
		t.Error("empty timestamp should parse as zero")
	}
}
