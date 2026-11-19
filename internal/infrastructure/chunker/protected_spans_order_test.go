package chunker

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestProtectedSpansMixedPatternsInDocumentOrder(t *testing.T) {
	parts := []string{
		"`示例 [link](url)`",
		"![图](image.png)",
		"$$x + y$$",
		"| name | value |\n| --- | --- |\n",
		"| cell | `code` |\n",
		"```go\n[inside](code)\n```",
		"[last](https://example.com)",
	}
	var docBuilder strings.Builder
	for _, part := range parts {
		docBuilder.WriteString(part)
		if !strings.HasSuffix(part, "\n") {
			docBuilder.WriteByte('\n')
		}
	}
	doc := docBuilder.String()
	var got []string
	lastEnd := 0
	for _, s := range protectedSpans(doc) {
		if s.start < lastEnd {
			t.Fatalf("overlapping or unordered span: %v after %d", s, lastEnd)
		}
		got = append(got, doc[s.start:s.end])
		lastEnd = s.end
	}
	if !reflect.DeepEqual(got, parts) {
		t.Fatalf("protected content = %q, want %q", got, parts)
	}
	// At the public entry point, overlapping inner matches must not split
	// an inline-code span, table header, image or fenced block apart.
	chunks := SplitText(doc, SplitterConfig{
		ChunkSize: 64, ChunkOverlap: 0, Separators: []string{"\n", " "},
	})
	for _, part := range parts {
		found := false
		for _, chunk := range chunks {
			if strings.Contains(chunk.Content, part) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("protected content split across chunks: %q", part)
		}
	}
}

func benchmarkMixedProtectedDocument(rows int) string {
	return strings.Repeat("[link](https://example.com) and `inline code`\n", rows)
}

func BenchmarkProtectedSpansMixedPatterns(b *testing.B) {
	for _, rows := range []int{1000, 4000, 16000} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			doc := benchmarkMixedProtectedDocument(rows)
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if len(protectedSpans(doc)) != 2*rows {
					b.Fatal("missing protected spans")
				}
			}
		})
	}
}

func BenchmarkSplitTextMixedPatterns(b *testing.B) {
	for _, rows := range []int{1000, 4000, 16000} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			doc := benchmarkMixedProtectedDocument(rows)
			cfg := DefaultConfig()
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if len(SplitText(doc, cfg)) == 0 {
					b.Fatal("missing chunks")
				}
			}
		})
	}
}
