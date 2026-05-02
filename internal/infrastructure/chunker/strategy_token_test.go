package chunker

import "testing"

func TestEnsureDefaults_TokenLimitClampsChunkSize(t *testing.T) {
	cfg := SplitterConfig{
		ChunkSize:  10000, // huge
		TokenLimit: 100,
		Languages:  []string{LangEnglish},
	}
	out := ensureDefaults(cfg)
	// 100 tokens * 4 chars/token * 0.9 ≈ 360 chars
	if out.ChunkSize >= 1000 {
		t.Errorf("expected ChunkSize clamped by TokenLimit, got %d", out.ChunkSize)
	}
	if out.ChunkOverlap >= out.ChunkSize {
		t.Errorf("overlap should be smaller than clamped chunk size: overlap=%d size=%d", out.ChunkOverlap, out.ChunkSize)
	}
}

func TestEnsureDefaults_TokenLimitChineseTighter(t *testing.T) {
	cfgEN := ensureDefaults(SplitterConfig{TokenLimit: 200, Languages: []string{LangEnglish}})
	cfgZH := ensureDefaults(SplitterConfig{TokenLimit: 200, Languages: []string{LangChinese}})
	if cfgZH.ChunkSize >= cfgEN.ChunkSize {
		t.Errorf("Chinese char budget should be tighter than English: zh=%d en=%d", cfgZH.ChunkSize, cfgEN.ChunkSize)
	}
}

func TestEnsureDefaults_NoTokenLimitKeepsChunkSize(t *testing.T) {
	cfg := SplitterConfig{ChunkSize: 800}
	out := ensureDefaults(cfg)
	if out.ChunkSize != 800 {
		t.Errorf("ChunkSize should stay 800, got %d", out.ChunkSize)
	}
}
