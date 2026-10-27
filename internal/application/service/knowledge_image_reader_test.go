package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageReaderKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *imageReaderKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type imageReaderFiles struct {
	interfaces.FileService
	body  []byte
	reads []string
}

func (f *imageReaderFiles) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.reads = append(f.reads, path)
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func imageResult(urls ...string) *types.SearchResult {
	infos := make([]types.ImageInfo, len(urls))
	for i, u := range urls {
		infos[i] = types.ImageInfo{URL: u}
	}
	raw, _ := json.Marshal(infos)
	return &types.SearchResult{ID: "c1", KnowledgeBaseID: "kb-1", ImageInfo: string(raw)}
}

func TestReadChunkImageReadsTheFirstImageFromItsKnowledgeBaseStorage(t *testing.T) {
	files := &imageReaderFiles{body: []byte("png bytes")}
	s := &knowledgeBaseService{
		repo:       &imageReaderKBRepo{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo: &attrsTenantRepo{}, // no tenant row: the default FileService serves
		fileSvc:    files,
	}
	data, err := s.ReadChunkImage(context.Background(), imageResult("local://a.png", "local://b.png"))
	require.NoError(t, err)
	assert.Equal(t, []byte("png bytes"), data)
	assert.Equal(t, []string{"local://a.png"}, files.reads)
}

func TestReadChunkImageRefusesWhatItCannotOrShouldNotRead(t *testing.T) {
	files := &imageReaderFiles{body: bytes.Repeat([]byte{1}, maxRerankImageBytes+1)}
	s := &knowledgeBaseService{
		repo:       &imageReaderKBRepo{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo: &attrsTenantRepo{},
		fileSvc:    files,
	}
	_, err := s.ReadChunkImage(context.Background(), imageResult("local://huge.png"))
	assert.ErrorContains(t, err, "larger than")

	_, err = s.ReadChunkImage(context.Background(), &types.SearchResult{ID: "c2", ImageInfo: ""})
	assert.ErrorContains(t, err, "carries no image")

	_, err = s.ReadChunkImage(context.Background(), imageResult(""))
	assert.ErrorContains(t, err, "no image URL")
}
