package web_search

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEmptyTestResultsError_SearxngWithUnresponsiveEngines(t *testing.T) {
	provider := &SearxngProvider{
		lastUnresponsive: [][]string{
			{"google", "timeout"},
			{"bing", "blocked"},
		},
	}
	err := EmptyTestResultsError(string(types.WebSearchProviderTypeSearxng), provider)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "searxng returned 0 results") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if !strings.Contains(msg, "google (timeout)") || !strings.Contains(msg, "bing (blocked)") {
		t.Fatalf("expected unresponsive engine details, got: %q", msg)
	}
}

func TestEmptyTestResultsError_SearxngWithoutDiagnostics(t *testing.T) {
	provider := &SearxngProvider{}
	err := EmptyTestResultsError(string(types.WebSearchProviderTypeSearxng), provider)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "JSON format is enabled in settings.yml") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if strings.Contains(msg, "API key") {
		t.Fatalf("searxng message must not mention API key: %q", msg)
	}
}

func TestEmptyTestResultsError_DuckDuckGo(t *testing.T) {
	err := EmptyTestResultsError(string(types.WebSearchProviderTypeDuckDuckGo), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "duckduckgo returned 0 results") {
		t.Fatalf("unexpected message: %q", err.Error())
	}
}

func TestEmptyTestResultsError_APIKeyProviders(t *testing.T) {
	err := EmptyTestResultsError(string(types.WebSearchProviderTypeBing), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Fatalf("bing message should mention API key: %q", err.Error())
	}
}

func TestFormatUnresponsiveEngines(t *testing.T) {
	got := formatUnresponsiveEngines([][]string{
		{"google", "timeout"},
		{"wikipedia"},
	})
	want := "unresponsive engines: google (timeout), wikipedia"
	if got != want {
		t.Fatalf("formatUnresponsiveEngines() = %q, want %q", got, want)
	}
}
