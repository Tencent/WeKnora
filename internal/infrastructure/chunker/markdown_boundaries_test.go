package chunker

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplit_LegacyMarkdownBoundaries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ChunkSize = 1536
	cfg.ChunkOverlap = 192

	const intro = "A paragraph that introduces the example."
	following := strings.TrimSpace(strings.Repeat("The explanation continues with more detail. ", 30))
	fence := "```go\n" + strings.Repeat("    total += weights[i] * values[i]\n", 38) + "```"
	formula := "$$\n" + strings.Repeat("f(x) = a x^2 + b x + c \\\\\n", 45) + "$$"

	var paragraphs []string
	for i := range 14 {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"Paragraph %d calls `parseEvents()` first, then `indexVariables()` on the result, and finally "+
				"`preProcess()` so that the event dates are read before anything else happens. See "+
				"[the repository](https://example.org/repo/%d) for the full source and "+
				"![the diagram](flow.png) for the steps.", i, i))
	}

	for _, tt := range []struct {
		name          string
		text          string
		wantSecond    string
		paragraphEnds bool
	}{
		{
			name:       "fenced code exceeds overlap",
			text:       intro + "\n\n" + fence + "\n\n" + following,
			wantSecond: following,
		},
		{
			name:       "display math exceeds overlap",
			text:       intro + "\n\n" + formula + "\n\n" + following,
			wantSecond: following,
		},
		{
			name:       "retain prose after a large block",
			text:       intro + "\n\n" + fence + "\n\nWhere the weights come from.\n\n" + following,
			wantSecond: "Where the weights come from.\n\n" + following,
		},
		{
			name:       "short formula fits in overlap",
			text:       strings.Repeat(intro+" ", 32) + "\n\n$$a=b+c$$\n\n" + following,
			wantSecond: "$$a=b+c$$\n\n" + following,
		},
		{
			name:          "inline code links and images stay in paragraphs",
			text:          strings.Join(paragraphs, "\n\n"),
			paragraphEnds: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			chunks := Split(tt.text, cfg)
			if len(chunks) < 2 {
				t.Fatalf("got %d chunks, want at least 2", len(chunks))
			}

			runes := []rune(tt.text)
			for i, c := range chunks {
				if c.Start < 0 || c.End > len(runes) || c.Start >= c.End ||
					c.Content != string(runes[c.Start:c.End]) {
					t.Fatalf("chunk %d does not match source range [%d,%d)", i, c.Start, c.End)
				}
				if i == 0 {
					continue
				}
				prev := chunks[i-1]
				if overlap := prev.End - c.Start; overlap < 0 || overlap > cfg.ChunkOverlap {
					t.Fatalf("chunk %d overlap = %d, want 0 to %d", i, overlap, cfg.ChunkOverlap)
				}
				if tt.paragraphEnds && prev.End < len(runes) && runes[prev.End-1] != '\n' && runes[prev.End] != '\n' {
					t.Fatalf("chunk %d ends mid-paragraph at rune %d, after %q",
						i-1, prev.End, string(runes[max(0, prev.End-30):prev.End]))
				}
			}
			if tt.wantSecond != "" && strings.TrimLeft(chunks[1].Content, "\n") != tt.wantSecond {
				got := strings.TrimLeft(chunks[1].Content, "\n")
				t.Fatalf("second chunk starts with %.60q, want %.60q", got, tt.wantSecond)
			}
		})
	}

	t.Run("configured separator inside inline code", func(t *testing.T) {
		text := "介绍。调用 `甲。乙` 即可。\n\n" + strings.Repeat("后续。", 8)
		chunks := Split(text, SplitterConfig{ChunkSize: 10, Separators: []string{"\n\n", "。"}})
		for _, c := range chunks {
			if strings.Contains(c.Content, "`甲。乙`") {
				return
			}
		}
		t.Fatal("configured separator split the inline code across chunks")
	})

	t.Run("overlap window begins inside inline code", func(t *testing.T) {
		text := "Intro `a long piece of code. still inside` concludes here. Tail\n\n" + strings.Repeat("x", 25)
		chunks := Split(text, SplitterConfig{ChunkSize: 65, ChunkOverlap: 40, Separators: cfg.Separators})
		if len(chunks) != 2 || !strings.HasPrefix(chunks[1].Content, "Tail\n\n") {
			t.Fatalf("expected the second chunk to start after the sentence, got %#v", chunks)
		}
	})

	t.Run("inline span fits hard size limit", func(t *testing.T) {
		const code = "`甲乙丙丁戊己庚辛壬癸`"
		tokenCfg := SplitterConfig{ChunkSize: 1536, TokenLimit: 20, Languages: []string{LangChinese}}
		for _, tt := range []struct {
			name  string
			cfg   SplitterConfig
			limit int
		}{
			{name: "token budget", cfg: tokenCfg, limit: tokenRuneBudget(tokenCfg)},
			{name: "absolute cap", cfg: cfg, limit: 7500},
		} {
			t.Run(tt.name, func(t *testing.T) {
				text := strings.Repeat("甲", tt.limit-5) + code + strings.Repeat("乙", tt.limit)
				var restored strings.Builder
				intact := false
				for _, c := range Split(text, tt.cfg) {
					if size := runeLen(c.Content); size > tt.limit {
						t.Fatalf("chunk size = %d, limit = %d", size, tt.limit)
					}
					intact = intact || strings.Contains(c.Content, code)
					restored.WriteString(c.Content)
				}
				if !intact {
					t.Fatal("hard size limit split inline code that fits in a chunk")
				}
				if restored.String() != text {
					t.Fatal("hard split lost source text")
				}
			})
		}
	})
}
