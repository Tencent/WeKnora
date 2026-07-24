package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildVLMCaptionPrompt(t *testing.T) {
	t.Run("uses configured language and custom instructions", func(t *testing.T) {
		got := buildVLMCaptionPrompt(context.Background(), types.VLMConfig{
			DescriptionLanguage: "English",
			CustomInstructions:  "Focus on alarm codes.",
		})
		if !strings.Contains(got, "in English") || !strings.Contains(got, "Focus on alarm codes.") {
			t.Fatalf("unexpected prompt: %s", got)
		}
	})

	t.Run("defaults to context language", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), types.LanguageContextKey, "ko-KR")
		got := buildVLMCaptionPrompt(ctx, types.VLMConfig{})
		if !strings.Contains(got, "in Korean") {
			t.Fatalf("unexpected prompt: %s", got)
		}
	})
}

func TestVLMCacheKeyIncludesResolvedPromptAndConcreteModelConfig(t *testing.T) {
	cfg := types.VLMConfig{
		Enabled:             true,
		ModelID:             "vlm-id",
		ModelName:           "vlm-name",
		BaseURL:             "https://vlm-a.example",
		InterfaceType:       "openai",
		DescriptionLanguage: "English",
		CustomInstructions:  "Focus on alarm codes.",
	}
	prompt := buildVLMCaptionPrompt(context.Background(), cfg)
	base := contentcache.VLMKey(
		"image-hash",
		vlmCacheModelKey(cfg),
		vlmCaptionPromptVersion+":"+contentcache.TextHash(prompt),
	)

	promptChanged := cfg
	promptChanged.CustomInstructions = "Focus on wiring."
	requireDifferent(t, base, contentcache.VLMKey(
		"image-hash",
		vlmCacheModelKey(promptChanged),
		vlmCaptionPromptVersion+":"+contentcache.TextHash(buildVLMCaptionPrompt(context.Background(), promptChanged)),
	))

	providerChanged := cfg
	providerChanged.BaseURL = "https://vlm-b.example"
	requireDifferent(t, base, contentcache.VLMKey(
		"image-hash",
		vlmCacheModelKey(providerChanged),
		vlmCaptionPromptVersion+":"+contentcache.TextHash(prompt),
	))
}

func requireDifferent(t *testing.T, a, b string) {
	t.Helper()
	if a == b {
		t.Fatalf("expected different values, both were %q", a)
	}
}
