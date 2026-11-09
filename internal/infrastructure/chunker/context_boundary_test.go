package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A fenced prompt block was preserved as one 2734-token input despite a
// 500-character target, exceeding the configured embedding context.
func TestSplitProtectedContentRespectsConfiguredBudget(t *testing.T) {
	for _, strategy := range []string{StrategyLegacy, StrategyAuto} {
		for _, body := range []string{
			"```text\n" + strings.Repeat("知识库权限隔离与并发验收。\n", 350) + "```",
			"![附件](" + strings.Repeat("long-path/", 450) + ")",
		} {
			t.Run(strategy+body[:3], func(t *testing.T) {
				chunks := Split(body, SplitterConfig{Strategy: strategy, ChunkSize: 500, ChunkOverlap: 60})
				covered := make([]bool, utf8.RuneCountInString(body))
				source := []rune(body)
				for _, c := range chunks {
					if utf8.RuneCountInString(c.Content) > 1000 {
						t.Fatalf("protected content bypassed budget: %d runes", utf8.RuneCountInString(c.Content))
					}
					if c.Start < 0 || c.End > len(source) || c.Content != string(source[c.Start:c.End]) {
						t.Fatal("source offsets changed")
					}
					for i := c.Start; i < c.End; i++ {
						covered[i] = true
					}
				}
				for i, ok := range covered {
					if !ok {
						t.Fatalf("source rune %d was dropped", i)
					}
				}
			})
		}
	}
}
