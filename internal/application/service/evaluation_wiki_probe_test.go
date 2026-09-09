package service

import (
	"strings"
	"testing"
)

func TestBuildWikiCacheProbePromptLayouts(t *testing.T) {
	optimizedA, prefixA, _, err := buildWikiCacheProbePrompt("optimized", 0)
	if err != nil {
		t.Fatal(err)
	}
	optimizedB, prefixB, _, err := buildWikiCacheProbePrompt("optimized", 1)
	if err != nil {
		t.Fatal(err)
	}
	if prefixA != prefixB {
		t.Fatal("optimized stable prefix changed between samples")
	}
	if strings.Index(optimizedA, "<instructions>") > strings.Index(optimizedA, "<chunks>") {
		t.Fatal("optimized rules must precede dynamic chunks")
	}
	legacy, _, _, err := buildWikiCacheProbePrompt("legacy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacy, "<chunks>") {
		t.Fatal("legacy layout must put dynamic chunks first")
	}
	if optimizedA == optimizedB {
		t.Fatal("probe samples must have different dynamic content")
	}
}

func TestBuildWikiCacheProbePromptRejectsUnboundedInput(t *testing.T) {
	if _, _, _, err := buildWikiCacheProbePrompt("custom", 0); err == nil {
		t.Fatal("custom layout should be rejected")
	}
	if _, _, _, err := buildWikiCacheProbePrompt("optimized", 4); err == nil {
		t.Fatal("out-of-range sample should be rejected")
	}
}

func TestValidateWikiCacheProbeOutput(t *testing.T) {
	valid, found := validateWikiCacheProbeOutput(
		`{"citations":{"entity/weknora":["c001"]},"new_slugs":[]}`,
		"entity/weknora",
	)
	if !valid || !found {
		t.Fatalf("valid=%v found=%v", valid, found)
	}
}
