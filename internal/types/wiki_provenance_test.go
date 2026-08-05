package types

import "testing"

func TestWikiProvenanceEnums(t *testing.T) {
	valid := []interface{ IsValid() bool }{
		KnowledgeRevisionPublished,
		WikiPageRevisionStaged,
		WikiProvenanceVerified,
		WikiBlockFact,
		WikiBlockAuthorManual,
		WikiSourceSupporting,
		WikiSourceValidationVerified,
		WikiSourceMappingBlock,
	}
	for i, value := range valid {
		if !value.IsValid() {
			t.Fatalf("valid enum at index %d was rejected", i)
		}
	}

	invalid := []interface{ IsValid() bool }{
		KnowledgeRevisionStatus("current"),
		WikiPageRevisionStatus("ready"),
		WikiProvenanceStatus("unknown"),
		WikiBlockType("sentence"),
		WikiBlockAuthorType("llm"),
		WikiSourceRole("primary"),
		WikiSourceValidationStatus("complete"),
		WikiSourceMappingGranularity("sentence"),
	}
	for i, value := range invalid {
		if value.IsValid() {
			t.Fatalf("invalid enum at index %d was accepted", i)
		}
	}
}

func TestWikiBlockSourceValidateOffsets(t *testing.T) {
	chunkID := "chunk-1"
	base := WikiBlockSource{
		TenantID:            1,
		KnowledgeBaseID:     "kb-1",
		PageID:              "page-1",
		BlockID:             "block-1",
		KnowledgeID:         "knowledge-1",
		KnowledgeRevisionID: "revision-1",
		ChunkID:             &chunkID,
		SourceStart:         4,
		SourceEnd:           12,
		SourceRole:          WikiSourceSupporting,
		Confidence:          0.95,
		ValidationStatus:    WikiSourceValidationVerified,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	badOffsets := base
	badOffsets.SourceStart = 12
	badOffsets.SourceEnd = 4
	if err := badOffsets.Validate(); err == nil {
		t.Fatal("reversed source offsets should be rejected")
	}

	missingChunk := base
	missingChunk.ChunkID = nil
	if err := missingChunk.Validate(); err == nil {
		t.Fatal("exact offsets without a chunk should be rejected")
	}

	badConfidence := base
	badConfidence.Confidence = 1.01
	if err := badConfidence.Validate(); err == nil {
		t.Fatal("confidence above one should be rejected")
	}
}

func TestWikiProvenanceBeforeCreateDefaults(t *testing.T) {
	revision := KnowledgeRevision{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		RevisionNo:      1,
	}
	if err := revision.BeforeCreate(nil); err != nil {
		t.Fatalf("knowledge revision defaults failed: %v", err)
	}
	if revision.ID == "" || revision.Status != KnowledgeRevisionStaged {
		t.Fatalf("knowledge revision defaults not applied: %+v", revision)
	}

	pageRevision := WikiProvenancePageRevision{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PageID:          "page-1",
		RevisionNo:      1,
	}
	if err := pageRevision.BeforeCreate(nil); err != nil {
		t.Fatalf("page revision defaults failed: %v", err)
	}
	if pageRevision.Status != WikiPageRevisionStaged || pageRevision.ProvenanceStatus != WikiProvenancePartial {
		t.Fatalf("page revision defaults not applied: %+v", pageRevision)
	}

	block := WikiPageBlock{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PageID:          "page-1",
		PageRevisionID:  pageRevision.ID,
		BlockType:       WikiBlockFact,
	}
	if err := block.BeforeCreate(nil); err != nil {
		t.Fatalf("block defaults failed: %v", err)
	}
	if block.ID == "" || block.LogicalBlockID != block.ID || block.AuthorType != WikiBlockAuthorGenerated {
		t.Fatalf("block defaults not applied: %+v", block)
	}

	source := WikiBlockSource{
		TenantID:            1,
		KnowledgeBaseID:     "kb-1",
		PageID:              "page-1",
		BlockID:             block.ID,
		KnowledgeID:         "knowledge-1",
		KnowledgeRevisionID: revision.ID,
	}
	if err := source.BeforeCreate(nil); err != nil {
		t.Fatalf("source defaults failed: %v", err)
	}
	if source.SourceStart != -1 || source.SourceEnd != -1 || source.SourceRole != WikiSourceSupporting {
		t.Fatalf("source defaults not applied: %+v", source)
	}
}

func TestWikiProvenanceTableNames(t *testing.T) {
	cases := map[string]string{
		(KnowledgeRevision{}).TableName():          "knowledge_revisions",
		(WikiProvenancePageRevision{}).TableName(): "wiki_provenance_page_revisions",
		(WikiPageBlock{}).TableName():              "wiki_page_blocks",
		(WikiBlockSource{}).TableName():            "wiki_block_sources",
		(WikiPageSource{}).TableName():             "wiki_page_sources",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("table name = %q, want %q", got, want)
		}
	}
}
