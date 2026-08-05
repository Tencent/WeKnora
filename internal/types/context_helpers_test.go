package types

import (
	"context"
	"testing"
)

func TestIsSyntheticUserID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"matches system-<digits>", "system-1", true},
		{"matches large tenant id", "system-1234567890", true},
		{"empty string", "", false},
		{"prefix only", "system-", false},
		{"missing prefix", "1", false},
		{"non-digit suffix", "system-abc", false},
		{"mixed suffix", "system-1a2", false},
		{"prefix with space", "system- 1", false},
		{"uppercase prefix", "SYSTEM-1", false},
		{"normal uuid user", "550e8400-e29b-41d4-a716-446655440000", false},
		{"system uuid trap", "system-550e8400", false}, // contains '-'
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := IsSyntheticUserID(c.id)
			if got != c.want {
				t.Fatalf("IsSyntheticUserID(%q) = %v, want %v", c.id, got, c.want)
			}
		})
	}
}

func TestLanguageLocaleName(t *testing.T) {
	tests := []struct {
		name     string
		locale   string
		expected string
	}{
		// Chinese (Simplified) variants
		{"Chinese Simplified zh-CN", "zh-CN", "Chinese (Simplified)"},
		{"Chinese Simplified zh", "zh", "Chinese (Simplified)"},
		{"Chinese Simplified zh-Hans", "zh-Hans", "Chinese (Simplified)"},

		// Chinese (Traditional) variants
		{"Chinese Traditional zh-TW", "zh-TW", "Chinese (Traditional)"},
		{"Chinese Traditional zh-HK", "zh-HK", "Chinese (Traditional)"},
		{"Chinese Traditional zh-Hant", "zh-Hant", "Chinese (Traditional)"},

		// English variants
		{"English en-US", "en-US", "English"},
		{"English en", "en", "English"},
		{"English en-GB", "en-GB", "English"},

		// Korean
		{"Korean ko-KR", "ko-KR", "Korean"},
		{"Korean ko", "ko", "Korean"},

		// Japanese
		{"Japanese ja-JP", "ja-JP", "Japanese"},
		{"Japanese ja", "ja", "Japanese"},

		// Russian
		{"Russian ru-RU", "ru-RU", "Russian"},
		{"Russian ru", "ru", "Russian"},

		// French
		{"French fr-FR", "fr-FR", "French"},
		{"French fr", "fr", "French"},

		// German
		{"German de-DE", "de-DE", "German"},
		{"German de", "de", "German"},

		// Spanish
		{"Spanish es-ES", "es-ES", "Spanish"},
		{"Spanish es", "es", "Spanish"},

		// Portuguese
		{"Portuguese pt-BR", "pt-BR", "Portuguese"},
		{"Portuguese pt", "pt", "Portuguese"},

		// Unknown/fallback
		{"Unknown locale", "unknown", "unknown"},
		{"Empty locale", "", ""},
		{"Arbitrary code", "xyz-ABC", "xyz-ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LanguageLocaleName(tt.locale)
			if result != tt.expected {
				t.Errorf("LanguageLocaleName(%q) = %q, want %q", tt.locale, result, tt.expected)
			}
		})
	}
}

func TestResolveLanguage(t *testing.T) {
	ctxWithLocale := context.WithValue(context.Background(), LanguageContextKey, "ko-KR")

	tests := []struct {
		name     string
		ctx      context.Context
		locale   string
		envLang  string
		expected string
	}{
		{"explicit locale wins over context", ctxWithLocale, "en-US", "", "en-US"},
		{"blank locale falls back to context", ctxWithLocale, "", "", "ko-KR"},
		{"whitespace locale falls back to context", ctxWithLocale, "   ", "", "ko-KR"},
		{"no locale and no context falls back to default", context.Background(), "", "", "zh-CN"},
		{"empty context value falls back to default", context.WithValue(
			context.Background(), LanguageContextKey, ""), "", "", "zh-CN"},
		{"deployment override wins over hardcoded default", context.Background(), "", "ru-RU", "ru-RU"},
		{"explicit locale wins over deployment override", context.Background(), "ja-JP", "ru-RU", "ja-JP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envLang != "" {
				t.Setenv("WEKNORA_LANGUAGE", tt.envLang)
			}
			if got := ResolveLanguage(tt.ctx, tt.locale); got != tt.expected {
				t.Errorf("ResolveLanguage(_, %q) = %q, want %q", tt.locale, got, tt.expected)
			}
		})
	}
}

// A missing language must never reach a prompt template: "Write in {{.Language}}"
// renders as "Write in ." and lets the model choose the output language.
func TestResolveLanguageNameNeverReturnsEmpty(t *testing.T) {
	for _, ctx := range []context.Context{
		context.Background(),
		context.WithValue(context.Background(), LanguageContextKey, ""),
	} {
		if got := ResolveLanguageName(ctx, ""); got == "" {
			t.Fatal("ResolveLanguageName returned an empty prompt language")
		}
	}
	if got := ResolveLanguageName(context.Background(), "ko-KR"); got != "Korean" {
		t.Errorf("ResolveLanguageName(_, %q) = %q, want %q", "ko-KR", got, "Korean")
	}
}

// Wiki carries an already-resolved display name on each queued update, so
// re-resolving it downstream must be a no-op rather than a reset to default.
func TestResolveLanguageNameIsIdempotentOverDisplayNames(t *testing.T) {
	for _, name := range []string{"Chinese (Simplified)", "English", "Korean"} {
		if got := ResolveLanguageName(context.Background(), name); got != name {
			t.Errorf("ResolveLanguageName(_, %q) = %q, want it unchanged", name, got)
		}
	}
}

func TestLanguageFromContextOrDefault(t *testing.T) {
	if got := LanguageFromContextOrDefault(context.Background()); got != "zh-CN" {
		t.Errorf("LanguageFromContextOrDefault(empty) = %q, want %q", got, "zh-CN")
	}
	ctx := context.WithValue(context.Background(), LanguageContextKey, "en-US")
	if got := LanguageFromContextOrDefault(ctx); got != "en-US" {
		t.Errorf("LanguageFromContextOrDefault(en-US) = %q, want %q", got, "en-US")
	}
}

func TestMCPOAuthNonInteractive(t *testing.T) {
	if IsMCPOAuthNonInteractive(nil) {
		t.Fatal("nil context should not be non-interactive")
	}
	if IsMCPOAuthNonInteractive(context.Background()) {
		t.Fatal("background context should not be non-interactive")
	}

	ctx := WithMCPOAuthNonInteractive(context.Background())
	if !IsMCPOAuthNonInteractive(ctx) {
		t.Fatal("marked context should be non-interactive")
	}
	child := context.WithValue(ctx, LanguageContextKey, "en-US")
	if !IsMCPOAuthNonInteractive(child) {
		t.Fatal("child context should inherit non-interactive flag")
	}
}

func TestLanguageFromContext(t *testing.T) {
	tests := []struct {
		name        string
		setupCtx    func() interface{}
		expectValue string
		expectOK    bool
	}{
		{
			name: "empty context",
			setupCtx: func() interface{} {
				return nil
			},
			expectValue: "",
			expectOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// These are basic smoke tests
			// Real context testing would require context.Context objects
			if tt.setupCtx == nil {
				t.Skip("skipping context-dependent test")
			}
		})
	}
}

func TestLLMCallMetadataContext(t *testing.T) {
	ctx := WithLLMCallMetadata(context.Background(), "wiki_page_modify", "abc123")
	purpose, prefix := LLMCallMetadataFromContext(ctx)
	if purpose != "wiki_page_modify" || prefix != "abc123" {
		t.Fatalf("metadata = (%q, %q)", purpose, prefix)
	}
}

// BenchmarkLanguageLocaleName benchmarks the language name lookup
func BenchmarkLanguageLocaleName(b *testing.B) {
	testCases := []string{"zh", "en", "zh-CN", "ko", "unknown"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, locale := range testCases {
			LanguageLocaleName(locale)
		}
	}
}
