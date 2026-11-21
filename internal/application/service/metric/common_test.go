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

func TestSplitIntoWords(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sentences []string
		want      []string
	}{
		{"nil", nil, nil},
		{"empty", []string{"", " \n\t"}, nil},
		{"words", []string{"Hello world_123! 42,foo."}, []string{"Hello", "world_123!", "42,foo."}},
		{"whitespace", []string{"go\tone", "two\nthree"}, []string{"go", "one", "two", "three"}},
		{"punctuation", []string{"A:B;C，D"}, []string{"A", ":", "B", ";", "C", "，", "D"}},
		{"mixed scripts", []string{"中A文!"}, []string{"中", "A", "文", "!"}},
		{"symbols", []string{"abc 😀 def + xyz"}, []string{"abc", "def", "xyz"}},
		{"sentence order", []string{"first", "second"}, []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, splitIntoWords(tc.sentences))
		})
	}
}

func BenchmarkSplitIntoWords(b *testing.B) {
	for _, tc := range []struct {
		name      string
		sentences []string
	}{
		{"empty", nil},
		{"English", []string{"The quick brown fox jumps over the lazy dog.", "Metric_123, test!"}},
		{"Chinese", []string{"中A文!", "测试指标"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				splitIntoWords(tc.sentences)
			}
		})
	}
}
