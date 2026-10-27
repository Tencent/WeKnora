package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSplitSentences(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"", nil},
		{" \n\t ", nil},
		{"One. Two. Three. Four", []string{"One", "Two", "Three", "Four"}},
		{"第一句。第二句。第三句。", []string{"第一句", "第二句", "第三句"}},
		{" One。 Two. Third 。", []string{"One", "Two", "Third"}},
		{".。One..。Two。", []string{"One", "Two"}},
		{" single sentence ", []string{"single sentence"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			require.Equal(t, tc.want, splitSentences(tc.text))
		})
	}
}

func TestTextMetricsIncludeEverySentence(t *testing.T) {
	input := &types.MetricInput{GeneratedTexts: "alpha.gamma", GeneratedGT: "alpha.beta"}
	for _, tc := range []struct {
		name  string
		score float64
	}{
		{"BLEU-1", NewBLEUMetric(false, BLEU1Gram).Compute(input)},
		{"ROUGE-1 precision", NewRougeMetric(false, "rouge-1", "p").Compute(input)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, 0.5, tc.score, 1e-9)
		})
	}
}
