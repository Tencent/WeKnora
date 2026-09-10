package vlm

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	openai "github.com/sashabaranov/go-openai"
)

func TestVLMImageDetail(t *testing.T) {
	t.Run("MiniMax uses documented default detail", func(t *testing.T) {
		t.Setenv("VLM_IMAGE_DETAIL", "")
		got := vlmImageDetail(provider.ProviderMiniMax)
		if got != openai.ImageURLDetail("default") {
			t.Fatalf("expected MiniMax detail default, got %q", got)
		}
	})

	t.Run("OpenAI keeps auto detail", func(t *testing.T) {
		t.Setenv("VLM_IMAGE_DETAIL", "")
		got := vlmImageDetail(provider.ProviderOpenAI)
		if got != openai.ImageURLDetailAuto {
			t.Fatalf("expected OpenAI detail auto, got %q", got)
		}
	})

	t.Run("environment override wins", func(t *testing.T) {
		t.Setenv("VLM_IMAGE_DETAIL", "low")
		got := vlmImageDetail(provider.ProviderMiniMax)
		if got != openai.ImageURLDetailLow {
			t.Fatalf("expected env override low, got %q", got)
		}
	})
}
