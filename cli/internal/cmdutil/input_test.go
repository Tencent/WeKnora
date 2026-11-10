package cmdutil

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseTimeFlag(t *testing.T) {
	// Empty → unset (nil, nil).
	if got, err := ParseTimeFlag("--before", ""); err != nil || got != nil {
		t.Errorf(`empty: got (%v, %v), want (nil, nil)`, got, err)
	}

	// Valid RFC3339 → parsed time.
	want, _ := time.Parse(time.RFC3339, "2026-06-01T00:00:00Z")
	got, err := ParseTimeFlag("--before", "2026-06-01T00:00:00Z")
	if err != nil || got == nil || !got.Equal(want) {
		t.Errorf("valid: got (%v, %v)", got, err)
	}

	// Malformed → CodeInputInvalidArgument naming the label.
	_, err = ParseTimeFlag("start_time", "2026-06-01")
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeInputInvalidArgument {
		t.Fatalf("malformed: expected CodeInputInvalidArgument, got %v", err)
	}
	if !strings.Contains(typed.Message, "start_time") || !strings.Contains(typed.Message, "RFC3339") {
		t.Errorf("message %q missing label/RFC3339 hint", typed.Message)
	}
}

func TestValidateEnum(t *testing.T) {
	allowed := []string{"keyword", "vector", "hybrid"}

	// Empty value is "unset" — allowed, returns "".
	if got, err := ValidateEnum("mode", "", allowed); err != nil || got != "" {
		t.Errorf(`empty: got (%q, %v), want ("", nil)`, got, err)
	}

	// Exact and case-insensitive matches return the canonical spelling.
	for _, in := range []string{"hybrid", "Hybrid", "HYBRID"} {
		got, err := ValidateEnum("mode", in, allowed)
		if err != nil || got != "hybrid" {
			t.Errorf(`%q: got (%q, %v), want ("hybrid", nil)`, in, got, err)
		}
	}

	// Canonical spelling comes from `allowed`, not the input casing.
	if got, _ := ValidateEnum("type", "embedding", []string{"Embedding", "Rerank"}); got != "Embedding" {
		t.Errorf(`canonical: got %q, want "Embedding"`, got)
	}

	// Unknown value → CodeInputInvalidArgument naming the flag + valid set.
	_, err := ValidateEnum("mode", "hybird", allowed)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeInputInvalidArgument {
		t.Fatalf("unknown: expected CodeInputInvalidArgument, got %v", err)
	}
	for _, want := range []string{"--mode", "keyword | vector | hybrid", `"hybird"`} {
		if !strings.Contains(typed.Message, want) {
			t.Errorf("message %q missing %q", typed.Message, want)
		}
	}
}
