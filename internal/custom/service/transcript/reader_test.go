package transcript

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
)

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
	metadata, err := parseChunkMetadata("## 视频定位信息\n\n```json\n{\"start_ms\":384000,\"end_ms\":385799,\"sentence_id\":\"s-1\",\"speaker_id\":\"speaker-2\",\"evidence_sentence_id\":\"evs:v1:abc\",\"transcript_generation\":\"generation-1\"}\n```\n\n## 原文\n\n内容")
	if err != nil {
		t.Fatalf("parseChunkMetadata returned error: %v", err)
	}
	if metadata.StartMs != 384000 || metadata.EndMs != 385799 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if metadata.SourceSentenceID != "s-1" || metadata.SpeakerID != "speaker-2" || metadata.EvidenceSentenceID != "evs:v1:abc" || metadata.TranscriptGeneration != "generation-1" {
		t.Fatalf("immutable evidence metadata was not parsed: %+v", metadata)
	}
}

func TestParseChunkMetadataRejectsMissingTiming(t *testing.T) {
	if _, err := parseChunkMetadata("## 原文\n\n内容"); err == nil {
		t.Fatal("expected missing timing metadata error")
	}
}

func TestSelectTimedChunkSkipsUnstructuredDuplicate(t *testing.T) {
	results := []weknora.SearchResult{
		{KnowledgeID: "chunk-18", Content: "## 原文\n\n摘要内容"},
		{KnowledgeID: "chunk-18", Content: "## 视频定位信息\n\n```json\n{\"start_ms\":180000,\"end_ms\":181250}\n```\n\n## 原文\n\n完整内容"},
	}
	content, metadata, err := selectTimedChunk(results, "chunk-18")
	if err != nil {
		t.Fatalf("selectTimedChunk returned error: %v", err)
	}
	if content == "" || metadata.StartMs != 180000 || metadata.EndMs != 181250 {
		t.Fatalf("unexpected selected chunk: content=%q metadata=%+v", content, metadata)
	}
}

func TestSelectTimedChunkRejectsWhenNoTimedDuplicateExists(t *testing.T) {
	_, _, err := selectTimedChunk([]weknora.SearchResult{{KnowledgeID: "chunk-18", Content: "摘要内容"}}, "chunk-18")
	if err == nil {
		t.Fatal("expected missing timing metadata error")
	}
}

func TestSelectTimedKnowledgeChunksJoinsSplitMetadataAndOriginal(t *testing.T) {
	chunks := []weknora.KnowledgeChunk{
		{KnowledgeID: "knowledge-1", ChunkIndex: 2, Content: "  \"video_type\": \"tutorial\"\n}\n```\n\n## 原文\n\n完整原文。"},
		{KnowledgeID: "knowledge-1", ChunkIndex: 0, Content: "## 视频定位信息\n\n"},
		{KnowledgeID: "knowledge-1", ChunkIndex: 1, Content: "```json\n{\"start_ms\":31779,\"end_ms\":50875,\n"},
	}

	content, metadata, err := selectTimedKnowledgeChunks(chunks, "knowledge-1")
	if err != nil {
		t.Fatalf("selectTimedKnowledgeChunks returned error: %v", err)
	}
	if metadata.StartMs != 31779 || metadata.EndMs != 50875 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if content != "## 视频定位信息\n\n```json\n{\"start_ms\":31779,\"end_ms\":50875,\n  \"video_type\": \"tutorial\"\n}\n```\n\n## 原文\n\n完整原文。" {
		t.Fatalf("unexpected joined content: %q", content)
	}
}

func TestSelectTimedKnowledgeChunksDropsGeneratedSummaryTail(t *testing.T) {
	chunks := []weknora.KnowledgeChunk{
		{KnowledgeID: "knowledge-1", ChunkIndex: 0, Content: "## 视频定位信息\n\n```json\n{\"start_ms\":0,\"end_ms\":1000}\n```\n\n## 原文\n\n原文内容。\n# Summary\n\n生成摘要，不是原文。"},
	}

	content, _, err := selectTimedKnowledgeChunks(chunks, "knowledge-1")
	if err != nil {
		t.Fatalf("selectTimedKnowledgeChunks returned error: %v", err)
	}
	if strings.Contains(content, "生成摘要") || !strings.Contains(content, "原文内容") {
		t.Fatalf("summary tail was not removed: %q", content)
	}
}

func TestOriginalTextRemovesPositioningMetadataAndGeneratedSummary(t *testing.T) {
	content := "## 视频定位信息\n\n```json\n{\"start_ms\":0,\"end_ms\":1000}\n```\n\n## 原文\n\n真实原文。\n# Summary\n\n生成摘要。"
	if got := OriginalText(content); got != "真实原文。" {
		t.Fatalf("OriginalText() = %q", got)
	}
}

func TestSelectTimedKnowledgeChunksRejectsGaps(t *testing.T) {
	chunks := []weknora.KnowledgeChunk{
		{KnowledgeID: "knowledge-1", ChunkIndex: 0, Content: "定位"},
		{KnowledgeID: "knowledge-1", ChunkIndex: 2, Content: "内容"},
	}
	if _, _, err := selectTimedKnowledgeChunks(chunks, "knowledge-1"); err == nil {
		t.Fatal("expected non-contiguous chunk error")
	}
}
