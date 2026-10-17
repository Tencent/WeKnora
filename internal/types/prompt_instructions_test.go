package types

import (
	"strings"
	"testing"
)

func TestAppendCustomPromptInstructions(t *testing.T) {
	t.Run("empty preserves prompt", func(t *testing.T) {
		if got := AppendCustomPromptInstructions("base", "  ", "wiki"); got != "base" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("appends bounded business guidance after base", func(t *testing.T) {
		got := AppendCustomPromptInstructions("base", " Focus on contracts. ", "wiki")
		if !strings.HasPrefix(got, "base\n\n<wiki_business_instructions>") {
			t.Fatalf("unexpected prefix: %q", got)
		}
		if !strings.Contains(got, "Focus on contracts.") || !strings.Contains(got, "do not conflict") {
			t.Fatalf("missing guidance or precedence rule: %q", got)
		}
	})
}

func TestValidateKnowledgeBasePromptInstructions(t *testing.T) {
	kb := &KnowledgeBase{
		ChunkingConfig: ChunkingConfig{TableMetadataInstructions: strings.Repeat("a", MaxCustomPromptInstructionsLength+1)},
	}
	if err := ValidateKnowledgeBasePromptInstructions(kb); err == nil {
		t.Fatal("expected length validation error")
	}
}

func TestNormalizeKnowledgeBasePromptInstructions(t *testing.T) {
	kb := &KnowledgeBase{
		VLMConfig: VLMConfig{CustomInstructions: "  focus labels  "},
		VideoConfig: VideoConfig{
			VLMConfig: &VLMConfig{CustomInstructions: "  focus frames  "},
		},
	}
	NormalizeKnowledgeBasePromptInstructions(kb)
	if kb.VLMConfig.CustomInstructions != "focus labels" {
		t.Fatalf("got %q", kb.VLMConfig.CustomInstructions)
	}
	if kb.VideoConfig.VLMConfig == nil ||
		kb.VideoConfig.VLMConfig.CustomInstructions != "focus frames" {
		t.Fatalf("video frame instructions were not trimmed: %+v", kb.VideoConfig.VLMConfig)
	}
}

// The video page's frame-description prompt is its own field: it must be
// length-checked like every other user-authored instruction, and a knowledge
// base with no video section must not be affected.
func TestValidateKnowledgeBasePromptInstructionsCoversVideoFrames(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", MaxCustomPromptInstructionsLength+1)
	withVideo := &KnowledgeBase{VideoConfig: VideoConfig{
		VLMConfig: &VLMConfig{CustomInstructions: long},
	}}
	err := ValidateKnowledgeBasePromptInstructions(withVideo)
	if err == nil || !strings.Contains(err.Error(), "video frame instructions") {
		t.Fatalf("expected a video frame instructions error, got %v", err)
	}

	atLimit := &KnowledgeBase{VideoConfig: VideoConfig{
		VLMConfig: &VLMConfig{CustomInstructions: long[:MaxCustomPromptInstructionsLength]},
	}}
	if err := ValidateKnowledgeBasePromptInstructions(atLimit); err != nil {
		t.Fatalf("a value at the limit must pass: %v", err)
	}
}
