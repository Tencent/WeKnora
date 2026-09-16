package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// ── fakes ──

type embeddedImageRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	images    map[string]*types.Knowledge // sibling image rows by external_id
}

func (r *embeddedImageRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *embeddedImageRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	return nil
}

func (r *embeddedImageRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, _ string, externalID string,
) (*types.Knowledge, error) {
	return r.images[externalID], nil
}

type embeddedImageFileSvc struct {
	interfaces.FileService
	content []byte
}

func (f *embeddedImageFileSvc) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.content)), nil
}

// ── helpers ──

func knowledgeWithMetadata(t *testing.T, kv map[string]string) *types.Knowledge {
	t.Helper()
	raw, err := json.Marshal(kv)
	require.NoError(t, err)
	return &types.Knowledge{
		ID:              "doc",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		FileType:        "md",
		ParseStatus:     types.ParseStatusPending,
		Metadata:        types.JSON(raw),
	}
}

// ── unit: resolveEmbeddedImageMarkers ──

func TestResolveEmbeddedImageMarkers(t *testing.T) {
	const providerURL = "minio://bucket/tok1.png"
	imageRow := func(path string) map[string]*types.Knowledge {
		return map[string]*types.Knowledge{"nt#doc1#image#tok1": {ID: "img1", FilePath: path}}
	}

	tests := []struct {
		name     string
		markdown string
		kv       map[string]string
		images   map[string]*types.Knowledge
		want     string
	}{
		{
			name:     "wrapped marker resolved to persisted provider FilePath",
			markdown: "before\n\n![图片](weknora-img://1)\n\nafter",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#tok1"}`},
			images:   imageRow(providerURL),
			want:     "before\n\n![图片](" + providerURL + ")\n\nafter",
		},
		{
			name:     "bare marker fallback also resolved",
			markdown: "see weknora-img://1 here",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#tok1"}`},
			images:   imageRow(providerURL),
			want:     "see ![图片](" + providerURL + ") here",
		},
		{
			name:     "image_map missing degrades",
			markdown: "![图片](weknora-img://1)",
			kv:       map[string]string{"datasource_id": "ds-1"},
			images:   imageRow(providerURL),
			want:     "![图片]()",
		},
		{
			name:     "unknown sequence number degrades",
			markdown: "![图片](weknora-img://9)",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#tok1"}`},
			images:   imageRow(providerURL),
			want:     "![图片]()",
		},
		{
			name:     "row not found (child ingested after parent) degrades",
			markdown: "![图片](weknora-img://1)",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#gone"}`},
			images:   map[string]*types.Knowledge{},
			want:     "![图片]()",
		},
		{
			name:     "empty FilePath degrades",
			markdown: "![图片](weknora-img://1)",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#tok1"}`},
			images:   imageRow(""),
			want:     "![图片]()",
		},
		{
			name:     "malformed image_map degrades",
			markdown: "![图片](weknora-img://1)",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1": broken`},
			images:   imageRow(providerURL),
			want:     "![图片]()",
		},
		{
			name:     "mixed forms and outcomes leave no marker behind",
			markdown: "![图片](weknora-img://1) mid ![图片](weknora-img://2) tail weknora-img://3",
			kv:       map[string]string{"datasource_id": "ds-1", "image_map": `{"1":"nt#doc1#image#tok1"}`},
			images:   imageRow(providerURL),
			want:     "![图片](" + providerURL + ") mid ![图片]() tail ![图片]()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &knowledgeService{
				repo: &embeddedImageRepo{images: tt.images},
			}
			got := svc.resolveEmbeddedImageMarkers(context.Background(), knowledgeWithMetadata(t, tt.kv), tt.markdown)
			require.Equal(t, tt.want, got)
			require.NotContains(t, got, "weknora-img://", "marker must never leak")
		})
	}
}

// ── end-to-end: ProcessDocument resolves markers before chunking ──

func TestProcessDocumentResolvesEmbeddedImageMarkers(t *testing.T) {
	// tok2's image row is absent: the child item is ingested after the
	// parent (out-of-order arrival) — the marker must degrade to a
	// placeholder, and no wrong URL may enter any chunk.
	md := "# Title\n\nintro\n\n![图片](weknora-img://1)\n\nlate ![图片](weknora-img://2)\n\noutro"
	parent := knowledgeWithMetadata(t, map[string]string{
		"datasource_id": "ds-1",
		"external_id":   "nt#doc1",
		"image_map":     `{"1":"nt#doc1#image#tok1","2":"nt#doc1#image#tok2"}`,
	})
	repo := &embeddedImageRepo{
		knowledge: parent,
		images: map[string]*types.Knowledge{
			"nt#doc1#image#tok1": {ID: "img1", TenantID: 7, KnowledgeBaseID: "kb", FilePath: "minio://bucket/tok1.png"},
		},
	}
	chunkRepo := &parentChildChunkService{}
	svc := &knowledgeService{
		repo:           repo,
		kbService:      &documentKBLookup{values: map[string]*types.KnowledgeBase{"kb": {ID: "kb", TenantID: 7}}},
		tenantRepo:     &documentTenantSpy{},
		tenantService:  &stubTenantServiceForModelDelete{},
		chunkRepo:      chunkRepo,
		graphEngine:    parentChildGraphRepo{},
		retrieveEngine: parentChildRetrieveRegistry{engine: &parentChildRetrieveEngine{}},
		task:           parentChildTaskEnqueuer{},
		fileSvc:        &embeddedImageFileSvc{content: []byte(md)},
		imageResolver:  docparser.NewImageResolver(),
	}
	payload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID:        7,
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		FilePath:        "doc.md",
		FileName:        "doc.md",
		FileType:        "md",
	})
	require.NoError(t, err)

	require.NoError(t, svc.ProcessDocument(
		context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload),
	))

	require.NotEmpty(t, chunkRepo.created, "chunks must be written")
	combined := ""
	for _, c := range chunkRepo.created {
		combined += c.Content
	}
	require.Contains(t, combined, "![图片](minio://bucket/tok1.png)",
		"persisted provider:// FilePath must reach the chunk content")
	require.Contains(t, combined, "![图片]()",
		"out-of-order image arrival must degrade to a placeholder")
	require.NotContains(t, combined, "weknora-img://", "no marker may leak into chunks")
	require.NotContains(t, combined, "tok2", "no URL may be invented for a missing image row")
	require.True(t, strings.Contains(combined, "intro") && strings.Contains(combined, "outro"),
		"document text must survive chunking")
}
