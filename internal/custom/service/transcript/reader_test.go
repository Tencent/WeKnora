package transcript

import "testing"

func TestEffectiveEndSecondsRoundsUpLastTimedChunk(t *testing.T) {
	chunks := []Chunk{{EndMs: 12_001}, {EndMs: 385_799}}
	got, err := EffectiveEndSeconds(chunks)
	if err != nil {
		t.Fatalf("EffectiveEndSeconds returned error: %v", err)
	}
	if got != 386 {
		t.Fatalf("EffectiveEndSeconds = %d, want 386", got)
	}
}

func TestParseChunkMetadata(t *testing.T) {
	metadata, err := parseChunkMetadata("## 视频定位信息\n\n```json\n{\"start_ms\":384000,\"end_ms\":385799}\n```\n\n## 原文\n\n内容")
	if err != nil {
		t.Fatalf("parseChunkMetadata returned error: %v", err)
	}
	if metadata.StartMs != 384000 || metadata.EndMs != 385799 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestParseChunkMetadataRejectsMissingTiming(t *testing.T) {
	if _, err := parseChunkMetadata("## 原文\n\n内容"); err == nil {
		t.Fatal("expected missing timing metadata error")
	}
}
