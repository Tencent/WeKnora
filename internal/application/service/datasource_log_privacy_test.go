package service

import (
	"strings"
	"testing"
)

func TestSyncLogIdentifierRedactsSourceIdentifier(t *testing.T) {
	const sourceID = "private-workspace/document/123"

	got := syncLogIdentifier(sourceID)
	if got == sourceID || strings.Contains(got, sourceID) {
		t.Fatalf("source identifier leaked in log reference: %q", got)
	}
	if got != syncLogIdentifier(sourceID) {
		t.Fatalf("log reference must be stable, got %q", got)
	}
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("log reference must identify its hash scheme, got %q", got)
	}
	if got := syncLogIdentifier(""); got != "none" {
		t.Fatalf("empty source identifier = %q, want none", got)
	}
}
