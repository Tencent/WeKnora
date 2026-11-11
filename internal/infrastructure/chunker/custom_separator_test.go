package chunker

import (
	"strings"
	"testing"
)

func TestSplitByCustomSeparatorOnlyMode(t *testing.T) {
	// Marker-only mode: segments within the budget map 1:1; the budget
	// comes from ChunkSize (TokenLimit unset). Segments longer than the
	// budget are split further so embedding inputs stay within limits.
	text := "第一段内容A。\n\n第二行。\n======\n第二段内容B。\n======\n\n======\n第三段内容C。" +
		"这一段故意写一段超过 chunk size 的长内容，验证 marker-only 模式会按预算二次切分，" +
		"预分块的边界在预算内仍然由上游决定，平台原样保留。\n======\n"
	cfg := SplitterConfig{
		ChunkSize:           512, // budget: every segment below it stays whole
		ChunkOverlap:        0,
		Strategy:            StrategyCustomSeparator,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d: %+v", len(chunks), chunks)
	}
	want := []string{
		"第一段内容A。\n\n第二行。",
		"第二段内容B。",
		"第三段内容C。这一段故意写一段超过 chunk size 的长内容，" +
			"验证 marker-only 模式会按预算二次切分，" +
			"预分块的边界在预算内仍然由上游决定，平台原样保留。",
	}
	for i, w := range want {
		if chunks[i].Content != w {
			t.Errorf("chunk %d:\n got %q\nwant %q", i, chunks[i].Content, w)
		}
		if strings.Contains(chunks[i].Content, "======") {
			t.Errorf("chunk %d still contains the separator", i)
		}
		if chunks[i].Seq != i {
			t.Errorf("chunk %d Seq = %d", i, chunks[i].Seq)
		}
	}
	// Position invariant: End-Start == rune count of Content, and content
	// matches the original text slice.
	runes := []rune(text)
	for i, c := range chunks {
		if c.End-c.Start != runeLen(c.Content) {
			t.Errorf("chunk %d violates position invariant: span=%d len=%d", i, c.End-c.Start, runeLen(c.Content))
		}
		if string(runes[c.Start:c.End]) != c.Content {
			t.Errorf("chunk %d content does not match source span", i)
		}
	}
}

func TestCustomSeparatorOnlyModeOversizedWithoutTokenLimit(t *testing.T) {
	// TokenLimit == 0: the budget is ChunkSize in characters. An oversized
	// segment must be split further (the embedding batch layer would
	// otherwise reject the document), with positions rebased globally.
	seg := strings.Repeat("长", 40) // 40 runes > budget 16
	text := "短。======\n" + seg + "\n======\n尾。"
	cfg := SplitterConfig{
		ChunkSize:           16,
		ChunkOverlap:        0,
		Strategy:            StrategyCustomSeparator,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	if len(chunks) != 5 { // 2 boundary segments + ceil(40/16)=3 windows
		t.Fatalf("oversized segment should be split into 3, got %d chunks", len(chunks))
	}
	if chunks[0].Content != "短。" || chunks[len(chunks)-1].Content != "尾。" {
		t.Fatalf("boundary segments altered: %q %q", chunks[0].Content, chunks[len(chunks)-1].Content)
	}
	for _, c := range chunks {
		if strings.Contains(c.Content, "======") {
			t.Fatalf("separator leaked into budget-split chunk: %q", c.Content)
		}
		if runeLen(c.Content) > 16+1 { // legacy splitter may slightly round; hard cap ~budget
			t.Fatalf("budget split exceeded the cap: %d runes", runeLen(c.Content))
		}
	}
	runes := []rune(text)
	for i, c := range chunks {
		if c.End-c.Start != runeLen(c.Content) {
			t.Errorf("chunk %d violates position invariant", i)
		}
		if string(runes[c.Start:c.End]) != c.Content {
			t.Errorf("chunk %d content does not match source span", i)
		}
	}
}

func TestCustomSeparatorOnlyModeOversizedWithTokenLimit(t *testing.T) {
	// TokenLimit set: the budget is the approximate character conversion of
	// the token limit, and it can tighten ChunkSize.
	seg := strings.Repeat("字", 300)
	text := seg + "\n======\n尾。"
	cfg := SplitterConfig{
		ChunkSize:           4000, // far above the token-derived budget
		ChunkOverlap:        0,
		Strategy:            StrategyCustomSeparator,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
		TokenLimit:          64, // zh conversion well below 300 chars
		Languages:           []string{"zh"},
	}
	chunks := Split(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("oversized segment should be split under the token budget, got %d", len(chunks))
	}
	runes := []rune(text)
	for i, c := range chunks {
		if c.End-c.Start != runeLen(c.Content) {
			t.Errorf("chunk %d violates position invariant", i)
		}
		if string(runes[c.Start:c.End]) != c.Content {
			t.Errorf("chunk %d content does not match source span", i)
		}
	}
}

func TestCustomSeparatorOnlyModeEmbeddingCharLimit(t *testing.T) {
	// EmbeddingCharLimit (vendor per-input cap) tightens the budget further,
	// leaving room for title/context prepended by the ingestion pipeline.
	seg := strings.Repeat("x", 400)
	text := seg + "\n======\n尾。"
	cfg := SplitterConfig{
		ChunkSize:           512,
		ChunkOverlap:        0,
		Strategy:            StrategyCustomSeparator,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
		EmbeddingCharLimit:  512, // minus reserve → budget ≈ 256 < 400
	}
	chunks := Split(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("embedding limit should tighten the budget, got %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if runeLen(c.Content) > 512-embeddingInputReserveChars {
			t.Fatalf("chunk over embedding budget: %d", runeLen(c.Content))
		}
	}
}

func TestCustomSeparatorOnlyModeBudgetDisabledWhenUnset(t *testing.T) {
	// Budget <= 0 keeps the historical "trust the marker verbatim" shape
	// (no EmbeddingCharLimit, generous ChunkSize).
	text := strings.Repeat("a", 300) + "\n======\n" + strings.Repeat("b", 300)
	cfg := SplitterConfig{
		ChunkSize:           100000,
		ChunkOverlap:        0,
		Strategy:            StrategyCustomSeparator,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	if len(chunks) != 2 {
		t.Fatalf("segments under budget must stay whole, got %d", len(chunks))
	}
}

func TestSplitByCustomSeparatorStripsMarkerAndWhitespace(t *testing.T) {
	text := "  alpha  \n======\n  beta  "
	chunks := Split(text, SplitterConfig{
		Strategy: StrategyCustomSeparator, CustomSeparator: "======", CustomSeparatorOnly: true,
	})
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Content != "alpha" || chunks[1].Content != "beta" {
		t.Fatalf("whitespace/separator not stripped: %q %q", chunks[0].Content, chunks[1].Content)
	}
}

func TestSplitByCustomSeparatorMixedMode(t *testing.T) {
	// Mixed mode ("split further" option): each segment is refined by the
	// legacy recursive splitter — an oversized segment is further split, a
	// short one is kept whole. Marker boundaries remain authoritative.
	long := strings.Repeat("这是一个比较长的段落。", 60) // ~600 runes > chunk size
	text := "短段。======\n" + long + "\n======\n另一个短段。"
	cfg := SplitterConfig{
		ChunkSize:       64,
		ChunkOverlap:    0,
		Separators:      []string{"\n\n", "\n", "。"},
		Strategy:        StrategyCustomSeparator,
		CustomSeparator: "======",
	}
	chunks := Split(text, cfg)
	if len(chunks) < 3 {
		t.Fatalf("mixed mode should refine the long segment, got %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if strings.Contains(c.Content, "======") {
			t.Fatalf("separator leaked into chunk: %q", c.Content)
		}
	}
	if chunks[0].Content != "短段。" {
		t.Errorf("first chunk = %q, want 短段。", chunks[0].Content)
	}
	// Position invariant still holds globally after rebasing.
	runes := []rune(text)
	for i, c := range chunks {
		if c.End-c.Start != runeLen(c.Content) {
			t.Errorf("chunk %d violates position invariant", i)
		}
		if string(runes[c.Start:c.End]) != c.Content {
			t.Errorf("chunk %d content does not match source span", i)
		}
	}
}

func TestCustomSeparatorRequiresStrategy(t *testing.T) {
	// Without the custom_separator strategy the marker is inert: the
	// heading strategy runs and the marker stays in the text.
	text := "# 标题\n\n段落一。\n======\n段落二。"
	cfg := SplitterConfig{
		ChunkSize:           512,
		Strategy:            StrategyHeading,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	for _, c := range chunks {
		if !strings.Contains(c.Content, "======") {
			// The heading splitter may legitimately place the marker in
			// either chunk; the assertion is that no marker-split happened.
			continue
		}
		return
	}
	if len(chunks) == 2 && chunks[0].Content == "# 标题\n\n段落一。" {
		t.Fatalf("marker split ran without the custom_separator strategy")
	}
}

func TestCustomSeparatorLegacyConfigMigration(t *testing.T) {
	// Configs saved before the strategy value existed carry the marker with
	// an empty strategy; ensureDefaults migrates them so the marker keeps
	// working.
	text := "旧配置一。\n======\n旧配置二。"
	chunks := Split(text, SplitterConfig{CustomSeparator: "======", CustomSeparatorOnly: true})
	if len(chunks) != 2 || chunks[0].Content != "旧配置一。" || chunks[1].Content != "旧配置二。" {
		t.Fatalf("legacy marker config not migrated: %+v", chunks)
	}
}

func TestCustomSeparatorStrategyWithoutMarker(t *testing.T) {
	// The strategy without a marker falls through to the ordinary chain
	// (empty text chunks normally); it must not crash or loop.
	text := "甲。\n\n乙。"
	chunks := Split(text, SplitterConfig{
		ChunkSize: 512, Separators: []string{"\n\n", "\n", "。"}, Strategy: StrategyCustomSeparator,
	})
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
}

func TestSplitByCustomSeparatorNotConfigured(t *testing.T) {
	// No marker, plain config: behaviour must be identical to before.
	text := "甲。\n\n乙。"
	got := Split(text, SplitterConfig{ChunkSize: 512, Separators: []string{"\n\n", "\n", "。"}})
	if len(got) == 0 {
		t.Fatal("no chunks")
	}
	for _, c := range got {
		if c.Start != 0 && c.End != 0 && c.End-c.Start != runeLen(c.Content) {
			t.Errorf("position invariant broken without custom separator")
		}
	}
}

func TestSplitWithDiagnosticsCustomTier(t *testing.T) {
	text := "一\n======\n二"
	_, diag := SplitWithDiagnostics(text, SplitterConfig{
		Strategy: StrategyCustomSeparator, CustomSeparator: "======", CustomSeparatorOnly: true,
	})
	if diag == nil || diag.SelectedTier != TierCustom {
		t.Fatalf("diagnostics tier = %+v, want custom", diag)
	}
}

func TestSplitParentChildCustomSeparatorFlattens(t *testing.T) {
	text := "父段一。\n======\n父段二。"
	res := SplitParentChild(text,
		SplitterConfig{
			Strategy: StrategyCustomSeparator, CustomSeparator: "======",
			CustomSeparatorOnly: true, ChunkSize: 512,
		},
		SplitterConfig{ChunkSize: 64},
	)
	if len(res.Parents) != 2 {
		t.Fatalf("want 2 parents, got %d", len(res.Parents))
	}
	if res.Parents[0].Content != "父段一。" {
		t.Errorf("parent content = %q", res.Parents[0].Content)
	}
}

func TestCustomSeparatorAdjacentMarkers(t *testing.T) {
	// Adjacent / repeated markers produce empty segments that must be
	// dropped instead of yielding whitespace-only chunks.
	text := "a\n======\n======\n\n======\nb"
	chunks := Split(text, SplitterConfig{
		ChunkSize: 512, Strategy: StrategyCustomSeparator,
		CustomSeparator: "======", CustomSeparatorOnly: true,
	})
	if len(chunks) != 2 || chunks[0].Content != "a" || chunks[1].Content != "b" {
		t.Fatalf("adjacent markers mishandled: %+v", chunks)
	}
}

func TestCustomSeparatorMultibyteMarker(t *testing.T) {
	text := "第一块<::CHUNK::>第二块<::CHUNK::>第三块"
	chunks := Split(text, SplitterConfig{
		ChunkSize: 512, Strategy: StrategyCustomSeparator,
		CustomSeparator: "<::CHUNK::>", CustomSeparatorOnly: true,
	})
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(chunks))
	}
	runes := []rune(text)
	for i, c := range chunks {
		if string(runes[c.Start:c.End]) != c.Content {
			t.Errorf("chunk %d span mismatch with multibyte marker", i)
		}
	}
}
