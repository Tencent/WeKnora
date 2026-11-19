package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBLEUMetric_ComputeZeroPrecision(t *testing.T) {
	for _, tc := range []struct {
		name      string
		candidate string
		reference string
		smoothing bool
		weights   BLEUWeight
		want      float64
	}{
		{"zero bigram precision", "a b c d", "a d c b", false, BLEU2Gram, 0},
		{"zero trigram precision", "a b c d", "a b d c", false, BLEUWeight{0, 0, 1, 0}, 0},
		{"zero fourgram precision", "a b c d", "a b c e", false, BLEU4Gram, 0},
		{"short candidate", "a", "a", false, BLEU4Gram, 0},
		{"zero weight ignores missing orders", "a", "a", false, BLEU1Gram, 1},
		{"positive weighted precisions", "a b c", "a b d", false, BLEU2Gram, 0.5773502691896257},
		{"perfect match", "a b c d", "a b c d", false, BLEU4Gram, 1},
		{"no match", "a b c d", "e f g h", false, BLEU4Gram, 0},
		{"empty candidate", "", "a", false, BLEU1Gram, 0},
		{"empty reference", "a", "", false, BLEU1Gram, 0},
		{"add-one smoothing unchanged", "a b c d", "a d c b", true, BLEU2Gram, 0.5},
		{"smoothed short candidate unchanged", "a", "a", true, BLEU4Gram, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &types.MetricInput{GeneratedTexts: tc.candidate, GeneratedGT: tc.reference}
			got := NewBLEUMetric(tc.smoothing, tc.weights).Compute(input)
			require.InDelta(t, tc.want, got, 1e-9)
		})
	}
}
