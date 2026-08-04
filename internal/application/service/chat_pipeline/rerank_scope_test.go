package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRerankFallbackMinScoreForExplicitScope(t *testing.T) {
	if got := rerankFallbackMinScore(nil); got != 0.15 {
		t.Fatalf("default fallback minimum = %v, want 0.15", got)
	}

	targets := types.SearchTargets{{DisableRecallThresholds: true}}
	if got := rerankFallbackMinScore(targets); got != 0 {
		t.Fatalf("explicit-scope fallback minimum = %v, want 0", got)
	}
}
