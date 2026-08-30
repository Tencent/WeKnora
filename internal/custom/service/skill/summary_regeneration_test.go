package skill

import (
	"strings"
	"testing"
)

func TestMarkExplicitSummaryRegenerationPreservesPayload(t *testing.T) {
	marked, err := MarkExplicitSummaryRegeneration(`{"transcript_generation":"generation-1"}`)
	if err != nil {
		t.Fatalf("MarkExplicitSummaryRegeneration returned error: %v", err)
	}
	if !IsExplicitSummaryRegeneration(marked) || !strings.Contains(marked, "generation-1") {
		t.Fatalf("marked payload = %q", marked)
	}
}

func TestIsExplicitSummaryRegenerationRejectsMissingOrMalformedPayload(t *testing.T) {
	for _, payload := range []string{"", "not-json", `{"explicit_summary_regeneration":false}`} {
		if IsExplicitSummaryRegeneration(payload) {
			t.Fatalf("payload was incorrectly marked: %q", payload)
		}
	}
}
