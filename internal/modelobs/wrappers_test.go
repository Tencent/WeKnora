package modelobs

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wrapperEmbedder struct{}

func (wrapperEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (wrapperEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

func (wrapperEmbedder) BatchEmbedWithPool(context.Context, embedding.Embedder, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}
func (wrapperEmbedder) GetModelName() string { return "embedding" }
func (wrapperEmbedder) GetDimensions() int   { return 1 }
func (wrapperEmbedder) GetModelID() string   { return "embedding-1" }

type wrapperReranker struct{}

func (wrapperReranker) Rerank(context.Context, string, []string) ([]rerank.RankResult, error) {
	return nil, nil
}
func (wrapperReranker) GetModelName() string { return "rerank" }
func (wrapperReranker) GetModelID() string   { return "rerank-1" }

type wrapperVLM struct{}

func (wrapperVLM) Predict(context.Context, [][]byte, string) (string, error) { return "text", nil }
func (wrapperVLM) GetModelName() string                                      { return "vlm" }
func (wrapperVLM) GetModelID() string                                        { return "vlm-1" }

type wrapperASR struct{}

func (wrapperASR) Transcribe(context.Context, []byte, string) (*asr.TranscriptionResult, error) {
	return &asr.TranscriptionResult{Text: "text"}, nil
}
func (wrapperASR) GetModelName() string { return "asr" }
func (wrapperASR) GetModelID() string   { return "asr-1" }

func TestProviderWrappersCreateOneLedgerRowPerCall(t *testing.T) {
	store := &recorderStore{}
	recorder := NewRecorder(store)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	model := func(id string, kind types.ModelType) *types.Model {
		return &types.Model{ID: id, TenantID: 7, Name: id, Type: kind}
	}

	embedder := recorder.WrapEmbedder(model("embedding-1", types.ModelTypeEmbedding), wrapperEmbedder{})
	_, err := embedder.Embed(ctx, "one")
	require.NoError(t, err)
	_, err = embedder.BatchEmbed(ctx, []string{"one"})
	require.NoError(t, err)
	_, err = embedder.BatchEmbedWithPool(ctx, embedder, []string{"one"})
	require.NoError(t, err)
	_, err = recorder.WrapReranker(model("rerank-1", types.ModelTypeRerank), wrapperReranker{}).
		Rerank(ctx, "query", []string{"one"})
	require.NoError(t, err)
	_, err = recorder.WrapVLM(model("vlm-1", types.ModelTypeVLLM), wrapperVLM{}).Predict(ctx, nil, "prompt")
	require.NoError(t, err)
	_, err = recorder.WrapASR(model("asr-1", types.ModelTypeASR), wrapperASR{}).Transcribe(ctx, nil, "audio.wav")
	require.NoError(t, err)

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.starts, 6)
	require.Len(t, store.completions, 6)
	assert.Equal(t, []string{"embedding", "embedding_batch", "embedding_batch", "rerank", "vlm", "asr"},
		[]string{
			store.starts[0].Operation, store.starts[1].Operation, store.starts[2].Operation,
			store.starts[3].Operation, store.starts[4].Operation, store.starts[5].Operation,
		})
	for _, completion := range store.completions {
		assert.Equal(t, types.ModelCallStatusSuccess, completion.Status)
		assert.False(t, completion.AccountingComplete)
		assert.Nil(t, completion.CostMicrounits)
	}
}
