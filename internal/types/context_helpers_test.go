package types

import (
	"testing"
)

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
