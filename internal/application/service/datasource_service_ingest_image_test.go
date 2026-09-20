package service

import (
	"context"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fakes: only the methods ingestItem touches on this path ──

type ingestImageRepo struct {
	sweepFakeRepo
	images map[string]string // external_id → FilePath
}

func (r *ingestImageRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, _, externalID string,
) (*types.Knowledge, error) {
	if path, ok := r.images[externalID]; ok {
		return &types.Knowledge{ID: "img-" + externalID, FilePath: path}, nil
	}
	return nil, nil
}

type ingestImageKS struct {
	interfaces.KnowledgeService
	t        *testing.T
	repo     interfaces.KnowledgeRepository
	gotBytes []byte
	gotMeta  map[string]string
}

func (k *ingestImageKS) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *ingestImageKS) CreateKnowledgeFromFile(
	_ context.Context, _ string, file *multipart.FileHeader, metadata map[string]string,
	_ *bool, _ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	f, err := file.Open()
	require.NoError(k.t, err)
	defer f.Close()
	content, err := io.ReadAll(f)
	require.NoError(k.t, err)
	k.gotBytes = content
	k.gotMeta = metadata
	return &types.Knowledge{ID: "new-knowledge"}, nil
}

func ingestImageTest(t *testing.T) (*DataSourceService, *types.DataSource, *ingestImageKS) {
	t.Helper()
	ks := &ingestImageKS{t: t}
	s := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "feishu",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	return s, ds, ks
}

// TestIngestItem_ResolvesEmbeddedImageMarkersBeforeCreate verifies that the
// content CreateKnowledgeFromFile receives has weknora-img:// markers rewritten
// to the sibling image row's FilePath — so the stored file, source_content and
// chunks all carry the same provider:// URL.
func TestIngestItem_ResolvesEmbeddedImageMarkersBeforeCreate(t *testing.T) {
	s, ds, ks := ingestImageTest(t)
	ks.repo = &ingestImageRepo{images: map[string]string{
		"img-1": "minio://bucket/images/abc.png",
	}}

	_, err := s.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1",
		FileName:   "doc.md",
		Content:    []byte("# 文档\n\n![截图](weknora-img://1)\n\n正文"),
		Metadata: map[string]string{
			"image_map": `{"1":"img-1"}`,
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, ks.gotBytes)
	assert.Contains(t, string(ks.gotBytes), "![截图](minio://bucket/images/abc.png)")
	assert.NotContains(t, string(ks.gotBytes), "weknora-img://")
	// image_map stays in metadata so the ProcessDocument hook can still resolve
	// any marker a late sibling arrival leaves behind.
	assert.Equal(t, `{"1":"img-1"}`, ks.gotMeta["image_map"])
}

// TestIngestItem_KeepsMarkerWhenSiblingMissing verifies that a sequence with no
// sibling image row (yet) keeps its weknora-img:// marker instead of being
// degraded at ingest — the ProcessDocument hook is the degrade authority.
func TestIngestItem_KeepsMarkerWhenSiblingMissing(t *testing.T) {
	s, ds, ks := ingestImageTest(t)
	ks.repo = &ingestImageRepo{} // no image rows

	_, err := s.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1",
		FileName:   "doc.md",
		Content:    []byte("![截图](weknora-img://1)"),
		Metadata: map[string]string{
			"image_map": `{"1":"img-missing"}`,
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, ks.gotBytes)
	assert.True(t, strings.Contains(string(ks.gotBytes), "weknora-img://1"))
}
