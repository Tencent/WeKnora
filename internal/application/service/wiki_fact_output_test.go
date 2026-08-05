package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseWikiFactOutputValidatesAndEnrichesCitations(t *testing.T) {
	evidence := map[string]wikiCitationEvidence{
		"chunk-1": {ChunkID: "chunk-1", KnowledgeID: "knowledge-1", Content: "Acme was founded in 2020."},
		"chunk-2": {ChunkID: "chunk-2", KnowledgeID: "knowledge-2", Content: "Acme released Product X."},
	}
	raw := `{
		"schema_version": 1,
		"summary": "Acme company profile.",
		"blocks": [
			{"type":"heading","content":"# Acme","citations":[]},
			{"type":"fact","content":"Acme was founded in 2020.","citations":[
				{"chunk_id":"chunk-1"},
				{"chunk_id":"chunk-1","role":"supporting"}
			]},
			{"type":"paragraph","content":"It released Product X.","citations":[
				{"chunk_id":"chunk-2","knowledge_id":"model-must-not-control-this","role":"supporting"}
			]}
		]
	}`

	got, err := parseWikiFactOutput(raw, evidence)
	if err != nil {
		t.Fatalf("parseWikiFactOutput() error = %v", err)
	}
	if got.SchemaVersion != 1 || len(got.Blocks) != 3 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if len(got.Blocks[1].Citations) != 1 {
		t.Fatalf("duplicate citations were not removed: %+v", got.Blocks[1].Citations)
	}
	if got.Blocks[1].Citations[0].KnowledgeID != "knowledge-1" {
		t.Fatalf("knowledge ID was not enriched from trusted evidence: %+v", got.Blocks[1].Citations[0])
	}
	if got.Blocks[2].Citations[0].KnowledgeID != "knowledge-2" {
		t.Fatalf("model-controlled knowledge ID was not overwritten: %+v", got.Blocks[2].Citations[0])
	}
	if got.Blocks[1].LogicalBlockID == "" || got.Blocks[1].LogicalBlockID == got.Blocks[2].LogicalBlockID {
		t.Fatalf("logical block IDs were not generated correctly: %+v", got.Blocks)
	}

	if ids := wikiFactChunkIDs(got); len(ids) != 2 || ids[0] != "chunk-1" || ids[1] != "chunk-2" {
		t.Fatalf("wikiFactChunkIDs() = %v", ids)
	}
	if ids := wikiFactKnowledgeIDs(got); len(ids) != 2 || ids[0] != "knowledge-1" || ids[1] != "knowledge-2" {
		t.Fatalf("wikiFactKnowledgeIDs() = %v", ids)
	}
}

func TestParseWikiFactOutputRejectsUnknownAndUnsupportedCitations(t *testing.T) {
	evidence := map[string]wikiCitationEvidence{
		"known": {ChunkID: "known", KnowledgeID: "knowledge-1", Content: "source"},
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown chunk",
			raw:  `{"summary":"s","blocks":[{"type":"fact","content":"claim","citations":[{"chunk_id":"invented"}]}]}`,
			want: "unknown chunk",
		},
		{
			name: "uncited fact",
			raw:  `{"summary":"s","blocks":[{"type":"fact","content":"claim","citations":[]}]}`,
			want: "supporting citation",
		},
		{
			name: "contradiction only",
			raw:  `{"summary":"s","blocks":[{"type":"fact","content":"claim","citations":[{"chunk_id":"known","role":"contradicting"}]}]}`,
			want: "supporting citation",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseWikiFactOutput(test.raw, evidence)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestWikiFactMetadataRoundTripAndRender(t *testing.T) {
	output := &wikiFactOutput{
		SchemaVersion: 1,
		Summary:       "summary",
		Blocks: []wikiFactBlock{
			{Type: types.WikiBlockHeading, Content: "Overview"},
			{Type: types.WikiBlockFact, Content: "A fact."},
			{Type: types.WikiBlockListItem, Content: "A list fact."},
		},
	}
	current := types.JSON(`{"manual_key":"keep"}`)
	metadata, err := setWikiFactMetadata(current, output)
	if err != nil {
		t.Fatalf("setWikiFactMetadata() error = %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata is invalid JSON: %v", err)
	}
	if _, ok := decoded["manual_key"]; !ok {
		t.Fatal("existing page metadata was not preserved")
	}
	restored, ok := getWikiFactMetadata(metadata)
	if !ok || restored.Summary != "summary" || len(restored.Blocks) != 3 {
		t.Fatalf("getWikiFactMetadata() = %+v, %v", restored, ok)
	}
	want := "## Overview\n\nA fact.\n\n- A list fact."
	if got := renderWikiFactOutput(restored); got != want {
		t.Fatalf("renderWikiFactOutput() = %q, want %q", got, want)
	}
}

func TestWikiEvidenceFromChunksEnforcesScopeAndBudget(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "ok", TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "k1", Content: "abcdef"},
		{ID: "other-tenant", TenantID: 8, KnowledgeBaseID: "kb", KnowledgeID: "k2", Content: "leak"},
		{ID: "other-kb", TenantID: 7, KnowledgeBaseID: "other", KnowledgeID: "k3", Content: "leak"},
	}
	evidence := wikiEvidenceFromChunks(chunks, 7, "kb", 4)
	if len(evidence) != 1 || evidence["ok"].Content != "abcd" {
		t.Fatalf("wikiEvidenceFromChunks() = %+v", evidence)
	}
}

func TestSourceRefsForWikiFactsKeepsOnlyActuallyCitedKnowledge(t *testing.T) {
	output := &wikiFactOutput{Blocks: []wikiFactBlock{{
		Type:    types.WikiBlockFact,
		Content: "fact",
		Citations: []wikiFactCitation{{
			ChunkID: "c2", KnowledgeID: "k2", Role: types.WikiSourceSupporting,
		}},
	}}}
	current := types.StringArray{"k1|Old", "k2|Existing title"}
	newRefs := map[string]string{"k2": "k2|New title", "k3": "k3|Unused"}
	refs := sourceRefsForWikiFacts(current, newRefs, output)
	if len(refs) != 1 || refs[0] != "k2|New title" {
		t.Fatalf("sourceRefsForWikiFacts() = %v", refs)
	}
}
