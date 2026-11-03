package chunker

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func BenchmarkHeuristicDocument(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			text := strings.Repeat(strings.Repeat("Evidence 中文 paragraph. ", 15)+"\n\n\n", count)
			cfg := SplitterConfig{ChunkSize: 512, ChunkOverlap: 64, Strategy: StrategyHeuristic}
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chunks := Split(text, cfg)
				if len(chunks) != count {
					b.Fatalf("got %d chunks, want %d", len(chunks), count)
				}
			}
		})
	}
}

func TestHeuristicBlankBoundaryRuneOffsets(t *testing.T) {
	text := "前言😀\n\n\n第一段\n\n\n\n第二段e\u0301\n\n\n尾声"
	var got []int
	for _, b := range findHeuristicBoundaries(text, nil) {
		if b.priority == PrioBlankBlock {
			got = append(got, b.runeStart)
		}
	}
	var want []int
	for _, pair := range ExcessiveBlanksPattern.FindAllStringIndex(text, -1) {
		want = append(want, utf8.RuneCountInString(text[:pair[1]]))
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOverlapAlignmentMatchesLinearReference(t *testing.T) {
	runes := []rune(strings.Repeat("中文 phrase\n", 100))
	var bounds []boundary
	for i := 0; i < len(runes); i += 7 {
		bounds = append(bounds, boundary{runeStart: i})
	}
	for _, candidates := range [][]boundary{
		nil,
		{{runeStart: 0}},
		{{runeStart: 0}, {runeStart: 0}, {runeStart: 500}, {runeStart: 1000}},
		bounds,
	} {
		for end := 1; end <= len(runes); end += 3 {
			for _, overlap := range []int{0, 1, 5, 12, 1000} {
				want := linearOverlapReference(runes, end, overlap, candidates)
				if got := applyOverlapAligned(runes, end, overlap, candidates); got != want {
					t.Fatalf("end=%d overlap=%d got=%d want=%d", end, overlap, got, want)
				}
			}
		}
	}
}

func linearOverlapReference(runes []rune, curEnd, overlap int, bounds []boundary) int {
	if overlap <= 0 {
		return curEnd
	}
	target := max(curEnd-overlap, 0)
	windowStart := max(curEnd-2*overlap, 0)
	best := -1
	for _, b := range bounds {
		if b.runeStart >= windowStart && b.runeStart < curEnd && b.runeStart > best {
			best = b.runeStart
		}
	}
	if best >= 0 {
		return best
	}
	for i := target; i > windowStart && i < len(runes); i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return target
}

// Digests capture every Chunk field from the unchanged production implementation.
func TestHeuristicDocumentOutputDigest(t *testing.T) {
	for _, fixture := range []struct{ name, unit, digest string }{
		{
			"multilingual", strings.Repeat("Evidence 中文 😀 paragraph. ", 15) + "\n\n\n",
			"c5555f18dc19ac4c82d50afee24e63bc42221ef09d18cdb93f5b9d859f36ce67",
		},
		{
			"ascii", strings.Repeat("Evidence from the imported report. ", 10) + "\n\n\n\n",
			"d7b0ca53c717a03a4b762538f58c8912b5e950a3de09e45f8634f2505dc4a5b6",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			text := strings.Repeat(fixture.unit, 40)
			cfg := SplitterConfig{ChunkSize: 512, ChunkOverlap: 64, Strategy: StrategyHeuristic}
			chunks, diag := SplitWithDiagnostics(text, cfg)
			if diag.SelectedTier != TierHeuristic {
				t.Fatalf("unexpected fallback: %+v", diag)
			}
			encoded, err := json.Marshal(chunks)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(encoded)); got != fixture.digest {
				t.Fatalf("chunk output changed: got %s want %s", got, fixture.digest)
			}
			runes := []rune(text)
			for _, chunk := range chunks {
				if chunk.Content != string(runes[chunk.Start:chunk.End]) {
					t.Fatal("source span mismatch")
				}
			}
		})
	}
}
