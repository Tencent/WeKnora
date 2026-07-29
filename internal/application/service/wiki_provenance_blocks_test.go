package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestSplitWikiMarkdownBlocksRoundTrip(t *testing.T) {
	markdown := "\r\n# Title\r\n\r\n" +
		"Paragraph line one\r\ncontinues here.\r\n\r\n" +
		"- first item\r\n  continuation\r\n" +
		"- second item\r\n\r\n" +
		"| Name | Value |\r\n" +
		"| --- | --- |\r\n" +
		"| Alpha | 1 |\r\n\r\n" +
		"> quoted fact\r\n> continued\r\n\r\n" +
		"```go\r\nfmt.Println(\"x\")\r\n```\r\n"

	blocks := splitWikiMarkdownBlocks(markdown, nil, types.WikiEditSourcePipeline)
	if got := renderWikiMarkdownBlocks(blocks); got != markdown {
		t.Fatalf("round trip changed Markdown\n--- got ---\n%q\n--- want ---\n%q", got, markdown)
	}

	wantTypes := []string{
		types.WikiBlockTypeHeading,
		types.WikiBlockTypeParagraph,
		types.WikiBlockTypeListItem,
		types.WikiBlockTypeListItem,
		types.WikiBlockTypeTableRow,
		types.WikiBlockTypeTableRow,
		types.WikiBlockTypeTableRow,
		types.WikiBlockTypeQuote,
		types.WikiBlockTypeParagraph, // fenced code uses factual paragraph provenance
	}
	if len(blocks) != len(wantTypes) {
		t.Fatalf("block count = %d, want %d: %#v", len(blocks), len(wantTypes), blocks)
	}
	for i, want := range wantTypes {
		if blocks[i].BlockType != want {
			t.Errorf("block[%d].BlockType = %q, want %q", i, blocks[i].BlockType, want)
		}
		if blocks[i].ID == "" || blocks[i].LogicalBlockID == "" || blocks[i].ContentHash == "" {
			t.Errorf("block[%d] missing generated identity/hash: %+v", i, blocks[i])
		}
		if len(blocks[i].SectionPath) != 1 || blocks[i].SectionPath[0] != "Title" {
			t.Errorf("block[%d].SectionPath = %v, want [Title]", i, blocks[i].SectionPath)
		}
	}
}

func TestThematicBreakAndEmptyFenceAreLosslessLayoutBlocks(t *testing.T) {
	markdown := "# Title\n\nFirst fact.\n\n---\n\n```\n```\n\nSecond fact.\n"
	blocks := splitWikiMarkdownBlocks(markdown, nil, types.WikiEditSourcePipeline)
	if got := renderWikiMarkdownBlocks(blocks); got != markdown {
		t.Fatalf("round trip changed layout Markdown: got %q want %q", got, markdown)
	}
	var layoutBlocks int
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block.Content)
		if trimmed == "---" || trimmed == "```\n```" {
			layoutBlocks++
			if wikiBlockNeedsSource(block) {
				t.Fatalf("layout-only block unexpectedly requires a source: %q", block.Content)
			}
		}
	}
	if layoutBlocks != 2 {
		t.Fatalf("layout block count = %d, want 2; blocks=%+v", layoutBlocks, blocks)
	}
}

func TestSplitWikiMarkdownBlocksCarriesUnchangedLogicalBlockAndSources(t *testing.T) {
	previousBlocks := splitWikiMarkdownBlocks("# Old section\n\nStable fact.\n", nil, types.WikiEditSourcePipeline)
	if len(previousBlocks) != 2 {
		t.Fatalf("previous block count = %d, want 2", len(previousBlocks))
	}
	previousFact := previousBlocks[1]
	previousFact.ProvenanceStatus = types.WikiBlockProvenanceVerified
	previousFact.Sources = []*types.WikiBlockSource{{
		ID:               "old-source-row",
		BlockID:          previousFact.ID,
		KnowledgeID:      "knowledge-1",
		ChunkID:          "chunk-1",
		Evidence:         "Stable fact.",
		EvidenceHash:     "evidence-hash",
		ValidationStatus: types.WikiSourceValidationLocated,
	}}
	previous := &types.WikiPageBlockSet{Blocks: previousBlocks}

	current := splitWikiMarkdownBlocks("# New section\n\nStable   fact.\n", previous, types.WikiEditSourcePipeline)
	if len(current) != 2 {
		t.Fatalf("current block count = %d, want 2", len(current))
	}
	currentFact := current[1]
	if currentFact.LogicalBlockID != previousFact.LogicalBlockID {
		t.Fatalf("logical ID = %q, want carried %q", currentFact.LogicalBlockID, previousFact.LogicalBlockID)
	}
	if currentFact.ProvenanceStatus != types.WikiBlockProvenanceVerified {
		t.Fatalf("provenance = %q, want verified", currentFact.ProvenanceStatus)
	}
	if len(currentFact.Sources) != 1 {
		t.Fatalf("source count = %d, want 1", len(currentFact.Sources))
	}
	cloned := currentFact.Sources[0]
	if cloned == previousFact.Sources[0] || cloned.ID == previousFact.Sources[0].ID {
		t.Fatal("source row must be cloned for the new immutable block set")
	}
	if cloned.BlockID != currentFact.ID || cloned.ChunkID != "chunk-1" {
		t.Fatalf("cloned source = %+v", cloned)
	}
}

func TestSplitWikiMarkdownBlocksKeepsManualIdentityWithinExactSection(t *testing.T) {
	for _, author := range []string{types.WikiEditSourceUser, types.WikiEditSourceAgent} {
		t.Run(author, func(t *testing.T) {
			previousBlocks := splitWikiMarkdownBlocks(
				"# Section A\n\nA manually maintained paragraph.\n",
				nil,
				types.WikiEditSourcePipeline,
			)
			require.Len(t, previousBlocks, 2)
			previousManual := previousBlocks[1]
			previousManual.AuthorType = author
			previous := &types.WikiPageBlockSet{Blocks: previousBlocks}

			current := splitWikiMarkdownBlocks(
				"# Section A\n\nA manually   maintained paragraph.\n",
				previous,
				types.WikiEditSourcePipeline,
			)
			require.Len(t, current, 2)
			require.Equal(t, previousManual.LogicalBlockID, current[1].LogicalBlockID)
			require.Equal(t, author, current[1].AuthorType)
		})
	}
}

func TestSplitWikiMarkdownBlocksDoesNotMoveManualIdentityAcrossSections(t *testing.T) {
	for _, author := range []string{types.WikiEditSourceUser, types.WikiEditSourceAgent} {
		t.Run(author, func(t *testing.T) {
			previousBlocks := splitWikiMarkdownBlocks(
				"# Section A\n\nA manually maintained paragraph.\n",
				nil,
				types.WikiEditSourcePipeline,
			)
			require.Len(t, previousBlocks, 2)
			previousManual := previousBlocks[1]
			previousManual.AuthorType = author
			previousManual.ProvenanceStatus = types.WikiBlockProvenanceVerified
			previousManual.Sources = []*types.WikiBlockSource{{
				ID:          "old-source-row",
				BlockID:     previousManual.ID,
				KnowledgeID: "knowledge-1",
				ChunkID:     "chunk-1",
			}}
			previous := &types.WikiPageBlockSet{Blocks: previousBlocks}

			current := splitWikiMarkdownBlocks(
				"# Section B\n\nA manually maintained paragraph.\n",
				previous,
				types.WikiEditSourcePipeline,
			)
			require.Len(t, current, 2)
			moved := current[1]
			require.NotEqual(t, previousManual.LogicalBlockID, moved.LogicalBlockID)
			require.Equal(t, types.WikiEditSourcePipeline, moved.AuthorType)
			require.Equal(t, types.WikiBlockProvenanceUnsupported, moved.ProvenanceStatus)
			require.Empty(t, moved.Sources)
		})
	}
}

func TestAddPreviousWikiSourceContextsKeepsRewrittenPageDependencies(t *testing.T) {
	previous := &types.WikiPageBlockSet{Blocks: []*types.WikiPageBlock{{
		Sources: []*types.WikiBlockSource{
			{KnowledgeID: "knowledge-a", KnowledgeAttempt: 3, DocumentTitle: "A"},
			{KnowledgeID: "knowledge-b", KnowledgeAttempt: 4, DocumentTitle: "B"},
		},
	}}}
	contexts := map[string]wikiProvenanceSourceContext{
		"knowledge-c": {KnowledgeAttempt: 5, SourceTitle: "C"},
	}
	addPreviousWikiSourceContexts(
		previous,
		map[string]struct{}{"knowledge-a": {}},
		contexts,
	)

	if _, exists := contexts["knowledge-a"]; exists {
		t.Fatal("retracted knowledge-a was restored to source contexts")
	}
	if got := contexts["knowledge-b"]; got.KnowledgeAttempt != 4 || got.SourceTitle != "B" {
		t.Fatalf("surviving rewritten dependency = %+v, want knowledge-b attempt 4", got)
	}
	if got := contexts["knowledge-c"]; got.KnowledgeAttempt != 5 || got.SourceTitle != "C" {
		t.Fatalf("current addition context was overwritten: %+v", got)
	}
}

func TestValidateWikiEvidenceQuoteNormalizesWhitespaceButRejectsParaphrase(t *testing.T) {
	chunk := "Before. Alpha beta\r\n  gamma. After."
	got, err := validateWikiEvidenceQuote("Alpha   beta\n gamma.", chunk)
	if err != nil {
		t.Fatalf("valid normalized quote rejected: %v", err)
	}
	if got != "Alpha   beta\n gamma." {
		t.Fatalf("stored evidence = %q, want original model quote", got)
	}
	if _, err := validateWikiEvidenceQuote("Alpha implies a different conclusion.", chunk); err == nil {
		t.Fatal("paraphrased/non-contiguous evidence must be rejected")
	}
	if _, err := validateWikiEvidenceQuote("", chunk); err == nil {
		t.Fatal("empty evidence must be rejected")
	}
}

func TestWikiBlockNeedsSourceSkipsStructuralRows(t *testing.T) {
	tests := []struct {
		name  string
		block *types.WikiPageBlock
		want  bool
	}{
		{
			name:  "heading",
			block: &types.WikiPageBlock{BlockType: types.WikiBlockTypeHeading, Content: "## Heading"},
			want:  false,
		},
		{
			name:  "table delimiter",
			block: &types.WikiPageBlock{BlockType: types.WikiBlockTypeTableRow, Content: "| :--- | ---: |"},
			want:  false,
		},
		{
			name:  "table data row",
			block: &types.WikiPageBlock{BlockType: types.WikiBlockTypeTableRow, Content: "| Alpha | 1 |"},
			want:  true,
		},
		{
			name:  "paragraph",
			block: &types.WikiPageBlock{BlockType: types.WikiBlockTypeParagraph, Content: "Fact."},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wikiBlockNeedsSource(tt.block); got != tt.want {
				t.Fatalf("wikiBlockNeedsSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitWikiProvenanceClaimsCoversClausesAndTableCells(t *testing.T) {
	paragraph := &types.WikiPageBlock{
		ID:        "block-1",
		BlockType: types.WikiBlockTypeParagraph,
		Content:   "Alpha comes from A, and beta comes from B. Gamma comes from C；Delta comes from D。",
	}
	got := splitWikiProvenanceClaimTexts(paragraph)
	want := []string{
		"Alpha comes from A,",
		"and beta comes from B.",
		"Gamma comes from C；",
		"Delta comes from D。",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("paragraph claims = %#v, want %#v", got, want)
	}

	noPunctuation := &types.WikiPageBlock{
		ID:        "block-conjunction",
		BlockType: types.WikiBlockTypeParagraph,
		Content:   "Alpha comes from A and beta comes from B",
	}
	got = splitWikiProvenanceClaimTexts(noPunctuation)
	want = []string{"Alpha comes from A", "and beta comes from B"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("conjunction claims = %#v, want %#v", got, want)
	}

	table := &types.WikiPageBlock{
		ID:        "block-2",
		BlockType: types.WikiBlockTypeTableRow,
		Content:   "| Alice | 2020, Shanghai |",
	}
	got = splitWikiProvenanceClaimTexts(table)
	want = []string{"Alice", "2020,", "Shanghai"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("table claims = %#v, want %#v", got, want)
	}
}

func TestAlignWikiBlockSourcesRequiresEveryClaimAndMergesTheirSources(t *testing.T) {
	blocks := splitWikiMarkdownBlocks(
		"Alpha was founded in 2020. Beta launched in 2021.",
		nil,
		types.WikiEditSourcePipeline,
	)
	chunks := []*types.Chunk{
		{
			ID:              "chunk-a",
			TenantID:        7,
			KnowledgeBaseID: "kb-1",
			KnowledgeID:     "knowledge-a",
			Content:         "Alpha was founded in 2020.",
			ChunkType:       types.ChunkTypeText,
			ChunkIndex:      0,
			IsEnabled:       true,
		},
		{
			ID:              "chunk-b",
			TenantID:        7,
			KnowledgeBaseID: "kb-1",
			KnowledgeID:     "knowledge-b",
			Content:         "Beta launched in 2021.",
			ChunkType:       types.ChunkTypeText,
			ChunkIndex:      1,
			IsEnabled:       true,
		},
	}
	contexts := map[string]wikiProvenanceSourceContext{
		"knowledge-a": {KnowledgeAttempt: 2, SourceTitle: "Document A"},
		"knowledge-b": {KnowledgeAttempt: 3, SourceTitle: "Document B"},
	}

	t.Run("missing second claim rejects the complete block", func(t *testing.T) {
		model := &wikiProvenanceChatStub{respond: func(string) (string, error) {
			return `{"citations":{"q000":[{"chunk":"c000","evidence":"Alpha was founded in 2020."}]}}`, nil
		}}
		svc := &wikiIngestService{}
		sources, err := svc.alignWikiBlockSourcesForContexts(
			context.Background(), model, blocks, chunks, contexts, "English",
		)
		if err == nil || !strings.Contains(err.Error(), "claim 2 has no validated source") {
			t.Fatalf("error = %v, want uncovered second-claim rejection", err)
		}
		if sources != nil {
			t.Fatalf("partial block sources escaped failed alignment: %+v", sources)
		}
	})

	t.Run("all claims covered merges both documents onto the block", func(t *testing.T) {
		model := &wikiProvenanceChatStub{respond: func(string) (string, error) {
			return `{"citations":{` +
				`"q000":[{"chunk":"c000","evidence":"Alpha was founded in 2020."}],` +
				`"q001":[{"chunk":"c001","evidence":"Beta launched in 2021."}]}}`, nil
		}}
		svc := &wikiIngestService{}
		sources, err := svc.alignWikiBlockSourcesForContexts(
			context.Background(), model, blocks, chunks, contexts, "English",
		)
		if err != nil {
			t.Fatalf("alignWikiBlockSourcesForContexts() error = %v", err)
		}
		got := sources[blocks[0].ID]
		if len(got) != 2 {
			t.Fatalf("merged source count = %d, want 2: %+v", len(got), got)
		}
		if got[0].KnowledgeID != "knowledge-a" || got[1].KnowledgeID != "knowledge-b" {
			t.Fatalf("merged source knowledge IDs = %q, %q", got[0].KnowledgeID, got[1].KnowledgeID)
		}
	})
}

func TestBuildWikiPageBlockSetMarksOnlyFullyCoveredBlocksVerified(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "chunk-verified",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-verified",
		Content:         "The page contains a fully grounded fact.",
		ContentRevision: 2,
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	repo := &wikiLiveProvenanceChunkRepo{byKnowledge: map[string][]*types.Chunk{
		chunk.KnowledgeID: {chunk},
	}}
	model := &wikiProvenanceChatStub{respond: func(string) (string, error) {
		return `{"citations":{"q000":[{"chunk":"c000","evidence":"fully grounded fact"}]}}`, nil
	}}
	page := &types.WikiPage{
		ID:              "page-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "verified-page",
		Content:         "The page contains a fully grounded fact.",
	}
	svc := &wikiIngestService{chunkRepo: repo}
	set, err := svc.buildWikiPageBlockSet(
		context.Background(),
		model,
		page,
		nil,
		[]SlugUpdate{{
			KnowledgeID:      chunk.KnowledgeID,
			KnowledgeAttempt: 4,
			DocTitle:         "Verified Document",
		}},
		nil,
		nil,
		page.TenantID,
		"English",
	)
	if err != nil {
		t.Fatalf("buildWikiPageBlockSet() error = %v", err)
	}
	if len(set.Blocks) != 1 {
		t.Fatalf("block count = %d, want 1", len(set.Blocks))
	}
	if got := set.Blocks[0].ProvenanceStatus; got != types.WikiBlockProvenanceVerified {
		t.Fatalf("provenance status = %q, want verified", got)
	}
	if len(set.Blocks[0].Sources) != 1 || set.Blocks[0].Sources[0].KnowledgeID != chunk.KnowledgeID {
		t.Fatalf("verified block sources = %+v", set.Blocks[0].Sources)
	}
}

func TestBuildWikiPageBlockSetPreservesUserBlocksWithoutFileAlignment(t *testing.T) {
	const content = "A person added this paragraph.\n"
	previous := &types.WikiPageBlockSet{
		Blocks: []*types.WikiPageBlock{{
			ID:               "old-user-block",
			LogicalBlockID:   "logical-user-block",
			BlockType:        types.WikiBlockTypeParagraph,
			Content:          content,
			AuthorType:       types.WikiEditSourceUser,
			ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
		}},
	}
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-user-block", ID: "page-user-block",
		Slug: "concept/user-block", Content: content,
	}

	set, err := (&wikiIngestService{}).buildWikiPageBlockSet(
		context.Background(), nil, page, previous, nil, nil, nil, 1, "English",
	)
	require.NoError(t, err)
	require.Len(t, set.Blocks, 1)
	require.Equal(t, "logical-user-block", set.Blocks[0].LogicalBlockID)
	require.Equal(t, types.WikiEditSourceUser, set.Blocks[0].AuthorType)
	require.Equal(t, types.WikiBlockProvenanceUnsupported, set.Blocks[0].ProvenanceStatus)
	require.Empty(t, set.Blocks[0].Sources)
}

func TestBuildWikiPageBlockSetRestoresUserBlockOmittedByModel(t *testing.T) {
	previous := &types.WikiPageBlockSet{Blocks: []*types.WikiPageBlock{
		{
			ID: "old-heading", LogicalBlockID: "logical-heading",
			BlockType: types.WikiBlockTypeHeading, Content: "# Existing section\n",
			AuthorType: types.WikiEditSourcePipeline, ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
		},
		{
			ID: "old-user-block", LogicalBlockID: "logical-user-block",
			BlockType: types.WikiBlockTypeParagraph, Content: "A person added this paragraph.\n",
			AuthorType: types.WikiEditSourceUser, ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
		},
	}}
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-user-block", ID: "page-user-block",
		Slug: "concept/user-block", Content: "# Existing section\n",
	}

	set, err := (&wikiIngestService{}).buildWikiPageBlockSet(
		context.Background(), nil, page, previous, nil, nil, nil, 1, "English",
	)
	require.NoError(t, err)
	require.Len(t, set.Blocks, 2)
	require.Equal(t, "logical-heading", set.Blocks[0].LogicalBlockID)
	require.Equal(t, "logical-user-block", set.Blocks[1].LogicalBlockID)
	require.Equal(t, types.WikiEditSourceUser, set.Blocks[1].AuthorType)
	require.Contains(t, set.RenderedContent, "A person added this paragraph.")
}

func TestBuildWikiPageBlockSetDropsNewExactCopyOfManualBlock(t *testing.T) {
	for _, author := range []string{types.WikiEditSourceUser, types.WikiEditSourceAgent} {
		t.Run(author, func(t *testing.T) {
			const content = "A person added this paragraph.\n"
			previous := &types.WikiPageBlockSet{Blocks: []*types.WikiPageBlock{{
				ID: "old-manual-block", LogicalBlockID: "logical-manual-block",
				BlockType: types.WikiBlockTypeParagraph, Content: content,
				AuthorType: author, ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
			}}}
			page := &types.WikiPage{
				TenantID: 1, KnowledgeBaseID: "kb-manual-block", ID: "page-manual-block",
				Slug: "concept/manual-block", Content: content + "\n" + content,
			}

			set, err := (&wikiIngestService{}).buildWikiPageBlockSet(
				context.Background(), nil, page, previous, nil, nil, nil, 1, "English",
			)
			require.NoError(t, err)
			require.Len(t, set.Blocks, 1)
			require.Equal(t, "logical-manual-block", set.Blocks[0].LogicalBlockID)
			require.Equal(t, author, set.Blocks[0].AuthorType)
			require.Equal(t, content+"\n", set.RenderedContent)
		})
	}
}

func TestDropNewExactCopiesOfWikiManualBlocksPreservesIntentionalVariants(t *testing.T) {
	previous := &types.WikiPageBlockSet{Blocks: []*types.WikiPageBlock{
		{
			LogicalBlockID: "manual", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose.",
			AuthorType: types.WikiEditSourceUser,
		},
		{
			LogicalBlockID: "old-pipeline", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose.",
			AuthorType: types.WikiEditSourcePipeline,
		},
	}}
	current := []*types.WikiPageBlock{
		{
			LogicalBlockID: "manual", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose.",
			AuthorType: types.WikiEditSourceUser,
		},
		{
			LogicalBlockID: "old-pipeline", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose.",
			AuthorType: types.WikiEditSourcePipeline,
		},
		{
			LogicalBlockID: "new-exact-copy", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose.\n",
			AuthorType: types.WikiEditSourcePipeline,
		},
		{
			LogicalBlockID: "new-other-section", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section B"}, Content: "Shared prose.",
			AuthorType: types.WikiEditSourcePipeline,
		},
		{
			LogicalBlockID: "new-wording", BlockType: types.WikiBlockTypeParagraph,
			SectionPath: types.StringArray{"Section A"}, Content: "Shared prose changed.",
			AuthorType: types.WikiEditSourcePipeline,
		},
	}

	got := dropNewExactCopiesOfWikiManualBlocks(previous, current)
	require.Len(t, got, 4)
	logicalIDs := make([]string, 0, len(got))
	for index, block := range got {
		require.Equal(t, index, block.SortOrder)
		logicalIDs = append(logicalIDs, block.LogicalBlockID)
	}
	require.Equal(t, []string{"manual", "old-pipeline", "new-other-section", "new-wording"}, logicalIDs)
}

func TestAlignWikiBlockSourcesUsesOpaqueHandlesAndBuildsValidatedSource(t *testing.T) {
	blocks := splitWikiMarkdownBlocks("Grounded fact.", nil, types.WikiEditSourcePipeline)
	chunk := &types.Chunk{
		ID:              "chunk-secret-uuid",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-secret-uuid",
		Content:         "The source states: Grounded fact.",
		ContentRevision: 3,
		ChunkIndex:      4,
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	model := &wikiProvenanceChatStub{respond: func(prompt string) (string, error) {
		if strings.Contains(prompt, blocks[0].ID) || strings.Contains(prompt, chunk.ID) || strings.Contains(prompt, chunk.KnowledgeID) {
			t.Fatalf("durable IDs leaked into provenance prompt: %q", prompt)
		}
		if !strings.Contains(prompt, `id="b000"`) || !strings.Contains(prompt, `id="q000"`) ||
			!strings.Contains(prompt, `id="c000"`) {
			t.Fatalf("opaque handles missing from prompt: %q", prompt)
		}
		return `{"citations":{"q000":[{"chunk":"c000","evidence":"Grounded fact."}]}}`, nil
	}}

	svc := &wikiIngestService{}
	sources, err := svc.alignWikiBlockSources(
		context.Background(), model, blocks, []*types.Chunk{chunk}, chunk.KnowledgeID, 9, "English",
	)
	if err != nil {
		t.Fatalf("alignWikiBlockSources() error = %v", err)
	}
	got := sources[blocks[0].ID]
	if len(got) != 1 {
		t.Fatalf("source count = %d, want 1: %+v", len(got), got)
	}
	if got[0].ChunkID != chunk.ID || got[0].KnowledgeID != chunk.KnowledgeID || got[0].KnowledgeAttempt != 9 {
		t.Fatalf("resolved source metadata = %+v", got[0])
	}
	if got[0].ChunkRevision != 3 || got[0].ValidationStatus != types.WikiSourceValidationLocated {
		t.Fatalf("source validation metadata = %+v", got[0])
	}
	if got[0].EvidenceHash == "" || got[0].ChunkContentHash == "" {
		t.Fatalf("source hashes were not populated: %+v", got[0])
	}
}

func TestAlignWikiBlockSourcesRejectsForgedHandlesAndEvidence(t *testing.T) {
	blocks := splitWikiMarkdownBlocks("Grounded fact.", nil, types.WikiEditSourcePipeline)
	chunk := &types.Chunk{
		ID:              "chunk-1",
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		Content:         "Grounded fact.",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	tests := []struct {
		name     string
		response string
		contains string
	}{
		{
			name:     "unknown claim handle",
			response: `{"citations":{"q999":[{"chunk":"c000","evidence":"Grounded fact."}]}}`,
			contains: "unknown claim handle",
		},
		{
			name:     "unknown chunk handle",
			response: `{"citations":{"q000":[{"chunk":"c999","evidence":"Grounded fact."}]}}`,
			contains: "unknown chunk handle",
		},
		{
			name:     "fabricated evidence",
			response: `{"citations":{"q000":[{"chunk":"c000","evidence":"Fabricated fact."}]}}`,
			contains: "not a contiguous quote",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &wikiProvenanceChatStub{respond: func(string) (string, error) { return tt.response, nil }}
			svc := &wikiIngestService{}
			_, err := svc.alignWikiBlockSources(
				context.Background(), model, blocks, []*types.Chunk{chunk}, chunk.KnowledgeID, 1, "English",
			)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("error = %v, want substring %q", err, tt.contains)
			}
		})
	}
}

func TestAlignWikiBlockSourcesFailsWholeAlignmentWhenOneBatchFails(t *testing.T) {
	blocks := splitWikiMarkdownBlocks("Supported fact.", nil, types.WikiEditSourcePipeline)
	chunks := []*types.Chunk{
		{
			ID:          "chunk-1",
			KnowledgeID: "knowledge-1",
			Content:     strings.Repeat("a", 7000) + " supported-first",
			ChunkType:   types.ChunkTypeText,
			ChunkIndex:  0,
			IsEnabled:   true,
		},
		{
			ID:          "chunk-2",
			KnowledgeID: "knowledge-1",
			Content:     strings.Repeat("b", 7000) + " fail-second",
			ChunkType:   types.ChunkTypeText,
			ChunkIndex:  1,
			IsEnabled:   true,
		},
	}
	model := &wikiProvenanceChatStub{respond: func(prompt string) (string, error) {
		if strings.Contains(prompt, "fail-second") {
			return "", errors.New("permanent batch failure")
		}
		return `{"citations":{"q000":[{"chunk":"c000","evidence":"supported-first"}]}}`, nil
	}}

	svc := &wikiIngestService{}
	sources, err := svc.alignWikiBlockSources(
		context.Background(), model, blocks, chunks, "knowledge-1", 1, "English",
	)
	if err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("error = %v, want fail-closed batch error", err)
	}
	if sources != nil {
		t.Fatalf("partial sources escaped failed alignment: %+v", sources)
	}
}

func TestAlignWikiBlockSourcesRejectsUnsupportedFactualBlock(t *testing.T) {
	blocks := splitWikiMarkdownBlocks("Unsupported fact.", nil, types.WikiEditSourcePipeline)
	chunk := &types.Chunk{
		ID:          "chunk-1",
		KnowledgeID: "knowledge-1",
		Content:     "Unrelated source text.",
		ChunkType:   types.ChunkTypeText,
		IsEnabled:   true,
	}
	model := &wikiProvenanceChatStub{respond: func(string) (string, error) {
		return `{"citations":{}}`, nil
	}}

	svc := &wikiIngestService{}
	_, err := svc.alignWikiBlockSources(
		context.Background(), model, blocks, []*types.Chunk{chunk}, "knowledge-1", 1, "English",
	)
	if err == nil || !strings.Contains(err.Error(), "has no validated source") {
		t.Fatalf("error = %v, want unsupported block rejection", err)
	}
}

type wikiLiveProvenanceChunkRepo struct {
	interfaces.ChunkRepository
	byKnowledge map[string][]*types.Chunk
	calls       []string
}

func (r *wikiLiveProvenanceChunkRepo) ListChunksByKnowledgeID(
	_ context.Context, _ uint64, knowledgeID string,
) ([]*types.Chunk, error) {
	r.calls = append(r.calls, knowledgeID)
	return r.byKnowledge[knowledgeID], nil
}

func TestLoadLiveWikiProvenanceChunksUsesEverySurvivingDocumentChunk(t *testing.T) {
	repo := &wikiLiveProvenanceChunkRepo{byKnowledge: map[string][]*types.Chunk{
		"knowledge-a": {
			{ID: "a-disabled", KnowledgeID: "knowledge-a", ChunkType: types.ChunkTypeText, Content: "old", IsEnabled: false},
			{ID: "a-live", KnowledgeID: "knowledge-a", ChunkType: types.ChunkTypeText, Content: "new evidence", IsEnabled: true},
			{ID: "a-image", KnowledgeID: "knowledge-a", ChunkType: types.ChunkTypeImageCaption, Content: "caption", IsEnabled: true},
		},
		"knowledge-b": {
			{ID: "b-live", KnowledgeID: "knowledge-b", ChunkType: types.ChunkTypeText, Content: "other evidence", IsEnabled: true},
		},
	}}
	svc := &wikiIngestService{chunkRepo: repo}
	contexts := map[string]wikiProvenanceSourceContext{
		"knowledge-b": {KnowledgeAttempt: 4, SourceTitle: "Document B"},
		"knowledge-a": {KnowledgeAttempt: 7, SourceTitle: "Document A"},
	}

	chunks, err := svc.loadLiveWikiProvenanceChunks(context.Background(), 9, contexts)
	if err != nil {
		t.Fatalf("loadLiveWikiProvenanceChunks() error = %v", err)
	}
	if strings.Join(repo.calls, ",") != "knowledge-a,knowledge-b" {
		t.Fatalf("knowledge loads = %v, want deterministic complete context order", repo.calls)
	}
	if len(chunks) != 3 || chunks[0].ID != "a-live" || chunks[1].ID != "a-image" || chunks[2].ID != "b-live" {
		t.Fatalf("live provenance chunks = %+v, want all enabled textual Wiki sources", chunks)
	}
	if contexts["knowledge-a"].KnowledgeAttempt != 7 || contexts["knowledge-a"].SourceTitle != "Document A" {
		t.Fatalf("source context was changed: %+v", contexts["knowledge-a"])
	}
}

type wikiProvenanceChatStub struct {
	mu      sync.Mutex
	respond func(prompt string) (string, error)
}

func (m *wikiProvenanceChatStub) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	prompt := ""
	if len(messages) > 0 {
		prompt = messages[len(messages)-1].Content
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	response, err := m.respond(prompt)
	if err != nil {
		return nil, err
	}
	return &types.ChatResponse{Content: response}, nil
}

func (m *wikiProvenanceChatStub) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *wikiProvenanceChatStub) GetModelName() string { return "wiki-provenance-test" }
func (m *wikiProvenanceChatStub) GetModelID() string   { return "wiki-provenance-test" }
