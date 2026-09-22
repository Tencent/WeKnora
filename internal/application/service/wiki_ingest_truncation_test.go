package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
)

// TestGeneratePageRewriteRejectsTruncatedRewrite is the regression test for
// Tencent/WeKnora#3468: a page rewrite cut off by the token budget must
// surface as an error wrapping errWikiLLMTruncated so the batch write-back
// keeps the existing page instead of storing a silently shrunk one.
// Providers report the budget stop as length / max_tokens /
// max_output_tokens (verbatim, any case/padding) — all must be fatal here.
func TestGeneratePageRewriteRejectsTruncatedRewrite(t *testing.T) {
	truncatedReasons := []string{
		"length",
		"max_tokens",
		"max_output_tokens",
		" Length ",
		"MAX_TOKENS",
		"Max_Output_Tokens",
	}
	for _, reason := range truncatedReasons {
		model := &templateCaptureChatModel{
			response:     "# Title\ntruncated mid-sent",
			finishReason: reason,
		}
		service := &wikiIngestService{}

		got, err := service.generatePageRewrite(
			context.Background(),
			model,
			agent.WikiPageModifyUserPrompt,
			map[string]string{"ExistingContent": "x"},
		)
		if !errors.Is(err, errWikiLLMTruncated) {
			t.Fatalf("generatePageRewrite() with finish_reason=%q error = %v, want errWikiLLMTruncated", reason, err)
		}
		if !strings.Contains(err.Error(), "truncat") {
			t.Fatalf("error %q should mention truncation", err)
		}
		if got != "" {
			t.Fatalf("generatePageRewrite() with finish_reason=%q = %q, want empty content", reason, got)
		}
	}
}

// TestGeneratePageRewriteAcceptsStopFinishReason guards the happy path:
// a complete rewrite (finish_reason=stop) must still be returned as-is.
func TestGeneratePageRewriteAcceptsStopFinishReason(t *testing.T) {
	model := &templateCaptureChatModel{
		response:     "full content",
		finishReason: "stop",
	}
	service := &wikiIngestService{}

	got, err := service.generatePageRewrite(
		context.Background(),
		model,
		agent.WikiPageModifyUserPrompt,
		map[string]string{"ExistingContent": "x"},
	)
	if err != nil {
		t.Fatalf("generatePageRewrite() error = %v, want nil", err)
	}
	if got != "full content" {
		t.Fatalf("generatePageRewrite() = %q, want %q", got, "full content")
	}
}

// TestGenerateWithTemplateKeepsTruncatedSummary is the discriminating
// non-rewrite regression for the #3492 review: a summary cut off by the
// token budget must still be returned with a nil error through the shared
// path, so the document keeps its (slightly clipped) summary page instead
// of entering failedOps/retry/dead-letter handling.
func TestGenerateWithTemplateKeepsTruncatedSummary(t *testing.T) {
	model := &templateCaptureChatModel{
		response:     "headline\nclipped summary mid-sent",
		finishReason: "length",
	}
	service := &wikiIngestService{}

	got, err := service.generateWithTemplate(
		context.Background(),
		model,
		agent.WikiSummaryPrompt,
		map[string]string{
			"Content":            "x",
			"Language":           "English",
			"ExtractedSlugs":     "",
			"CustomInstructions": "",
			"InstructionScope":   "wiki_content",
		},
	)
	if err != nil {
		t.Fatalf("generateWithTemplate() error = %v for truncated summary, want nil", err)
	}
	if got != "headline\nclipped summary mid-sent" {
		t.Fatalf("generateWithTemplate() = %q, want truncated content kept", got)
	}
}

// TestIsWikiLLMTruncatedFinishReason pins the detection set to the
// isLengthFinishReason semantics: trim + case-insensitive, exactly length,
// max_tokens, max_output_tokens — nothing broader.
func TestIsWikiLLMTruncatedFinishReason(t *testing.T) {
	truncated := []string{"length", "max_tokens", "max_output_tokens", " Length ", "MAX_TOKENS", "Max_Output_Tokens"}
	for _, reason := range truncated {
		if !isWikiLLMTruncatedFinishReason(reason) {
			t.Fatalf("isWikiLLMTruncatedFinishReason(%q) = false, want true", reason)
		}
	}
	notTruncated := []string{"", "stop", "STOP", "tool_calls", "content_filter", "error", "lengthy"}
	for _, reason := range notTruncated {
		if isWikiLLMTruncatedFinishReason(reason) {
			t.Fatalf("isWikiLLMTruncatedFinishReason(%q) = true, want false", reason)
		}
	}
}
