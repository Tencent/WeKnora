package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkHasExtractableText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", false},
		{"whitespace only", "  \n\t ", false},
		{"single image link", "![page_1.jpg](resource://abc)", false},
		{"several image links with newlines", "![p1](resource://a)\n\n![p2](resource://b)\n", false},
		{"prose", "第1章 総則", true},
		{"prose around an image link", "See figure: ![fig](resource://x) below.", true},
		{"image link followed by OCR text", "![page_1.jpg](resource://abc)\nSection 2: Definitions", true},
		{"title containing right paren", `![a](images/a.png "阶段 1) 结果")`, false},
		{"path with balanced parens", `![a](images/a_(1).png "title")`, false},
		{"empty image wrapper", `<image url="x"><image_original>![a](x)</image_original></image>`, false},
		{"wrapper with OCR body", `<image url="images/p1.png">
<image_original>![p1](images/p1.png)</image_original>
<image_caption>scanned letter</image_caption>
<image_ocr>Sehr geehrter Herr Mustermann</image_ocr>
</image>`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chunkHasExtractableText(tt.content); got != tt.want {
				t.Fatalf("chunkHasExtractableText(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestSelectGraphChunks(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "text-link", ChunkType: types.ChunkTypeText, Content: "![page_1.jpg](resource://abc)"},
		{ID: "ocr-scanned", ChunkType: types.ChunkTypeImageOCR, ParentChunkID: "text-link", Content: "第1章 総則"},
		{
			ID: "cap-scanned", ChunkType: types.ChunkTypeImageCaption,
			ParentChunkID: "text-link", Content: "a scanned page of printed text",
		},
		{ID: "text-prose", ChunkType: types.ChunkTypeText, Content: "Contract between Alice and Bob."},
		{ID: "ocr-figure", ChunkType: types.ChunkTypeImageOCR, ParentChunkID: "text-prose", Content: "ACME logo"},
		{
			ID: "text-title-paren", ChunkType: types.ChunkTypeText,
			Content: `![a](images/a.png "阶段 1) 结果")`,
		},
		{ID: "ocr-orphan", ChunkType: types.ChunkTypeImageOCR, Content: "standalone OCR"},
	}

	got := selectGraphChunks(chunks)
	gotIDs := make([]string, len(got))
	for i, c := range got {
		gotIDs[i] = c.ID
	}
	assert.Equal(t, []string{"ocr-scanned", "text-prose", "ocr-orphan"}, gotIDs)
}

func TestWikiIngestKeepsScannedPageOCR(t *testing.T) {
	page := func(page int) types.SourceLocators {
		return types.SourceLocators{{Type: types.SourceLocatorPDF, Page: page}}
	}
	chunks := []*types.Chunk{
		{
			ID: "text-link", ChunkType: types.ChunkTypeText, IsEnabled: true, ChunkIndex: 2,
			Content: "![page_1.jpg](resource://abc)",
		},
		{
			ID: "ocr-scanned", ChunkType: types.ChunkTypeImageOCR, IsEnabled: true,
			ParentChunkID: "text-link", ChunkIndex: 0, SourceLocators: page(1),
			Content: "第1章 総則 本标准规定了扫描件正文。",
		},
		{
			ID: "cap-scanned", ChunkType: types.ChunkTypeImageCaption, IsEnabled: true,
			ParentChunkID: "text-link", Content: "a scanned page",
		},
		{
			ID: "text-prose", ChunkType: types.ChunkTypeText, IsEnabled: true, ChunkIndex: 1,
			Content: "Contract between Alice and Bob.",
		},
		{
			ID: "ocr-figure", ChunkType: types.ChunkTypeImageOCR, IsEnabled: true,
			ParentChunkID: "text-prose", Content: "ACME logo",
		},
		{
			ID: "ocr-page2", ChunkType: types.ChunkTypeImageOCR, IsEnabled: true,
			ParentChunkID: "text-link", ChunkIndex: 0, SourceLocators: page(2),
			Content: "第二节 扫描页应当排在第一章之后。",
		},
		{
			ID: "ocr-off", ChunkType: types.ChunkTypeImageOCR, IsEnabled: false,
			ParentChunkID: "text-link", Content: "这条识别结果已经停用。",
		},
	}

	scanned := appendScannedPageText(
		reconstructEnrichedContent(context.Background(), nil, 1, chunks[:3]),
		chunks[:3],
	)
	if !strings.Contains(scanned, "第1章 総則") || strings.Contains(scanned, "a scanned page") {
		t.Fatalf("scanned wiki content = %q", scanned)
	}
	if !hasSufficientTextContent(scanned) {
		t.Fatal("scanned OCR should be enough text for wiki extraction")
	}
	if strings.Count(scanned, "第1章 総則") != 1 {
		t.Fatalf("OCR was repeated: %q", scanned)
	}

	already := appendScannedPageText(scanned, chunks[:3])
	if already != scanned {
		t.Fatalf("inlined OCR was appended again: %q", already)
	}

	mixed := appendScannedPageText(
		reconstructEnrichedContent(context.Background(), nil, 1, []*types.Chunk{chunks[0], chunks[3]}),
		chunks,
	)
	if !strings.Contains(mixed, "Contract between Alice and Bob.") || !strings.Contains(mixed, "第1章 総則") {
		t.Fatalf("mixed document dropped scanned OCR: %q", mixed)
	}
	chapter := strings.Index(mixed, "第1章 総則")
	section := strings.Index(mixed, "第二节")
	if chapter < 0 || section < chapter {
		t.Fatalf("scanned pages are out of order: %q", mixed)
	}
	if strings.Contains(mixed, "ACME logo") || strings.Contains(mixed, "已经停用") {
		t.Fatalf("figure or disabled OCR was appended: %q", mixed)
	}

	disabledParent := []*types.Chunk{
		{
			ID: "text-link", ChunkType: types.ChunkTypeText, IsEnabled: false,
			Content: "![page](resource://abc)",
		},
		{
			ID: "ocr-scanned", ChunkType: types.ChunkTypeImageOCR, IsEnabled: true,
			ParentChunkID: "text-link", Content: "第1章 総則 本标准规定了扫描件正文。",
		},
	}
	if got := appendScannedPageText("", disabledParent); got != "" {
		t.Fatalf("disabled parent still contributed OCR: %q", got)
	}

	cited := wikiCitationChunks(chunks)
	var parent, prose *types.Chunk
	for _, c := range cited {
		switch c.ID {
		case "text-link":
			parent = c
		case "text-prose":
			prose = c
		}
	}
	if parent == nil || !strings.Contains(parent.Content, "第1章 総則") ||
		!strings.Contains(parent.Content, "第二节") {
		t.Fatalf("citation parent = %#v", parent)
	}
	if prose == nil || strings.Contains(prose.Content, "ACME logo") {
		t.Fatalf("citation prose = %#v", prose)
	}
	if chunks[0].Content != "![page_1.jpg](resource://abc)" {
		t.Fatal("citation copies mutated the stored text chunk")
	}
}

func TestKnowledgePostProcessGraphEnqueueSelection(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")

	const knowledgeID = "knowledge-graph-ocr"
	const kbID = "kb-graph"
	repo := &wikiEnqueueFailureKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID:              knowledgeID,
			TenantID:        7,
			KnowledgeBaseID: kbID,
			ParseStatus:     types.ParseStatusProcessing,
		},
	}
	queue := &wikiEnqueueFailureTaskQueue{}
	bind := func(id, parent, content string, chunkType types.ChunkType) *types.Chunk {
		return &types.Chunk{
			ID:              id,
			TenantID:        7,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: kbID,
			ParentChunkID:   parent,
			ChunkType:       chunkType,
			Content:         content,
		}
	}
	service := &KnowledgePostProcessService{
		knowledgeRepo: repo,
		kbService: &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{
			ID:       kbID,
			TenantID: 7,
			IndexingStrategy: types.IndexingStrategy{
				GraphEnabled: true,
			},
			ExtractConfig: &types.ExtractConfig{Enabled: true},
		}},
		chunkRepo: &wikiEnqueueFailureChunkRepo{chunks: []*types.Chunk{
			bind("text-link", "", "![page_1.jpg](resource://abc)", types.ChunkTypeText),
			bind("ocr-scanned", "text-link", "第1章 総則", types.ChunkTypeImageOCR),
			bind("cap-scanned", "text-link", "a scanned page", types.ChunkTypeImageCaption),
			bind("text-prose", "", "Contract between Alice and Bob.", types.ChunkTypeText),
			bind("ocr-figure", "text-prose", "ACME logo", types.ChunkTypeImageOCR),
		}},
		taskEnqueuer: queue,
	}

	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID:        7,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: kbID,
	})
	require.NoError(t, err)

	err = service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payload))
	require.NoError(t, err)

	assert.Equal(t, []string{"ocr-scanned", "text-prose"}, queue.extractChunkIDs)
	assert.Equal(t, []string{
		types.TypeSummaryGeneration,
		types.TypeChunkExtract,
		types.TypeChunkExtract,
	}, queue.taskTypes)
	assert.Equal(t, 3, repo.expectedSubtasks, "summary plus two graph extract slots")
}
