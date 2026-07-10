package embedding

import (
	"context"
	"strings"
	"testing"
)

func TestUsageCapturePropagatesThroughNestedDecorators(t *testing.T) {
	outerCtx, outer := WithUsageCapture(context.Background())
	innerCtx, inner := WithUsageCapture(outerCtx)

	reportTokenUsage(innerCtx, 12, 12, "provider:test")
	reportTokenUsage(innerCtx, 8, 8, "provider:test")

	for name, capture := range map[string]*UsageCapture{"outer": outer, "inner": inner} {
		usage := capture.Usage()
		if usage.InputTokens != 20 || usage.TotalTokens != 20 || usage.Source != "provider:test" {
			t.Fatalf("%s capture = %+v, want 20 provider tokens", name, usage)
		}
	}
}

func TestEstimateEmbeddingUsageDoesNotUndercountCJKAsRunesDividedByFour(t *testing.T) {
	text := strings.Repeat("中", 100)
	usage := estimateEmbeddingUsage([]string{text}, "unknown-embedding-model")
	if usage.InputTokens <= 25 {
		t.Fatalf("CJK estimate = %d, expected more than the old rune_count/4 estimate", usage.InputTokens)
	}
	if usage.InputTokens != usage.TotalTokens {
		t.Fatalf("input=%d total=%d", usage.InputTokens, usage.TotalTokens)
	}
}
