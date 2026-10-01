package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
)

func TestEnforceChunkBudgetBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		total   int
		wantErr bool
	}{
		{"empty document", 0, false},
		{"typical document", 147, false},
		{"large legit spreadsheet (24283 chunks)", 24283, false},
		{"exactly at budget", MaxChunksPerDocument, false},
		{"budget plus one", MaxChunksPerDocument + 1, true},
		{"attack-scale amplification", 4_234_000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceChunkBudget(tc.total)
			if tc.wantErr && err == nil {
				t.Fatalf("enforceChunkBudget(%d) = nil, want rejection", tc.total)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("enforceChunkBudget(%d) = %v, want acceptance", tc.total, err)
			}
			if err != nil {
				for _, want := range []string{"100000", "chunk_size"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error %q missing %q (limit + remedy must reach the reporter)",
							err.Error(), want)
					}
				}
			}
		})
	}
}

// Regression anchor for #3539: separator-dense input at a tiny chunk_size
// amplifies at chunk granularity (measured ~42 chunks/KB at chunk_size=1 on
// the real NormalizeSplitterConfig + chunker.Split entry). The budget must
// reject the split result itself — an input byte cap cannot catch this shape,
// and the same text at the default size stays comfortably within budget.
func TestChunkBudgetRejectsAmplifiedSplit(t *testing.T) {
	para := "Asahi Linux brings first-class Linux support to Apple Silicon Macs.\n" +
		"The community maintains drivers for GPU, display and audio subsystems.\n" +
		"知识库检索增强生成需要合理切分文档段落。\n" +
		"- 切分过小会导致上下文碎片化\n" +
		"- 切分过大则检索精度下降\n"
	var sb strings.Builder
	for sb.Len() < 3*1024*1024 {
		sb.WriteString("# Section Title\n\n")
		for i := 0; i < 5; i++ {
			sb.WriteString(para)
		}
	}
	text := sb.String()

	cfg := chunker.NormalizeSplitterConfig(chunker.SplitterConfig{ChunkSize: 1, ChunkOverlap: 64})
	amplified := chunker.Split(text, cfg)
	if len(amplified) <= MaxChunksPerDocument {
		t.Fatalf("fixture no longer amplifies: %d chunks at chunk_size=1, want > %d — regenerate the input",
			len(amplified), MaxChunksPerDocument)
	}
	if err := enforceChunkBudget(len(amplified)); err == nil {
		t.Fatalf("enforceChunkBudget accepted an amplified split of %d chunks", len(amplified))
	}

	defaultCfg := chunker.NormalizeSplitterConfig(chunker.SplitterConfig{ChunkSize: 512, ChunkOverlap: 64})
	normal := chunker.Split(text, defaultCfg)
	if len(normal) > MaxChunksPerDocument {
		t.Fatalf("default-size split of the same text exceeded the budget: %d chunks",
			len(normal))
	}
	if err := enforceChunkBudget(len(normal)); err != nil {
		t.Fatalf("enforceChunkBudget rejected a normal split of %d chunks: %v", len(normal), err)
	}
}
