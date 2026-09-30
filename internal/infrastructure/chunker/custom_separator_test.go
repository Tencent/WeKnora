package chunker

import (
	"strings"
	"testing"
)

func TestSplitByCustomSeparatorOnlyMode(t *testing.T) {
	text := "第一段内容A。\n\n第二行。\n======\n第二段内容B。\n======\n\n======\n第三段内容C，很长也没有关系因为只识别分隔符模式不裁剪长度，这里故意写一段超过 chunk size 的内容来验证不会被二次切分，预分块的边界由上游决定，平台原样保留。\n======\n"
	cfg := SplitterConfig{
		ChunkSize:           32, // deliberately tiny: must NOT cap segments in only-mode
		ChunkOverlap:        0,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d: %+v", len(chunks), chunks)
	}
	want := []string{"第一段内容A。\n\n第二行。", "第二段内容B。", "第三段内容C，很长也没有关系因为只识别分隔符模式不裁剪长度，这里故意写一段超过 chunk size 的内容来验证不会被二次切分，预分块的边界由上游决定，平台原样保留。"}
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

func TestSplitByCustomSeparatorStripsMarkerAndWhitespace(t *testing.T) {
	text := "  alpha  \n======\n  beta  "
	chunks := Split(text, SplitterConfig{CustomSeparator: "======", CustomSeparatorOnly: true})
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Content != "alpha" || chunks[1].Content != "beta" {
		t.Fatalf("whitespace/separator not stripped: %q %q", chunks[0].Content, chunks[1].Content)
	}
}

func TestSplitByCustomSeparatorMixedMode(t *testing.T) {
	// In mixed mode each segment goes through the regular chain: an
	// oversized segment is further split, a short one is kept whole.
	long := strings.Repeat("这是一个比较长的段落。", 60) // ~600 runes > chunk size
	text := "短段。======\n" + long + "\n======\n另一个短段。"
	cfg := SplitterConfig{
		ChunkSize:       64,
		ChunkOverlap:    0,
		Separators:      []string{"\n\n", "\n", "。"},
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

func TestSplitByCustomSeparatorOverridesStrategy(t *testing.T) {
	// A heading/heuristic strategy must not override the custom separator.
	text := "# 标题\n\n段落一。\n======\n段落二。"
	cfg := SplitterConfig{
		Strategy:            StrategyHeading,
		CustomSeparator:     "======",
		CustomSeparatorOnly: true,
	}
	chunks := Split(text, cfg)
	if len(chunks) != 2 {
		t.Fatalf("custom separator must take precedence over strategy, got %d chunks", len(chunks))
	}
}

func TestSplitByCustomSeparatorNotConfigured(t *testing.T) {
	// Empty custom separator: behaviour must be identical to before.
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
	_, diag := SplitWithDiagnostics(text, SplitterConfig{CustomSeparator: "======", CustomSeparatorOnly: true})
	if diag == nil || diag.SelectedTier != TierCustom {
		t.Fatalf("diagnostics tier = %+v, want custom", diag)
	}
}

func TestSplitParentChildCustomSeparatorFlattens(t *testing.T) {
	text := "父段一。\n======\n父段二。"
	res := SplitParentChild(text,
		SplitterConfig{CustomSeparator: "======", CustomSeparatorOnly: true, ChunkSize: 512},
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
	chunks := Split(text, SplitterConfig{CustomSeparator: "======", CustomSeparatorOnly: true})
	if len(chunks) != 2 || chunks[0].Content != "a" || chunks[1].Content != "b" {
		t.Fatalf("adjacent markers mishandled: %+v", chunks)
	}
}

func TestCustomSeparatorMultibyteMarker(t *testing.T) {
	text := "第一块<::CHUNK::>第二块<::CHUNK::>第三块"
	chunks := Split(text, SplitterConfig{CustomSeparator: "<::CHUNK::>", CustomSeparatorOnly: true})
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
