package service

import (
	"context"
	"strings"
	"testing"

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

// TestVLMOCRPromptExcludesCustomInstructions pins the OCR-side half of the
// prompt contract: knowledge base custom instructions may shape *how images
// are described* (caption prompt) but must never reach the OCR prompt, where
// free-form business rules compete with the "No text content" output contract
// and produce poisoned image_ocr child chunks.
func TestVLMOCRPromptExcludesCustomInstructions(t *testing.T) {
	const marker = "UNIQUE_BUSINESS_MARKER_8f3a"
	cfg := types.VLMConfig{CustomInstructions: "For images without text output " + marker}

	for _, sourceType := range []string{"", "scanned_pdf"} {
		got := buildVLMOCRPrompt(sourceType, cfg)
		if strings.Contains(got, marker) {
			t.Fatalf("OCR prompt for source %q leaked custom instructions: %s", sourceType, got)
		}
		if !strings.Contains(got, "No text content") {
			t.Fatalf("OCR prompt for source %q lost the No text content contract: %s", sourceType, got)
		}
	}

	if got := buildVLMOCRPrompt("scanned_pdf", cfg); got != vlmOCRScannedPDFPrompt {
		t.Fatalf("scanned_pdf source must use the scanned-PDF prompt verbatim")
	}
	if got := buildVLMOCRPrompt("regular", cfg); got != vlmOCRPrompt {
		t.Fatalf("regular source must use the default OCR prompt verbatim")
	}

	// The caption path keeps its custom instructions — only the OCR append was
	// removed.
	caption := buildVLMCaptionPrompt(context.Background(), cfg)
	if !strings.Contains(caption, marker) {
		t.Fatalf("caption prompt should still carry custom instructions: %s", caption)
	}
}
