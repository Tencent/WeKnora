package chunker

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitText_BasicASCII(t *testing.T) {
	text := "Hello world. This is a test."
	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 0, Separators: []string{". "}}
	chunks := SplitText(text, cfg)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	combined := ""
	for _, c := range chunks {
		combined += c.Content
	}
	if combined != text {
		t.Errorf("combined content mismatch:\n  got:  %q\n  want: %q", combined, text)
	}
}

func TestSplitText_ChineseText_StartEndAreRuneOffsets(t *testing.T) {
	// Each Chinese character is 3 bytes in UTF-8 but 1 rune.
	// This test ensures Start/End are rune offsets, not byte offsets.
	text := "你好世界这是一个测试文本用于检验分割位置"
	runeCount := utf8.RuneCountInString(text)
	byteCount := len(text)
	if runeCount == byteCount {
		t.Fatal("test requires multi-byte characters")
	}

	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 0, Separators: []string{"\n"}}
	chunks := SplitText(text, cfg)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	c := chunks[0]
	if c.Start != 0 {
		t.Errorf("Start: got %d, want 0", c.Start)
	}
	if c.End != runeCount {
		t.Errorf("End: got %d, want %d (runeCount); byteCount would be %d",
			c.End, runeCount, byteCount)
	}
}

func TestSplitText_ChineseMultiChunk_StartEndConsistency(t *testing.T) {
	// Build a long Chinese text that will be split into multiple chunks.
	line := "这是一段中文内容用于测试分割功能是否正确。"
	text := strings.Repeat(line+"\n\n", 20)
	text = strings.TrimRight(text, "\n")

	cfg := SplitterConfig{ChunkSize: 30, ChunkOverlap: 5, Separators: []string{"\n\n", "\n", "。"}}
	chunks := SplitText(text, cfg)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	textRunes := []rune(text)
	for i, c := range chunks {
		contentRunes := []rune(c.Content)
		contentRuneLen := len(contentRunes)

		// End - Start must equal the rune length of the content
		spanLen := c.End - c.Start
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: End(%d) - Start(%d) = %d, but rune len of content = %d",
				i, c.End, c.Start, spanLen, contentRuneLen)
		}

		// Start must be non-negative and End must not exceed total rune count
		if c.Start < 0 {
			t.Errorf("chunk[%d]: Start is negative: %d", i, c.Start)
		}
		if c.End > len(textRunes) {
			t.Errorf("chunk[%d]: End %d exceeds total rune count %d", i, c.End, len(textRunes))
		}

		// Content from rune slice must match the chunk content
		if c.Start >= 0 && c.End <= len(textRunes) {
			sliced := string(textRunes[c.Start:c.End])
			if sliced != c.Content {
				t.Errorf("chunk[%d]: content mismatch via rune slice:\n  got:  %q\n  want: %q",
					i, sliced, c.Content)
			}
		}
	}
}

func TestSplitText_MixedChineseAndASCII(t *testing.T) {
	text := "Hello你好World世界Test测试"
	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 0, Separators: []string{"\n"}}
	chunks := SplitText(text, cfg)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	c := chunks[0]
	expectedRuneLen := utf8.RuneCountInString(text)
	if c.End-c.Start != expectedRuneLen {
		t.Errorf("End(%d) - Start(%d) = %d, want rune len %d (byte len would be %d)",
			c.End, c.Start, c.End-c.Start, expectedRuneLen, len(text))
	}
}

func TestSplitText_ProtectedPattern_ChineseContext(t *testing.T) {
	// Test protected markdown images in Chinese context.
	text := "这是前面的中文内容。![图片描述](http://example.com/img.png)这是后面的中文内容。"
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0, Separators: []string{"。"}}
	chunks := SplitText(text, cfg)

	textRunes := []rune(text)
	for i, c := range chunks {
		if c.Start < 0 || c.End > len(textRunes) {
			t.Errorf("chunk[%d]: out of rune range [%d, %d), total runes %d",
				i, c.Start, c.End, len(textRunes))
			continue
		}
		sliced := string(textRunes[c.Start:c.End])
		if sliced != c.Content {
			t.Errorf("chunk[%d]: rune-slice mismatch:\n  sliced: %q\n  content: %q",
				i, sliced, c.Content)
		}
	}
}

func TestSplitText_SimulateMergeSlicing(t *testing.T) {
	// Simulate what merge.go:104-106 does to ensure it won't panic.
	// This is the exact pattern that caused the production crash.
	line := "这是第一段内容用于模拟知识库问答的文本"
	text := line + "\n\n" + line + "\n\n" + line

	cfg := SplitterConfig{ChunkSize: 25, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("need at least 2 chunks for overlap test, got %d", len(chunks))
	}

	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		curr := chunks[i]

		if curr.Start > prev.End {
			continue // non-overlapping, no merge needed
		}

		// This is the exact merge.go logic:
		contentRunes := []rune(curr.Content)
		offset := len(contentRunes) - (curr.End - prev.End)

		if offset < 0 {
			t.Fatalf("chunk[%d] merge panic: offset=%d < 0 (contentRunes=%d, curr.End=%d, prev.End=%d)",
				i, offset, len(contentRunes), curr.End, prev.End)
		}
		if offset > len(contentRunes) {
			t.Fatalf("chunk[%d] merge panic: offset=%d > len(contentRunes)=%d",
				i, offset, len(contentRunes))
		}

		_ = string(contentRunes[offset:])
	}
}

// TestSplitText_RecursiveSeparators_NoOversizeChunks exposes the regression
// where after picking the first separator that yields >1 piece, sub-pieces
// that are still larger than ChunkSize were not split further with the next
// separator. Real-world docs with one paragraph break followed by a long
// run of newline-separated lines must still be honored.
func TestSplitText_RecursiveSeparators_NoOversizeChunks(t *testing.T) {
	// One paragraph break, then 50 short newline-separated lines forming
	// ~1500 chars in the second paragraph.
	body := strings.Repeat("This is one fairly short line of text.\n", 50)
	text := "lead paragraph that is short.\n\n" + body
	cfg := SplitterConfig{
		ChunkSize:    300,
		ChunkOverlap: 30,
		Separators:   []string{"\n\n", "\n", ". "},
	}
	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// No chunk should exceed roughly 1.5x ChunkSize — recursive splitting
	// at the next-priority separator should keep this bounded.
	maxAllowed := cfg.ChunkSize * 3 / 2
	for i, c := range chunks {
		l := len([]rune(c.Content))
		if l > maxAllowed {
			t.Errorf("chunk %d is %d runes, > 1.5x ChunkSize (%d) — recursive split missing", i, l, maxAllowed)
		}
	}
}

func TestSplitText_Empty(t *testing.T) {
	chunks := SplitText("", DefaultConfig())
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty text, got %d", len(chunks))
	}
}

func TestSplitText_SingleCharChinese(t *testing.T) {
	text := "你"
	cfg := SplitterConfig{ChunkSize: 10, ChunkOverlap: 0, Separators: []string{"\n"}}
	chunks := SplitText(text, cfg)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Start != 0 || chunks[0].End != 1 {
		t.Errorf("expected [0,1), got [%d,%d)", chunks[0].Start, chunks[0].End)
	}
}

func TestSplitText_LaTeXBlockInChinese(t *testing.T) {
	text := "前面的文字$$E=mc^2$$后面的文字"
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0, Separators: []string{"\n"}}
	chunks := SplitText(text, cfg)

	textRunes := []rune(text)
	for i, c := range chunks {
		spanLen := c.End - c.Start
		contentRuneLen := utf8.RuneCountInString(c.Content)
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: span %d != rune len %d", i, spanLen, contentRuneLen)
		}
		if c.End > len(textRunes) {
			t.Errorf("chunk[%d]: End %d > total runes %d", i, c.End, len(textRunes))
		}
	}
}

func TestSplitText_CodeBlockInChinese(t *testing.T) {
	text := "中文描述\n```python\nprint('hello')\n```\n继续中文"
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	textRunes := []rune(text)
	for i, c := range chunks {
		if c.Start < 0 || c.End > len(textRunes) {
			t.Errorf("chunk[%d]: out of range [%d,%d), total %d", i, c.Start, c.End, len(textRunes))
			continue
		}
		sliced := string(textRunes[c.Start:c.End])
		if sliced != c.Content {
			t.Errorf("chunk[%d]: rune-slice mismatch:\n  sliced: %q\n  content: %q",
				i, sliced, c.Content)
		}
	}
}

func TestSplitText_OverlapChunks_NonNegativeStart(t *testing.T) {
	// When overlap is used, start of the next chunk could go before 0 if broken.
	text := strings.Repeat("中文测试内容，", 50)
	cfg := SplitterConfig{ChunkSize: 20, ChunkOverlap: 5, Separators: []string{"，"}}
	chunks := SplitText(text, cfg)

	for i, c := range chunks {
		if c.Start < 0 {
			t.Errorf("chunk[%d]: negative Start %d", i, c.Start)
		}
		if c.End < c.Start {
			t.Errorf("chunk[%d]: End %d < Start %d", i, c.End, c.Start)
		}
	}
}

func TestFindSemanticOverlapBoundary_PriorityThenEarliest(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "paragraph outranks earlier sentence and line",
			text: "第一句。第二句\n普通换行后的内容\n\n最后一段",
			want: "最后一段",
		},
		{
			name: "earliest sentence wins within same priority",
			text: "第一句。第二句。第三句",
			want: "第二句。第三句",
		},
		{
			name: "windows paragraph break",
			text: "第一段。\r\n\r\n第二段",
			want: "第二段",
		},
		{
			name: "line outranks earlier sentence",
			text: "第一句。第一行\n第二行",
			want: "第二行",
		},
		{
			name: "windows line break",
			text: "第一句。第一行\r\n第二行",
			want: "第二行",
		},
		{
			name: "chinese question mark",
			text: "第一句？第二句",
			want: "第二句",
		},
		{
			name: "chinese exclamation mark",
			text: "第一句！第二句",
			want: "第二句",
		},
		{
			name: "english period requires following space",
			text: "First sentence. Second sentence",
			want: "Second sentence",
		},
		{
			name: "english question mark requires following space",
			text: "Question? Answer",
			want: "Answer",
		},
		{
			name: "english exclamation mark requires following space",
			text: "Warning! Continue",
			want: "Continue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end, ok := findSemanticOverlapBoundary(tt.text)
			if !ok {
				t.Fatal("expected semantic overlap boundary")
			}
			got := string([]rune(tt.text)[end:])
			if got != tt.want {
				t.Fatalf("overlap tail = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindSemanticOverlapBoundary_NoConfiguredSemanticSeparator(t *testing.T) {
	for _, text := range []string{
		"没有任何语义分隔符的连续文本",
		"只有，逗号；分号：冒号",
		"version1.2 remains one unit",
		"address 192.168.1.1 remains one unit",
		"see https://ex.com?q=1&foo=bar",
	} {
		if end, ok := findSemanticOverlapBoundary(text); ok {
			t.Errorf("findSemanticOverlapBoundary(%q) returned boundary at %d", text, end)
		}
	}
}

func TestFindSemanticOverlapBoundary_IgnoresProtectedContent(t *testing.T) {
	for _, text := range []string{
		"代码是 `fmt.Println(\"hello. world\")` 后续内容",
		"代码块 ```go\nfmt.Println(\"hello. world\")\n``` 后续内容",
		"公式 $$x. y$$ 后续内容",
	} {
		if end, ok := findSemanticOverlapBoundary(text); ok {
			t.Errorf("findSemanticOverlapBoundary(%q) returned protected boundary at %d", text, end)
		}
	}
}

func TestFindSemanticOverlapBoundary_FiltersEligibilityBeforePriorityAndPosition(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		minEnd int
		want   string
	}{
		{
			name:   "earlier ineligible sentence does not hide eligible sentence",
			text:   "a。bc。XYZ",
			minEnd: 4,
			want:   "XYZ",
		},
		{
			name:   "ineligible paragraph does not outrank eligible sentence",
			text:   "\n\nx? tail",
			minEnd: 3,
			want:   "tail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end, ok := findSemanticOverlapBoundaryEndingAtOrAfter(tt.text, tt.minEnd)
			if !ok {
				t.Fatal("expected eligible semantic overlap boundary")
			}
			if got := string([]rune(tt.text)[end:]); got != tt.want {
				t.Fatalf("overlap tail = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComputeOverlap_FindsBoundaryInsideLargeUnit(t *testing.T) {
	text := "abcdefgh。尾巴内容"
	unit := splitUnit{text: text, start: 100, end: 100 + len([]rune(text))}

	overlap, overlapLen := computeOverlap([]splitUnit{unit}, 8, 32, 8)
	if got := unitsText(overlap); got != "尾巴内容" {
		t.Fatalf("overlap = %q, want %q", got, "尾巴内容")
	}
	if overlapLen != len([]rune("尾巴内容")) {
		t.Fatalf("overlapLen = %d, want %d", overlapLen, len([]rune("尾巴内容")))
	}
	if len(overlap) != 1 || overlap[0].start != 109 || overlap[0].end != 113 {
		t.Fatalf("overlap source span = %+v, want [109,113)", overlap)
	}
}

func TestComputeOverlap_NoBoundaryMeansNoOverlap(t *testing.T) {
	text := "abcdefgh尾巴内容"
	unit := splitUnit{text: text, start: 0, end: len([]rune(text))}

	overlap, overlapLen := computeOverlap([]splitUnit{unit}, 8, 32, 8)
	if len(overlap) != 0 || overlapLen != 0 {
		t.Fatalf("expected no overlap, got %q (%d)", unitsText(overlap), overlapLen)
	}
}

func TestComputeOverlap_RespectsNextChunkCapacity(t *testing.T) {
	text := "abcdefgh。尾巴"
	unit := splitUnit{text: text, start: 0, end: len([]rune(text))}

	overlap, overlapLen := computeOverlap([]splitUnit{unit}, 10, 8, 5)
	if got := unitsText(overlap); got != "尾巴" {
		t.Fatalf("overlap = %q, want %q", got, "尾巴")
	}
	if overlapLen+5 > 8 {
		t.Fatalf("overlap plus next content exceeds chunk size: %d + %d > %d", overlapLen, 5, 8)
	}
}

func TestComputeOverlap_LookbehindBoundaryEligibility(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "single rune separator ending at minus one is eligible",
			text: "abcd。WXYZ",
			want: "WXYZ",
		},
		{
			name: "longest separator ending at minus one is eligible",
			text: "\r\n\r\nWXYZ",
			want: "WXYZ",
		},
		{
			name: "separator ending before minus one is ineligible",
			text: "a。bcWXYZ",
			want: "",
		},
		{
			name: "separator crossing original window start is eligible",
			text: "abc. WXY",
			want: "WXY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit := splitUnit{text: tt.text, start: 0, end: len([]rune(tt.text))}
			overlap, overlapLen := computeOverlap([]splitUnit{unit}, 4, 20, 4)
			if got := unitsText(overlap); got != tt.want {
				t.Fatalf("overlap = %q, want %q", got, tt.want)
			}
			if overlapLen != len([]rune(tt.want)) {
				t.Fatalf("overlapLen = %d, want %d", overlapLen, len([]rune(tt.want)))
			}
			if overlapLen > 4 {
				t.Fatalf("overlap exceeds configured limit: %d > 4", overlapLen)
			}
		})
	}
}

func TestSplitText_OverlapDoesNotBreakURLQuery(t *testing.T) {
	line1 := strings.Repeat("a", 35)
	text := line1 + "\n" + "https://ex.com?q=1 tail\n" + "more content here"
	cfg := SplitterConfig{ChunkSize: 40, ChunkOverlap: 10, Separators: []string{"\n\n", "\n"}}

	chunks := SplitText(text, cfg)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d: %#v", len(chunks), chunks)
	}
	if strings.HasPrefix(chunks[2].Content, "q=1") {
		t.Fatalf("overlap broke at URL query string: chunk[2]=%q", chunks[2].Content)
	}
}

func TestBuildUnitsWithProtection_InlineCodeNotSplit(t *testing.T) {
	text := "intro `code.here` suffix"
	units := buildUnitsWithProtection(text, protectedSpans(text), []string{"\n"}, 0)

	for _, u := range units {
		if strings.Contains(u.text, "code.here") && !strings.Contains(u.text, "`code.here`") {
			t.Fatalf("inline code period split unit: %q", u.text)
		}
	}
}

func TestSplitText_SemanticOverlapInsideParagraph(t *testing.T) {
	first := strings.Repeat("甲", 15) + "。尾巴"
	text := first + "\n\n" + "后续内容"
	cfg := SplitterConfig{
		ChunkSize:    20,
		ChunkOverlap: 10,
		Separators:   []string{"\n\n", "\n", "。"},
	}

	chunks := SplitText(text, cfg)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.HasPrefix(chunks[1].Content, "尾巴\n\n") {
		t.Fatalf("second chunk should start at the sentence boundary inside the large unit, got %q", chunks[1].Content)
	}
	if got := chunks[1].End - chunks[1].Start; got != len([]rune(chunks[1].Content)) {
		t.Fatalf("position invariant broken: span=%d content=%d", got, len([]rune(chunks[1].Content)))
	}
}

func TestBuildUnitsWithProtection_RuneOffsets(t *testing.T) {
	text := "你好世界"
	units := buildUnitsWithProtection(text, nil, []string{"\n"}, 0)

	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}

	u := units[0]
	expectedRuneLen := 4 // 4 Chinese characters
	byteLen := len(text) // 12 bytes

	if u.start != 0 {
		t.Errorf("start: got %d, want 0", u.start)
	}
	if u.end != expectedRuneLen {
		t.Errorf("end: got %d, want %d (rune len); byte len is %d", u.end, expectedRuneLen, byteLen)
	}
}

func TestBuildUnitsWithProtection_WithProtectedSpan(t *testing.T) {
	text := "前面![alt](url)后面"
	protected := protectedSpans(text)
	units := buildUnitsWithProtection(text, protected, []string{"\n"}, 0)

	textRunes := []rune(text)
	for i, u := range units {
		contentRuneLen := utf8.RuneCountInString(u.text)
		spanLen := u.end - u.start
		if spanLen != contentRuneLen {
			t.Errorf("unit[%d] %q: span %d != rune len %d (byte len %d)",
				i, u.text, spanLen, contentRuneLen, len(u.text))
		}
		if u.start < 0 || u.end > len(textRunes) {
			t.Errorf("unit[%d]: out of range [%d,%d), total runes %d",
				i, u.start, u.end, len(textRunes))
		}
	}
}

func TestSplitBySeparators(t *testing.T) {
	tests := []struct {
		text       string
		separators []string
		wantParts  int
	}{
		{"a\n\nb\n\nc", []string{"\n\n"}, 5},
		{"abc", []string{"\n"}, 1},
		{"a\nb\nc", []string{"\n"}, 5},
		{"", []string{"\n"}, 1},
	}

	for _, tt := range tests {
		parts := splitBySeparators(tt.text, tt.separators, 0)
		if len(parts) != tt.wantParts {
			t.Errorf("splitBySeparators(%q, %v): got %d parts %v, want %d",
				tt.text, tt.separators, len(parts), parts, tt.wantParts)
		}
	}
}

func TestExtractImageRefs(t *testing.T) {
	text := "hello ![alt1](url1) world ![alt2](url2) end"
	refs := ExtractImageRefs(text)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].OriginalRef != "url1" || refs[0].AltText != "alt1" {
		t.Errorf("ref[0] mismatch: %+v", refs[0])
	}
	if refs[1].OriginalRef != "url2" || refs[1].AltText != "alt2" {
		t.Errorf("ref[1] mismatch: %+v", refs[1])
	}
}

func TestSplitText_LargeChineseDocument(t *testing.T) {
	// Simulate a real document with paragraphs of Chinese text.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("第%d段：这是一段用于测试的中文内容，包含各种常见的汉字和标点符号。", i))
		sb.WriteString("\n\n")
	}
	text := sb.String()

	cfg := SplitterConfig{ChunkSize: 50, ChunkOverlap: 10, Separators: []string{"\n\n", "\n", "。"}}
	chunks := SplitText(text, cfg)

	textRunes := []rune(text)
	for i, c := range chunks {
		contentRuneLen := utf8.RuneCountInString(c.Content)
		spanLen := c.End - c.Start
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: End(%d)-Start(%d)=%d != runeLen(%d)",
				i, c.End, c.Start, spanLen, contentRuneLen)
		}
		if c.Start < 0 {
			t.Errorf("chunk[%d]: negative Start %d", i, c.Start)
		}
		if c.End > len(textRunes) {
			t.Errorf("chunk[%d]: End %d > total runes %d", i, c.End, len(textRunes))
		}
		if c.Start >= 0 && c.End <= len(textRunes) {
			sliced := string(textRunes[c.Start:c.End])
			if sliced != c.Content {
				t.Errorf("chunk[%d]: content mismatch via rune-slice", i)
			}
		}
	}

	// Simulate merge.go logic on all overlapping chunk pairs
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		curr := chunks[i]
		if curr.Start > prev.End {
			continue
		}
		contentRunes := []rune(curr.Content)
		offset := len(contentRunes) - (curr.End - prev.End)
		if offset < 0 || offset > len(contentRunes) {
			t.Fatalf("chunk[%d] merge would panic: offset=%d, contentRunes=%d, curr.End=%d, prev.End=%d",
				i, offset, len(contentRunes), curr.End, prev.End)
		}
	}
}

// ---------------------------------------------------------------------------
// Table header prepending tests
// ---------------------------------------------------------------------------

func TestSplitText_TableHeaderPrependedToChunks(t *testing.T) {
	// A markdown table large enough to span multiple chunks.
	// Each chunk after the first should have the header row + separator prepended.
	text := "" +
		"前面的文字\n\n" +
		"| 姓名 | 年龄 | 城市 |\n" +
		"| --- | --- | --- |\n" +
		"| 张三 | 25 | 北京 |\n" +
		"| 李四 | 30 | 上海 |\n" +
		"| 王五 | 28 | 广州 |\n" +
		"| 赵六 | 35 | 深圳 |\n" +
		"| 孙七 | 22 | 杭州 |\n" +
		"| 周八 | 40 | 成都 |\n" +
		"\n后面的文字"

	tableHeader := "| 姓名 | 年龄 | 城市 |\n| --- | --- | --- |\n"

	cfg := SplitterConfig{ChunkSize: 60, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// Find chunks that contain table row data but not the original header position.
	// These should have the header prepended.
	headerPrependCount := 0
	for _, c := range chunks {
		if strings.Contains(c.Content, "| 李四") || strings.Contains(c.Content, "| 王五") ||
			strings.Contains(c.Content, "| 赵六") || strings.Contains(c.Content, "| 孙七") ||
			strings.Contains(c.Content, "| 周八") {
			if !strings.Contains(c.Content, "| 张三") {
				// This is a chunk with table rows but not the first row;
				// it should have the header prepended.
				if !strings.HasPrefix(c.Content, tableHeader) {
					t.Errorf("chunk (seq=%d) has table rows but is missing prepended header:\n%s",
						c.Seq, c.Content)
				} else {
					headerPrependCount++
				}
			}
		}
	}

	if headerPrependCount == 0 {
		t.Error("expected at least one chunk to have prepended table header, found none")
		for i, c := range chunks {
			t.Logf("chunk[%d] (seq=%d, start=%d, end=%d):\n%s", i, c.Seq, c.Start, c.End, c.Content)
		}
	}
}

func TestSplitText_NoHeaderForNonTableContent(t *testing.T) {
	// Ensure header prepending doesn't affect non-table content.
	text := strings.Repeat("这是一段普通的中文文本，不包含任何表格。\n\n", 10)

	cfg := SplitterConfig{ChunkSize: 30, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	textRunes := []rune(text)
	for i, c := range chunks {
		contentRuneLen := utf8.RuneCountInString(c.Content)
		spanLen := c.End - c.Start
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: span %d != rune len %d (no table, should be exact)", i, spanLen, contentRuneLen)
		}
		if c.End > len(textRunes) {
			t.Errorf("chunk[%d]: End %d exceeds total runes %d", i, c.End, len(textRunes))
		}
	}
}

func TestSplitText_TableHeaderEndedByEmptyLine(t *testing.T) {
	// After the table ends (empty line), subsequent chunks should NOT have the header.
	text := "" +
		"| A | B |\n" +
		"| --- | --- |\n" +
		"| 1 | 2 |\n" +
		"| 3 | 4 |\n" +
		"\n" +
		"这是表格之后的普通文本内容，不应该包含表头。\n" +
		"更多的普通文本内容用于填充。"

	cfg := SplitterConfig{ChunkSize: 40, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	for _, c := range chunks {
		hasTableRow := strings.Contains(c.Content, "| A | B |") || strings.Contains(c.Content, "|")
		hasPlainText := strings.Contains(c.Content, "这是表格之后") || strings.Contains(c.Content, "更多的普通")
		if hasPlainText && !hasTableRow {
			// This chunk is purely post-table text; should NOT have table header
			if strings.Contains(c.Content, "| --- |") {
				t.Errorf("post-table chunk should not contain table header:\n%s", c.Content)
			}
		}
	}
}

func TestHeaderTracker_BasicLifecycle(t *testing.T) {
	ht := newHeaderTracker()

	// Before table: no headers
	ht.update("Some regular text")
	if h := ht.getHeaders(); h != "" {
		t.Errorf("expected no headers before table, got %q", h)
	}

	// Table header unit
	ht.update("| A | B |\n| --- | --- |\n")
	if h := ht.getHeaders(); h == "" {
		t.Error("expected active header after table header unit")
	}

	// Table row: header should stay active
	ht.update("| 1 | 2 |\n")
	if h := ht.getHeaders(); h == "" {
		t.Error("header should remain active during table rows")
	}

	// Empty line: header should end
	ht.update("\n")
	if h := ht.getHeaders(); h != "" {
		t.Errorf("header should be cleared after empty line, got %q", h)
	}

	// New table can be tracked after the old one ended
	ht.update("| X | Y |\n| --- | --- |\n")
	if h := ht.getHeaders(); h == "" {
		t.Error("expected new header to be tracked after previous table ended")
	}
}

func TestHeaderTracker_EmptyHeaderRowRewrite(t *testing.T) {
	// Some converters (e.g., MarkItDown) produce tables with empty header rows:
	//   ||
	//   | --- | --- |
	//   | real col A | real col B |
	// The tracker should rewrite the header to be a proper Markdown table header:
	//   | real col A | real col B |
	//   | --- | --- |
	ht := newHeaderTracker()

	// Empty header row + separator
	ht.update("||\n| --- | --- | --- |\n")
	h := ht.getHeaders()
	if h == "" {
		t.Fatal("expected active header after empty header unit")
	}
	t.Logf("after empty header unit (pending): %q", h)

	// First data row → becomes the real column names
	ht.update("| 测试用例 ID | 测试模块 | 备注 |\n")
	h = ht.getHeaders()
	t.Logf("after rewrite: %q", h)

	if !strings.Contains(h, "测试用例 ID") {
		t.Errorf("rewritten header should contain column names, got:\n%s", h)
	}
	if strings.Contains(h, "||") {
		t.Errorf("rewritten header should NOT contain empty '||' row, got:\n%s", h)
	}
	if !strings.Contains(h, "---") {
		t.Errorf("rewritten header should contain separator, got:\n%s", h)
	}
	// Column names should come BEFORE the separator
	colIdx := strings.Index(h, "测试用例 ID")
	sepIdx := strings.Index(h, "---")
	if colIdx > sepIdx {
		t.Errorf("column names should appear before separator in rewritten header:\n%s", h)
	}

	// Subsequent data rows should NOT be absorbed
	ht.update("| TC-001 | 模块A | 备注1 |\n")
	h2 := ht.getHeaders()
	if strings.Contains(h2, "TC-001") {
		t.Errorf("header should NOT include subsequent data rows, got:\n%s", h2)
	}

	// Table end
	ht.update("\n")
	if ht.getHeaders() != "" {
		t.Error("header should be cleared after empty line")
	}
}

func TestHeaderTracker_NormalHeaderNoExtension(t *testing.T) {
	// A table with proper column names in the header row should NOT be extended.
	ht := newHeaderTracker()

	ht.update("| 姓名 | 年龄 |\n| --- | --- |\n")
	h := ht.getHeaders()
	if h == "" {
		t.Fatal("expected active header")
	}

	// First data row should NOT be absorbed into the header
	ht.update("| 张三 | 25 |\n")
	h2 := ht.getHeaders()
	if strings.Contains(h2, "张三") {
		t.Errorf("normal header should not absorb data rows, got:\n%s", h2)
	}
}

func TestSplitText_EmptyHeaderRowPrepend(t *testing.T) {
	// Simulate MarkItDown output: empty header row, real column names in first data row.
	text := "" +
		"前言\n\n" +
		"||\n" +
		"| --- | --- | --- |\n" +
		"| 用例ID | 模块 | 步骤 |\n" +
		"| TC-001 | A | 步骤1 |\n" +
		"| TC-002 | B | 步骤2 |\n" +
		"| TC-003 | C | 步骤3 |\n" +
		"| TC-004 | D | 步骤4 |\n" +
		"\n" +
		"结尾"

	cfg := SplitterConfig{ChunkSize: 80, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	t.Logf("total chunks: %d", len(chunks))
	for i, c := range chunks {
		t.Logf("chunk[%d] seq=%d start=%d end=%d:\n%s", i, c.Seq, c.Start, c.End, c.Content)
	}

	for _, c := range chunks {
		hasLaterRow := strings.Contains(c.Content, "TC-002") ||
			strings.Contains(c.Content, "TC-003") ||
			strings.Contains(c.Content, "TC-004")
		if hasLaterRow && !strings.Contains(c.Content, "TC-001") {
			// Should have column names prepended
			if !strings.Contains(c.Content, "用例ID") {
				t.Errorf("chunk with data rows should have real column names prepended:\n%s", c.Content)
			}
			// Should NOT have the empty || row
			lines := strings.Split(c.Content, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				isOnlyPipes := trimmed != "" && func() bool {
					for _, r := range trimmed {
						if r != '|' && r != ' ' {
							return false
						}
					}
					return true
				}()
				if isOnlyPipes {
					t.Errorf("chunk should NOT contain empty pipe row %q:\n%s", trimmed, c.Content)
					break
				}
			}
		}

		// No line should appear as a duplicate in any chunk
		lines := strings.Split(strings.TrimRight(c.Content, "\n"), "\n")
		seen := make(map[string]int)
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.Contains(trimmed, "---") {
				continue
			}
			seen[trimmed]++
			if seen[trimmed] > 1 {
				t.Errorf("line appears %d times in chunk (seq=%d): %q", seen[trimmed], c.Seq, trimmed)
			}
		}
	}

	// Verify restoration still works
	restored := restoreTextFromChunks(chunks)
	if restored != text {
		t.Errorf("restoration failed for empty-header table\n  original: %q\n  restored: %q", text, restored)
	}
}

func TestHeaderTracker_ColumnMismatchEndsTable(t *testing.T) {
	ht := newHeaderTracker()
	ht.update("| Name | Game | Fame | Blame |\n| --- | --- | --- | --- |\n")
	if ht.getHeaders() == "" {
		t.Fatal("expected active table header")
	}
	ht.update("| Sinple | Table |\n")
	if h := ht.getHeaders(); h != "" {
		t.Fatalf("2-col row should end 4-col table header, still active:\n%s", h)
	}
}

func TestHeaderTracker_ParagraphBreakEndsOnNextUnit(t *testing.T) {
	ht := newHeaderTracker()
	ht.update("| Name | Game | Fame | Blame |\n| --- | --- | --- | --- |\n")
	ht.update("| Russell Wilson | Football | High | Tacky uniform |\n\n")
	if h := ht.getHeaders(); h == "" {
		t.Fatal("paragraph break alone should not clear header yet")
	}
	if !ht.pendingTableBreak {
		t.Fatal("expected pendingTableBreak after row ending with \\n\\n")
	}
	ht.update("| Sinple | Table |\n")
	if h := ht.getHeaders(); h != "" {
		t.Fatalf("next table row should clear previous header, got %q", h)
	}
	if !ht.headerEndedThisUnit {
		t.Fatal("expected flush signal when new table starts after paragraph break")
	}
}

func TestSplitText_EnTablesNoCrossTableHeader(t *testing.T) {
	text := "## A table, with and without a header row\n\n" +
		"| Name | Game | Fame | Blame |\n" +
		"| --- | --- | --- | --- |\n" +
		"| Lebron James | Basketball | Very High | Leaving Cleveland |\n" +
		"| Ryan Braun | Baseball | Moderate | Steroids |\n" +
		"| Russell Wilson | Football | High | Tacky uniform |\n\n" +
		"| Sinple | Table |\n" +
		"| Without | Header |\n\n" +
		"| Simple  Multiparagraph | Table  Full |\n" +
		"| Of  Paragraphs | In each  Cell. |\n"

	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{"\n\n", "\n", "。"}}
	chunks := SplitText(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for i, c := range chunks {
		hasSinple := strings.Contains(c.Content, "| Sinple | Table |")
		hasSimple := strings.Contains(c.Content, "| Simple  Multiparagraph |")
		if hasSinple || hasSimple {
			if strings.Contains(c.Content, "| Name | Game | Fame | Blame |") {
				t.Errorf("chunk[%d] must not carry table-1 header into later tables:\n%s", i, c.Content)
			}
		}
	}
}

func TestSplitText_MultipleTablesInDocument(t *testing.T) {
	text := "" +
		"第一个表格：\n\n" +
		"| 名称 | 值 |\n" +
		"| --- | --- |\n" +
		"| A | 1 |\n" +
		"| B | 2 |\n" +
		"| C | 3 |\n" +
		"\n" +
		"中间的文字\n\n" +
		"| 项目 | 状态 |\n" +
		"| --- | --- |\n" +
		"| X | 完成 |\n" +
		"| Y | 进行中 |\n" +
		"| Z | 未开始 |\n" +
		"\n" +
		"结尾文字"

	cfg := SplitterConfig{ChunkSize: 50, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	// Verify that if a chunk has rows from table 2, it has table 2's header, not table 1's.
	for _, c := range chunks {
		if strings.Contains(c.Content, "| Y |") && !strings.Contains(c.Content, "| X |") {
			if !strings.Contains(c.Content, "| 项目 | 状态 |") {
				t.Errorf("chunk with table-2 rows should have table-2 header:\n%s", c.Content)
			}
			if strings.Contains(c.Content, "| 名称 | 值 |") {
				t.Errorf("chunk with table-2 rows should NOT have table-1 header:\n%s", c.Content)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Start/End restoration tests — verify original text can be reconstructed
// ---------------------------------------------------------------------------

// restoreTextFromChunks reconstructs the original text using only chunk
// Start/End positions. For chunks with prepended headers, the header is a
// "virtual" prefix whose length = runeLen(Content) - (End - Start).
// The original text portion is the last (End-Start) runes of Content.
func restoreTextFromChunks(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}

	// Sort by End (ascending), then Start (ascending) — same order as Python restore_text
	sorted := make([]Chunk, len(chunks))
	copy(sorted, chunks)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			if sorted[j].End < sorted[j-1].End ||
				(sorted[j].End == sorted[j-1].End && sorted[j].Start < sorted[j-1].Start) {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			} else {
				break
			}
		}
	}

	var result []rune
	lastEnd := 0

	for _, c := range sorted {
		if c.End <= lastEnd {
			continue // fully contained in a previously processed chunk
		}

		contentRunes := []rune(c.Content)
		spanLen := c.End - c.Start
		headerLen := len(contentRunes) - spanLen
		if headerLen < 0 {
			headerLen = 0
		}
		// originalPortion is the text[Start:End] part, excluding any prepended header
		originalPortion := contentRunes[headerLen:]

		// Only take the portion after lastEnd (skip overlap)
		newStart := 0
		if lastEnd > c.Start {
			newStart = lastEnd - c.Start
		}
		if newStart < len(originalPortion) {
			result = append(result, originalPortion[newStart:]...)
		}

		lastEnd = c.End
	}

	return string(result)
}

func TestSplitText_RestoreTextNoTable(t *testing.T) {
	// Plain Chinese text without any tables — baseline restoration check.
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString(fmt.Sprintf("第%d段：这是一段用于测试的中文内容，包含各种标点符号。", i))
		sb.WriteString("\n\n")
	}
	text := sb.String()

	cfg := SplitterConfig{ChunkSize: 50, ChunkOverlap: 10, Separators: []string{"\n\n", "\n", "。"}}
	chunks := SplitText(text, cfg)

	restored := restoreTextFromChunks(chunks)
	if restored != text {
		t.Errorf("restoration failed for plain text\n  original len: %d\n  restored len: %d",
			len([]rune(text)), len([]rune(restored)))
		// Find first difference
		orig := []rune(text)
		rest := []rune(restored)
		minLen := len(orig)
		if len(rest) < minLen {
			minLen = len(rest)
		}
		for i := 0; i < minLen; i++ {
			if orig[i] != rest[i] {
				t.Errorf("first diff at rune %d: orig=%q rest=%q", i, string(orig[i]), string(rest[i]))
				break
			}
		}
	}
}

func TestSplitText_RestoreTextWithTable(t *testing.T) {
	// Document with a table large enough to span multiple chunks.
	// Use a 2-column table (shorter header ~24 runes) + ChunkSize 80 so the
	// header can be prepended (header 24 + row ~16 = 40 < 80).
	text := "" +
		"这是文档前言部分的内容。\n\n" +
		"| 姓名 | 城市 |\n" +
		"| --- | --- |\n" +
		"| 张三 | 北京 |\n" +
		"| 李四 | 上海 |\n" +
		"| 王五 | 广州 |\n" +
		"| 赵六 | 深圳 |\n" +
		"| 孙七 | 杭州 |\n" +
		"| 周八 | 成都 |\n" +
		"| 吴九 | 武汉 |\n" +
		"| 郑十 | 南京 |\n" +
		"\n" +
		"这是表格之后的文字内容。\n" +
		"这里还有更多的普通段落。"

	cfg := SplitterConfig{ChunkSize: 80, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	t.Logf("total chunks: %d", len(chunks))
	for i, c := range chunks {
		contentRunes := []rune(c.Content)
		spanLen := c.End - c.Start
		headerLen := len(contentRunes) - spanLen
		t.Logf("chunk[%d] seq=%d start=%d end=%d span=%d contentLen=%d headerPrepend=%d\n  content: %q",
			i, c.Seq, c.Start, c.End, spanLen, len(contentRunes), headerLen, c.Content)
	}

	// 1. Verify basic position invariants
	textRunes := []rune(text)
	for i, c := range chunks {
		if c.Start < 0 {
			t.Errorf("chunk[%d]: Start %d < 0", i, c.Start)
		}
		if c.End > len(textRunes) {
			t.Errorf("chunk[%d]: End %d > total runes %d", i, c.End, len(textRunes))
		}
		if c.End < c.Start {
			t.Errorf("chunk[%d]: End %d < Start %d", i, c.End, c.Start)
		}
	}

	// 2. Verify text[Start:End] matches the non-header portion of Content
	for i, c := range chunks {
		contentRunes := []rune(c.Content)
		spanLen := c.End - c.Start
		headerLen := len(contentRunes) - spanLen
		if headerLen < 0 {
			t.Errorf("chunk[%d]: Content rune len (%d) < span len (%d) — impossible",
				i, len(contentRunes), spanLen)
			continue
		}

		if c.Start >= 0 && c.End <= len(textRunes) {
			originalSlice := string(textRunes[c.Start:c.End])
			contentSuffix := string(contentRunes[headerLen:])
			if originalSlice != contentSuffix {
				t.Errorf("chunk[%d]: text[%d:%d] != Content[%d:]"+
					"\n  text slice:     %q"+
					"\n  content suffix: %q",
					i, c.Start, c.End, headerLen, originalSlice, contentSuffix)
			}
		}
	}

	// 3. Restore original text and compare
	restored := restoreTextFromChunks(chunks)
	if restored != text {
		t.Errorf("restoration FAILED"+
			"\n  original rune len: %d"+
			"\n  restored rune len: %d",
			len(textRunes), len([]rune(restored)))
		orig := []rune(text)
		rest := []rune(restored)
		minLen := len(orig)
		if len(rest) < minLen {
			minLen = len(rest)
		}
		for i := 0; i < minLen; i++ {
			if orig[i] != rest[i] {
				lo := i - 20
				if lo < 0 {
					lo = 0
				}
				hi := i + 20
				if hi > minLen {
					hi = minLen
				}
				t.Errorf("first diff at rune %d:\n  orig context: %q\n  rest context: %q",
					i, string(orig[lo:hi]), string(rest[lo:hi]))
				break
			}
		}
	} else {
		t.Log("restoration OK — original text perfectly reconstructed from Start/End")
	}

	// 4. Verify full coverage — Start/End spans must cover [0, len(textRunes))
	covered := make([]bool, len(textRunes))
	for _, c := range chunks {
		for p := c.Start; p < c.End && p < len(textRunes); p++ {
			covered[p] = true
		}
	}
	for i, v := range covered {
		if !v {
			t.Errorf("rune position %d is not covered by any chunk", i)
			break
		}
	}
}

func TestSplitText_RestoreTextWithMultipleTables(t *testing.T) {
	text := "" +
		"前言\n\n" +
		"| A | B |\n| --- | --- |\n" +
		"| 1 | 2 |\n| 3 | 4 |\n| 5 | 6 |\n| 7 | 8 |\n" +
		"\n中间文字\n\n" +
		"| X | Y |\n| --- | --- |\n" +
		"| a | b |\n| c | d |\n| e | f |\n" +
		"\n结尾"

	cfg := SplitterConfig{ChunkSize: 50, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	chunks := SplitText(text, cfg)

	// Restore and compare
	restored := restoreTextFromChunks(chunks)
	if restored != text {
		t.Errorf("multi-table restoration failed\n  original: %q\n  restored: %q", text, restored)
	}

	// Verify text[Start:End] matches content suffix for every chunk
	textRunes := []rune(text)
	for i, c := range chunks {
		contentRunes := []rune(c.Content)
		spanLen := c.End - c.Start
		headerLen := len(contentRunes) - spanLen
		if headerLen < 0 {
			t.Errorf("chunk[%d]: headerLen < 0", i)
			continue
		}
		if c.End <= len(textRunes) {
			if string(textRunes[c.Start:c.End]) != string(contentRunes[headerLen:]) {
				t.Errorf("chunk[%d]: text[Start:End] mismatch with content suffix", i)
			}
		}
	}
}

func TestSplitText_RestoreTextWithOverlap(t *testing.T) {
	// Larger overlap to stress the overlap+header interaction.
	text := "" +
		"| 列1 | 列2 | 列3 |\n" +
		"| --- | --- | --- |\n" +
		"| 数据A1 | 数据A2 | 数据A3 |\n" +
		"| 数据B1 | 数据B2 | 数据B3 |\n" +
		"| 数据C1 | 数据C2 | 数据C3 |\n" +
		"| 数据D1 | 数据D2 | 数据D3 |\n" +
		"| 数据E1 | 数据E2 | 数据E3 |\n" +
		"| 数据F1 | 数据F2 | 数据F3 |\n" +
		"\n" +
		"表后文本。"

	for _, overlap := range []int{0, 3, 10, 20} {
		t.Run(fmt.Sprintf("overlap=%d", overlap), func(t *testing.T) {
			cfg := SplitterConfig{ChunkSize: 60, ChunkOverlap: overlap, Separators: []string{"\n\n", "\n"}}
			chunks := SplitText(text, cfg)

			restored := restoreTextFromChunks(chunks)
			if restored != text {
				t.Errorf("restoration failed with overlap=%d\n  orig len=%d  rest len=%d",
					overlap, len([]rune(text)), len([]rune(restored)))
				for i, c := range chunks {
					t.Logf("  chunk[%d] start=%d end=%d content=%q", i, c.Start, c.End, c.Content)
				}
			}
		})
	}
}

func TestSplitTextParentChild_WithTableHeaders(t *testing.T) {
	text := "" +
		"前言\n\n" +
		"| 列A | 列B |\n" +
		"| --- | --- |\n" +
		"| 数据1 | 数据2 |\n" +
		"| 数据3 | 数据4 |\n" +
		"| 数据5 | 数据6 |\n" +
		"| 数据7 | 数据8 |\n" +
		"\n" +
		"结尾"

	parentCfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0, Separators: []string{"\n\n", "\n"}}
	childCfg := SplitterConfig{ChunkSize: 40, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}}
	result := SplitTextParentChild(text, parentCfg, childCfg)

	if len(result.Children) == 0 {
		t.Fatal("expected child chunks")
	}

	// Verify child chunk positions don't exceed parent document
	textRunes := []rune(text)
	for i, child := range result.Children {
		if child.Start < 0 {
			t.Errorf("child[%d]: negative Start %d", i, child.Start)
		}
		if child.End > len(textRunes) {
			t.Errorf("child[%d]: End %d exceeds text rune count %d", i, child.End, len(textRunes))
		}
	}
}

func TestMergeUnitsCapsProtectedContentWithOverlapAndRestoration(t *testing.T) {
	for _, size := range []int{4096, 4097, 7000} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			text := strings.Repeat("数", size)
			chunks := mergeUnits([]splitUnit{{text: text, start: 0, end: size}}, 512, 50)
			for index, chunk := range chunks {
				if chunkSize := len([]rune(chunk.Content)); chunkSize > 4096 {
					t.Fatalf("chunk %d has %d runes, exceeds protected-content cap", index, chunkSize)
				}
			}
			if size == 4096 && len(chunks) != 1 {
				t.Fatalf("expected exact-limit content to stay in one chunk, got %d", len(chunks))
			}
			if size > 4096 {
				if len(chunks) < 2 {
					t.Fatalf("expected oversized content to split, got %d chunk", len(chunks))
				}
				if overlap := chunks[0].End - chunks[1].Start; overlap <= 0 || overlap > 50 {
					t.Fatalf("expected overlap in (0, 50], got %d", overlap)
				}
			}
			if restored := restoreTextFromChunks(chunks); restored != text {
				t.Fatalf("restoration mismatch: got %d runes, want %d", len([]rune(restored)), size)
			}
		})
	}
}

func TestMergeUnitsOversizedTableRowRetainsActiveHeader(t *testing.T) {
	header := "| A | B |\n| --- | --- |\n"
	row := "| " + strings.Repeat("数据", 2200) + " | value |\n"
	headerLen := runeLen(header)
	text := header + row
	chunks := mergeUnits([]splitUnit{
		{text: header, start: 0, end: headerLen},
		{text: row, start: headerLen, end: runeLen(text)},
	}, 512, 50)

	if len(chunks) < 3 {
		t.Fatalf("expected header plus multiple row chunks, got %d", len(chunks))
	}
	for index, chunk := range chunks[1:] {
		if chunk.ContextHeader != header {
			t.Fatalf("row chunk %d lost active table header", index)
		}
		if size := runeLen(chunk.EmbeddingContent()); size > 4096 {
			t.Fatalf("row chunk %d has %d embedding runes, exceeds protected-content cap", index, size)
		}
		if got, want := chunk.Content, string([]rune(text)[chunk.Start:chunk.End]); got != want {
			t.Fatalf("row chunk %d source span mismatch", index)
		}
	}
	if restored := restoreTextFromChunks(chunks); restored != text {
		t.Fatal("oversized table restoration mismatch")
	}
}

func TestMergeUnitsCapsEmergencyOverlapAmplification(t *testing.T) {
	text := strings.Repeat("数", 7000)
	chunks := mergeUnits([]splitUnit{{text: text, start: 0, end: 7000}}, 10000, 5000)
	if len(chunks) > 4 {
		t.Fatalf("high overlap amplified one protected unit into %d chunks", len(chunks))
	}
	for index := 1; index < len(chunks); index++ {
		overlap := chunks[index-1].End - chunks[index].Start
		if overlap <= 0 || overlap > 2048 {
			t.Fatalf("chunk pair %d overlap = %d, want in (0, 2048]", index, overlap)
		}
	}
	if restored := restoreTextFromChunks(chunks); restored != text {
		t.Fatal("high-overlap emergency split restoration mismatch")
	}
}

func TestOversizedProtectedTableParentChildKeepsOffsetsAndHeader(t *testing.T) {
	header := "| A | B |\n| --- | --- |\n"
	row := "| " + strings.Repeat("数据", 2200) + " | value |\n"
	text := header + row
	headerLen := runeLen(header)
	parentCfg := SplitterConfig{
		ChunkSize: 10000, ChunkOverlap: 50, Separators: []string{"\n\n", "\n"}, Strategy: StrategyLegacy,
	}
	childCfg := SplitterConfig{
		ChunkSize: 512, ChunkOverlap: 50, Separators: []string{"\n\n", "\n"}, Strategy: StrategyLegacy,
	}

	results := map[string]ParentChildResult{
		"legacy":   SplitTextParentChild(text, parentCfg, childCfg),
		"strategy": SplitParentChild(text, parentCfg, childCfg),
	}
	textRunes := []rune(text)
	for name, result := range results {
		t.Run(name, func(t *testing.T) {
			foundRowChild := false
			for index, child := range result.Children {
				if child.Start < 0 || child.End > len(textRunes) || child.Start >= child.End {
					t.Fatalf("child %d has invalid source span [%d, %d)", index, child.Start, child.End)
				}
				if got, want := child.Content, string(textRunes[child.Start:child.End]); got != want {
					t.Fatalf("child %d source span mismatch", index)
				}
				if child.Start >= headerLen {
					foundRowChild = true
					if child.ContextHeader != header {
						t.Fatalf("row child %d lost table header context", index)
					}
				}
			}
			if !foundRowChild {
				t.Fatal("expected at least one child from the oversized table row")
			}
		})
	}
}

func TestOversizedProtectedTableKeepsHeaderAcrossAdaptiveTiers(t *testing.T) {
	header := "| A | B |\n| --- | --- |\n"
	row := "| " + strings.Repeat("数据", 2200) + " | value |\n"
	cfg := SplitterConfig{ChunkSize: 512, ChunkOverlap: 50, Separators: []string{"\n\n", "\n"}}

	headingPrefix := "# Table Section\n\n" + header
	headingText := headingPrefix + row + "plain tail\n\n# Next Section\n\nnext body"
	heuristicText := "intro\f" + header + row
	cases := map[string]struct {
		text     string
		rowStart int
		rowEnd   int
		chunks   []Chunk
	}{
		"heading": {
			text:     headingText,
			rowStart: runeLen(headingPrefix),
			rowEnd:   runeLen(headingPrefix + row),
			chunks:   splitByHeadingsImpl(headingText, cfg, nil),
		},
		"heuristic": {
			text:     heuristicText,
			rowStart: runeLen("intro\f" + header),
			rowEnd:   runeLen(heuristicText),
			chunks:   splitByHeuristicsImpl(heuristicText, cfg, nil),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			foundRowChunk := false
			textRunes := []rune(tc.text)
			for index, chunk := range tc.chunks {
				if chunk.Start < 0 || chunk.End > len(textRunes) || chunk.Start >= chunk.End {
					t.Fatalf("chunk %d has invalid span [%d, %d)", index, chunk.Start, chunk.End)
				}
				if chunk.End <= tc.rowStart || chunk.Start >= tc.rowEnd {
					continue
				}
				foundRowChunk = true
				if !strings.Contains(chunk.ContextHeader, header) {
					t.Fatalf("row chunk %d lost table header context: %q", index, chunk.ContextHeader)
				}
				if got, want := chunk.Content, string(textRunes[chunk.Start:chunk.End]); got != want {
					t.Fatalf("row chunk %d source span mismatch", index)
				}
			}
			if !foundRowChunk {
				t.Fatal("expected at least one oversized table row chunk")
			}
		})
	}
}
