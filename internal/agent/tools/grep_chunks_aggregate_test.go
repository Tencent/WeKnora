package tools

import (
	"regexp"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAggregateByKnowledge_mergesChunksFromSameDocument(t *testing.T) {
	tool := &GrepChunksTool{}
	compiled := []*regexp.Regexp{regexp.MustCompile(`(?i)sample`)}
	results := []chunkWithTitle{
		{
			Chunk: types.Chunk{
				ID:              "chunk-a",
				KnowledgeID:     "doc-1",
				KnowledgeBaseID: "kb-1",
				Content:         "sample hit one",
			},
			KnowledgeTitle: "report-a.pdf",
		},
		{
			Chunk: types.Chunk{
				ID:              "chunk-b",
				KnowledgeID:     "doc-1",
				KnowledgeBaseID: "kb-1",
				Content:         "sample hit two",
			},
			KnowledgeTitle: "report-a.pdf",
		},
		{
			Chunk: types.Chunk{
				ID:              "chunk-c",
				KnowledgeID:     "doc-2",
				KnowledgeBaseID: "kb-1",
				Content:         "sample other doc",
			},
			KnowledgeTitle: "report-b.pdf",
		},
	}

	aggregated := tool.aggregateByKnowledge(results, []string{"sample"}, compiled)
	if len(aggregated) != 2 {
		t.Fatalf("want 2 documents, got %d", len(aggregated))
	}

	byID := make(map[string]knowledgeAggregation, len(aggregated))
	for _, row := range aggregated {
		byID[row.KnowledgeID] = row
	}
	if byID["doc-1"].ChunkHitCount != 2 {
		t.Fatalf("doc-1 chunk hits = %d, want 2", byID["doc-1"].ChunkHitCount)
	}
	if byID["doc-2"].ChunkHitCount != 1 {
		t.Fatalf("doc-2 chunk hits = %d, want 1", byID["doc-2"].ChunkHitCount)
	}
}
