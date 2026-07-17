package dingtalk

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseDingTalkConfig(t *testing.T) {
	t.Run("valid and trimmed", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
			"client_id":     " app-key ",
			"client_secret": " secret ",
			"operator_id":   " union-id ",
		}})
		if err != nil {
			t.Fatalf("parseDingTalkConfig() error = %v", err)
		}
		if cfg.ClientID != "app-key" || cfg.ClientSecret != "secret" || cfg.OperatorID != "union-id" {
			t.Fatalf("credentials were not trimmed: %#v", cfg)
		}
		if got := cfg.GetBaseURL(); got != DefaultBaseURL {
			t.Fatalf("GetBaseURL() = %q, want %q", got, DefaultBaseURL)
		}
	})

	for _, tc := range []struct {
		name         string
		config       *types.DataSourceConfig
		wantSentinel error
	}{
		{name: "nil config", config: nil, wantSentinel: datasource.ErrInvalidConfig},
		{name: "missing client id", config: &types.DataSourceConfig{Credentials: map[string]interface{}{"client_secret": "s", "operator_id": "o"}}, wantSentinel: datasource.ErrInvalidCredentials},
		{name: "missing secret", config: &types.DataSourceConfig{Credentials: map[string]interface{}{"client_id": "i", "operator_id": "o"}}, wantSentinel: datasource.ErrInvalidCredentials},
		{name: "missing operator", config: &types.DataSourceConfig{Credentials: map[string]interface{}{"client_id": "i", "client_secret": "s"}}, wantSentinel: datasource.ErrInvalidCredentials},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDingTalkConfig(tc.config)
			if !errors.Is(err, tc.wantSentinel) {
				t.Fatalf("parseDingTalkConfig() error = %v, want errors.Is(%v)", err, tc.wantSentinel)
			}
		})
	}

	t.Run("rejects unsafe base url", func(t *testing.T) {
		_, err := parseDingTalkConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
			"client_id": "i", "client_secret": "s", "operator_id": "o", "base_url": "http://127.0.0.1:8080",
		}})
		if err == nil || !strings.Contains(err.Error(), "SSRF") {
			t.Fatalf("parseDingTalkConfig() error = %v, want SSRF rejection", err)
		}
	})
}

func TestAccessTokenResponsePrefersOfficialField(t *testing.T) {
	response := accessTokenResponse{AccessToken: "official", LegacyAccessToken: "legacy"}
	if got := response.token(); got != "official" {
		t.Fatalf("token() = %q, want official", got)
	}
	response.AccessToken = ""
	if got := response.token(); got != "legacy" {
		t.Fatalf("token() fallback = %q, want legacy", got)
	}
}

func TestParseDingTalkTime(t *testing.T) {
	want := time.Date(2025, 4, 3, 2, 1, 0, 0, time.UTC)
	for _, value := range []string{
		"2025-04-03T02:01:00Z",
		"1743645660000",
		"1743645660",
	} {
		if got := parseDingTalkTime(value); !got.Equal(want) {
			t.Fatalf("parseDingTalkTime(%q) = %s, want %s", value, got, want)
		}
	}
	if got := parseDingTalkTime("not-a-time"); !got.IsZero() {
		t.Fatalf("invalid time = %s, want zero", got)
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName(` .bad:/\\*?"<>|. `); got != "bad" {
		t.Fatalf("sanitizeFileName() = %q", got)
	}
	long := strings.Repeat("文", 100)
	got := sanitizeFileName(long)
	if len(got) > 200 || !strings.HasPrefix(long, got) {
		t.Fatalf("sanitizeFileName(long) produced invalid UTF-8 truncation: bytes=%d value=%q", len(got), got)
	}
}

func TestAPIErrorSanitizesBody(t *testing.T) {
	err := apiErrorFromResponse(403, []byte(`{"code":"Forbidden","message":"permission denied","requestId":"req-1","secret":"must-not-leak"}`))
	text := err.Error()
	if !strings.Contains(text, "Forbidden") || !strings.Contains(text, "permission denied") || !strings.Contains(text, "req-1") {
		t.Fatalf("error omitted safe fields: %s", text)
	}
	if strings.Contains(text, "must-not-leak") {
		t.Fatalf("error leaked unmodeled response field: %s", text)
	}
}
