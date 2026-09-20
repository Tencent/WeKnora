package service

import (
	"context"
	"strings"
	"testing"
)

// TestGenerateWithTemplateRejectsLengthTruncatedRewrite is the regression
// test for Tencent/WeKnora#3468: a page rewrite cut off by the token budget
// (finish_reason=length) must surface as an error so the batch write-back
// keeps the existing page instead of storing a silently shrunk one.
func TestGenerateWithTemplateRejectsLengthTruncatedRewrite(t *testing.T) {
	model := &templateCaptureChatModel{
		response:     "# Title\ntruncated mid-sent",
		finishReason: "length",
	}
	service := &wikiIngestService{}

	_, err := service.generateWithTemplate(
		context.Background(),
		model,
		`Content={{.Content}}`,
		map[string]string{"Content": "x"},
	)
	if err == nil {
		t.Fatal("generateWithTemplate() = nil error for finish_reason=length, want truncation error")
	}
	if !strings.Contains(err.Error(), "truncat") {
		t.Fatalf("error %q should mention truncation", err)
	}
}

// TestGenerateWithTemplateAcceptsStopFinishReason guards the happy path:
// a complete rewrite (finish_reason=stop) must still be returned as-is.
func TestGenerateWithTemplateAcceptsStopFinishReason(t *testing.T) {
	model := &templateCaptureChatModel{
		response:     "full content",
		finishReason: "stop",
	}
	service := &wikiIngestService{}

	got, err := service.generateWithTemplate(
		context.Background(),
		model,
		`Content={{.Content}}`,
		map[string]string{"Content": "x"},
	)
	if err != nil {
		t.Fatalf("generateWithTemplate() error = %v, want nil", err)
	}
	if got != "full content" {
		t.Fatalf("generateWithTemplate() = %q, want %q", got, "full content")
	}
}
