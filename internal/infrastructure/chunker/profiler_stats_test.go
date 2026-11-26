package chunker

import (
	"math"
	"strings"
	"testing"
)

func TestProfileDocumentPopulationLineStats(t *testing.T) {
	tests := []struct {
		name, text string
		lines      int
		mean, std  float64
	}{
		{"empty", "", 0, 0, 0},
		{"trailing empty line", "a\nbbb\n", 3, 4.0 / 3, math.Sqrt(14) / 3},
		{"Unicode rune lengths", "界\n🙂🙂🙂\n", 3, 4.0 / 3, math.Sqrt(14) / 3},
		{"code excluded", "a\n```\nlong code\n```\nbbb\n", 6, 4.0 / 3, math.Sqrt(14) / 3},
		{"all code", "```\ncode\n```", 3, 0, 0},
		{"single line", strings.Repeat("x", 10000), 1, 10000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ProfileDocument(tt.text)
			if p.TotalLines != tt.lines ||
				math.Abs(p.AvgLineLen-tt.mean) > 1e-12 || math.Abs(p.StdLineLen-tt.std) > 1e-12 {
				t.Fatalf("lines=%d mean=%g std=%g; want %d, %g, %g",
					p.TotalLines, p.AvgLineLen, p.StdLineLen, tt.lines, tt.mean, tt.std)
			}
		})
	}
}

func BenchmarkProfileDocumentManyLines(b *testing.B) {
	text := strings.Repeat("ordinary prose for profiling.\n", 100000)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		profileBenchmarkResult = ProfileDocument(text)
	}
}

var profileBenchmarkResult *DocProfile
