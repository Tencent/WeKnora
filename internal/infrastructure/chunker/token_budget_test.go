package chunker

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// assertChunkEmbeddingBudget verifies that an explicitly configured token
// limit is a hard cap for both the stored chunk and the text sent to the
// embedding model (ContextHeader plus Content).
func assertChunkEmbeddingBudget(t *testing.T, chunks []Chunk, budget int) {
	t.Helper()
	for i, c := range chunks {
		if got := runeLen(c.Content); got > budget {
			t.Errorf("chunk[%d] content: got %d runes, want <= %d", i, got, budget)
		}
		if got := utf8.RuneCountInString(c.EmbeddingContent()); got > budget {
			t.Errorf("chunk[%d] embedding content: got %d runes, want <= %d", i, got, budget)
		}
	}
}

// assertChunkSourceSpans verifies the source-offset invariant, including
// synthetic table headers prepended to Content.
func assertChunkSourceSpans(t *testing.T, chunks []Chunk, text string) {
	t.Helper()
	source := []rune(text)
	for i, c := range chunks {
		content := []rune(c.Content)
		spanLen := c.End - c.Start
		if spanLen < 0 || spanLen > len(content) {
			t.Errorf("chunk[%d]: invalid span [%d,%d) for %d content runes", i, c.Start, c.End, len(content))
			continue
		}
		if c.Start < 0 || c.End > len(source) {
			t.Errorf("chunk[%d]: span [%d,%d) outside source length %d", i, c.Start, c.End, len(source))
			continue
		}
		prefixLen := len(content) - spanLen
		if got, want := string(content[prefixLen:]), string(source[c.Start:c.End]); got != want {
			t.Errorf("chunk[%d]: content suffix does not match source span:\n  got:  %q\n  want: %q",
				i, got, want)
		}
	}
}

func TestSplitText_ExplicitTokenLimitCapsSeparatorlessText(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	text := strings.Repeat("中", 1000)
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: tokenLimit,
		Languages:  []string{LangChinese},
		Separators: []string{"\n\n", "\n", "。"},
	}

	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for separator-free text, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, text)
	if restored := restoreTextFromChunks(chunks); restored != text {
		t.Fatalf("restoration mismatch: got %d runes, want %d", runeLen(restored), runeLen(text))
	}
}

func TestSplitText_ExplicitTokenLimitCapsProtectedCodeBlock(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	code := "```go\n" + strings.Repeat("fmt.Println(\"chunk\")\n", 80) + "```"
	text := "前言\n\n" + code + "\n\n结语"
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: tokenLimit,
		Languages:  []string{LangChinese},
		Separators: []string{"\n\n", "\n"},
	}

	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for oversized code block, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, text)
	if restored := restoreTextFromChunks(chunks); restored != text {
		t.Fatalf("restoration mismatch: got %d runes, want %d", runeLen(restored), runeLen(text))
	}
}

func TestSplitText_ExplicitTokenLimitCapsProtectedTable(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	var sb strings.Builder
	sb.WriteString("| 名称 | 描述 |\n| --- | --- |\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "| 行%d | %s |\n", i, strings.Repeat("数据", 20))
	}
	text := sb.String()
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: tokenLimit,
		Languages:  []string{LangChinese},
		Separators: []string{"\n\n", "\n"},
	}

	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for oversized table, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, text)
	if restored := restoreTextFromChunks(chunks); restored != text {
		t.Fatalf("restoration mismatch: got %d runes, want %d", runeLen(restored), runeLen(text))
	}
}

func TestSplit_ExplicitTokenLimitCapsEveryStrategy(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	text := "# 第一章 总览\n\n" +
		strings.Repeat("这是一段没有分隔符的连续中文内容用于验证预算。", 30) + "\n\n" +
		"## 1.1 代码示例\n\n```go\n" +
		strings.Repeat("fmt.Println(\"long protected code block\")\n", 40) +
		"```\n\n" +
		"| 列A | 列B |\n| --- | --- |\n" +
		strings.Repeat("| 数据A | 数据B |\n", 40)

	strategies := []string{
		StrategyLegacy,
		StrategyRecursive,
		StrategyHeading,
		StrategyHeuristic,
		StrategyAuto,
	}
	for _, strategy := range strategies {
		t.Run(strategy, func(t *testing.T) {
			cfg := SplitterConfig{
				ChunkSize:    10000,
				ChunkOverlap: 80,
				TokenLimit:   tokenLimit,
				Languages:    []string{LangChinese},
				Strategy:     strategy,
				Separators:   []string{"\n\n", "\n", "。"},
			}
			chunks := Split(text, cfg)
			if len(chunks) < 2 {
				t.Fatalf("expected multiple chunks, got %d", len(chunks))
			}
			assertChunkEmbeddingBudget(t, chunks, budget)
			assertChunkSourceSpans(t, chunks, text)
			if restored := restoreTextFromChunks(chunks); restored != text {
				t.Fatalf("restoration mismatch: got %d runes, want %d", runeLen(restored), runeLen(text))
			}
		})
	}
}

func TestSplit_ExplicitTokenLimitCountsHeadingOverhead(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	heading := "# " + strings.Repeat("标题", 40)
	text := heading + "\n" + strings.Repeat("正文内容", 120) + "\n# 结尾\n短正文"
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: tokenLimit,
		Languages:  []string{LangChinese},
		Strategy:   StrategyHeading,
		Separators: []string{"\n\n", "\n", "。"},
	}

	chunks := Split(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.ContextHeader == "" {
			t.Errorf("chunk[%d] lost its heading context (len=%d content=%q)",
				i, runeLen(c.Content), c.Content)
		}
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, text)
}

func TestSplitParentChild_ExplicitTokenLimitCapsEveryChild(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	text := "# 文档标题\n\n" +
		strings.Repeat("没有分隔符的连续中文内容用于父子分块验证。", 25) + "\n\n" +
		"```go\n" + strings.Repeat("fmt.Println(\"child token budget\")\n", 40) + "```\n\n" +
		"# 第二节\n\n" +
		"| 列A | 列B |\n| --- | --- |\n" +
		strings.Repeat("| 数据A | 数据B |\n", 30)

	parentCfg := SplitterConfig{
		ChunkSize:    4096,
		ChunkOverlap: 0,
		Strategy:     StrategyLegacy,
		Separators:   []string{"\n\n", "\n", "。"},
	}
	for _, strategy := range []string{StrategyLegacy, StrategyHeading, StrategyAuto} {
		childCfg := SplitterConfig{
			ChunkSize:    1024,
			ChunkOverlap: 50,
			TokenLimit:   tokenLimit,
			Languages:    []string{LangChinese},
			Strategy:     strategy,
			Separators:   []string{"\n\n", "\n", "。"},
		}
		results := map[string]ParentChildResult{
			"strategy": SplitParentChild(text, parentCfg, childCfg),
			"legacy":   SplitTextParentChild(text, parentCfg, childCfg),
		}
		for name, result := range results {
			t.Run(strategy+"/"+name, func(t *testing.T) {
				if len(result.Children) == 0 {
					t.Fatal("expected child chunks")
				}
				children := make([]Chunk, 0, len(result.Children))
				for _, child := range result.Children {
					children = append(children, child.Chunk)
				}
				assertChunkEmbeddingBudget(t, children, budget)
				assertChunkSourceSpans(t, children, text)
			})
		}
	}
}

func TestSplitWithDiagnostics_ExplicitTokenLimitCapsOutput(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	text := "# 第一节\n\n" + strings.Repeat("没有分隔符的长段落内容", 80) +
		"\n\n# 第二节\n\n" + strings.Repeat("另一段没有分隔符的内容", 80)
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: tokenLimit,
		Languages:  []string{LangChinese},
		Strategy:   StrategyAuto,
		Separators: []string{"\n\n", "\n", "。"},
	}

	chunks, diag := SplitWithDiagnostics(text, cfg)
	if diag == nil || diag.SelectedTier == "" {
		t.Fatal("expected diagnostics with a selected tier")
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, text)
}

func TestSplitText_ExplicitTokenLimitWithOverlapStaysWithinBudget(t *testing.T) {
	const tokenLimit = 200
	budget := CharsForTokenLimit(tokenLimit, LangChinese)
	text := strings.Repeat("甲", 1000)
	cfg := SplitterConfig{
		ChunkSize:    10000,
		ChunkOverlap: 200,
		TokenLimit:   tokenLimit,
		Languages:    []string{LangChinese},
		Separators:   []string{"\n"},
	}

	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	for i := 1; i < len(chunks); i++ {
		overlap := chunks[i-1].End - chunks[i].Start
		if overlap < 0 || overlap > cfg.ChunkOverlap {
			t.Fatalf("chunk pair %d overlap = %d, want in [0, %d]", i, overlap, cfg.ChunkOverlap)
		}
	}
	assertChunkSourceSpans(t, chunks, text)
}

func TestEnforceChunkBudget_SyntheticTableHeaderBeyondBudgetKeepsSourceOffsets(t *testing.T) {
	header := "| A | B |\n| --- | --- |\n"
	source := strings.Repeat("数据", 20)
	budget := runeLen(header) / 2
	chunks := enforceChunkBudget([]Chunk{{
		Content: header + source,
		Start:   0,
		End:     runeLen(source),
	}}, budget)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, budget)
	assertChunkSourceSpans(t, chunks, source)
	for i, c := range chunks {
		if strings.Contains(c.Content, "| A | B |") {
			t.Errorf("chunk[%d] kept a synthetic table header that does not fit the budget", i)
		}
	}
}

func TestEnforceChunkBudget_OneRuneBudget(t *testing.T) {
	source := "中文🙂"
	chunks := enforceChunkBudget([]Chunk{{
		Content: source,
		Start:   0,
		End:     runeLen(source),
	}}, 1)

	if len(chunks) != runeLen(source) {
		t.Fatalf("expected %d one-rune chunks, got %d", runeLen(source), len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, 1)
	assertChunkSourceSpans(t, chunks, source)
}

func TestEnforceChunkBudget_SyntheticOnlyChunkIsNotMappedToSource(t *testing.T) {
	chunks := enforceChunkBudget([]Chunk{{
		Content: strings.Repeat("表头", 20),
		Start:   7,
		End:     7,
	}}, 5)

	if len(chunks) != 0 {
		t.Fatalf("synthetic-only chunk must not be attributed to source offsets, got %d chunks", len(chunks))
	}
}

func TestEnforceChunkBudget_ContextHeaderConsumesBudget(t *testing.T) {
	source := strings.Repeat("正文", 50)
	chunks := enforceChunkBudget([]Chunk{{
		Content:       source,
		ContextHeader: strings.Repeat("标", 100),
		Start:         0,
		End:           runeLen(source),
	}}, 20)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, 20)
	assertChunkSourceSpans(t, chunks, source)
	for i, c := range chunks {
		if c.ContextHeader != "" {
			t.Errorf("chunk[%d] kept a context header that leaves no room for content", i)
		}
	}
}

func TestSplitText_MinimumTokenBudgetOneRune(t *testing.T) {
	text := "中文🙂"
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: 1,
		Languages:  []string{LangChinese},
		Separators: []string{"\n"},
	}
	chunks := SplitText(text, cfg)
	if len(chunks) != runeLen(text) {
		t.Fatalf("expected %d one-rune chunks, got %d", runeLen(text), len(chunks))
	}
	assertChunkEmbeddingBudget(t, chunks, 1)
	assertChunkSourceSpans(t, chunks, text)
}

func TestSplitText_NoTokenLimitKeepsExistingOversizeBehavior(t *testing.T) {
	text := strings.Repeat("中", 1000)
	cfg := SplitterConfig{
		ChunkSize:  10000,
		TokenLimit: 0,
		Separators: []string{"\n"},
	}
	chunks := SplitText(text, cfg)
	if len(chunks) != 1 {
		t.Fatalf("unconfigured TokenLimit behavior changed: got %d chunks, want 1", len(chunks))
	}
}
