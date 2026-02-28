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

func TestBuildUnitsWithProtection_RuneOffsets(t *testing.T) {
	text := "你好世界"
	units := buildUnitsWithProtection(text, nil, []string{"\n"})

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
	units := buildUnitsWithProtection(text, protected, []string{"\n"})

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
		parts := splitBySeparators(tt.text, tt.separators)
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
