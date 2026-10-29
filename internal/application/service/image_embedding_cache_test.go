package service

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

type cacheImageEmbedder struct {
	*imageModel
	identity string
}

func (m *cacheImageEmbedder) ProcessingCacheIdentity() string { return m.identity }

type invalidCacheImageEmbedder struct {
	*cacheImageEmbedder
	vectors [][]float32
	err     error
	calls   int
}

func (m *invalidCacheImageEmbedder) BatchEmbedImages(context.Context, []embedding.Image) ([][]float32, error) {
	m.calls++
	return m.vectors, m.err
}
func TestImageEmbeddingCacheRejectsInvalidOutputs(t *testing.T) {
	for _, test := range []struct {
		name    string
		vectors [][]float32
		err     error
	}{
		{"count", nil, nil}, {"dimension", [][]float32{{1}}, nil}, {"nan", [][]float32{{1, float32(math.NaN()), 1}}, nil}, {"failure", nil, errors.New("offline")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
			svc := &ImageMultimodalService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})}
			m := &invalidCacheImageEmbedder{cacheImageEmbedder: &cacheImageEmbedder{imageModel: &imageModel{dims: 3}, identity: "model"}, vectors: test.vectors, err: test.err}
			for i := 0; i < 2; i++ {
				_, err := svc.cachedImageVector(context.Background(), types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc"}, m, m, embedding.Image{Data: []byte("image"), MIMEType: "image/png"})
				require.Error(t, err)
			}
			require.Equal(t, 2, m.calls)
			require.Empty(t, store.rows)
		})
	}
}

func TestImageEmbeddingCacheCurrentChunks(t *testing.T) {
	svc, _, recorder, engine := imageVectorFixture(t)
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	model := &cacheImageEmbedder{imageModel: &imageModel{dims: 3}, identity: "model"}
	for _, id := range []string{"old", "new"} {
		svc.processingCache = processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
		payload := types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkID: id, ImageURL: id}
		require.Equal(t, "indexed", svc.indexImageVector(context.Background(), imageVectorKB(), payload, testPNG(t), multimodalChunks(), model, engine))
	}
	require.Len(t, model.images, 1)
	require.Len(t, recorder.rows, 2)
	require.NotEqual(t, recorder.rows[0].ChunkID, recorder.rows[1].ChunkID)
	model.identity = "changed"
	payload := types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkID: "third"}
	require.Equal(t, "indexed", svc.indexImageVector(context.Background(), imageVectorKB(), payload, testPNG(t), multimodalChunks(), model, engine))
	require.Len(t, model.images, 2)
}
