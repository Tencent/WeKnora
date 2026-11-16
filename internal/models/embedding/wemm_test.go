package embedding

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// WeMM's vLLM template appends the dedicated <embedding> pooling token.
// Both retrieval sides and images must use that template, including when
// vLLM exposes the local model path instead of the Hugging Face repository ID.
// https://huggingface.co/tencent/WeMM-Embedding-2B/blob/main/embedding_chat_template.jinja
func TestWeMMVLLMPresets(t *testing.T) {
	vendor, ok := modelruntime.Get("generic")
	require.True(t, ok)
	listed := vendor.ModelsByType(types.ModelTypeEmbedding)
	for _, tc := range []struct {
		size string
		dims int
	}{{"2B", 2048}, {"4B", 2560}, {"9B", 4096}} {
		short := "WeMM-Embedding-" + tc.size
		id := "tencent/" + short
		t.Run(tc.size, func(t *testing.T) {
			visible := false
			for _, entry := range listed {
				if entry.ID == id {
					visible = true
					require.Equal(t, tc.dims, entry.Dimension)
				}
			}
			require.True(t, visible, "preset must appear in the embedding picker")
			for _, name := range []string{id, short, "/models/" + short, strings.ToLower(id)} {
				t.Run(name, func(t *testing.T) {
					resolved, err := modelruntime.Resolve(modelruntime.Ref{
						Provider: "generic", Model: name, ModelType: types.ModelTypeEmbedding,
					})
					require.NoError(t, err)
					require.True(t, resolved.Cataloged)
					require.Equal(t, tc.dims, resolved.Spec.Dimension)
					require.Equal(t, api.EmbeddingOpenAI, resolved.EmbeddingAPI)
					require.Equal(t, []string{"text", "image"}, resolved.Spec.Input)
					require.True(t, resolved.Embeddings.BatchMessages)
					require.Equal(t, 5, resolved.Embeddings.ImageBatchLimit())

					up := newUpstream(t)
					e, err := NewEmbedder(Config{
						Source: types.ModelSourceRemote, Provider: "generic", ModelName: name,
						BaseURL: up.url + "/v1", Dimensions: tc.dims, TruncatePromptTokens: 511,
					}, nil, nil)
					require.NoError(t, err)
					require.Equal(t, tc.dims, e.GetDimensions())
					ie, ok := AsImageEmbedder(e)
					require.True(t, ok, "images must work without a row override")
					contexts := []context.Context{context.Background(), types.WithEmbedQuery(context.Background())}
					for _, ctx := range contexts {
						vectors, err := e.BatchEmbed(ctx, []string{"ab", "c"})
						require.NoError(t, err)
						require.Equal(t, [][]float32{{2}, {1}}, vectors)
					}
					vectors, err := ie.BatchEmbedImages(context.Background(), pngs(6))
					require.NoError(t, err)
					require.Equal(t, [][]float32{{1}, {2}, {3}, {4}, {5}, {6}}, vectors)

					conversation := func(part map[string]any) []any {
						return []any{map[string]any{"role": "user", "content": []any{part}}}
					}
					textBatch := []any{
						conversation(map[string]any{"type": "text", "text": "ab"}),
						conversation(map[string]any{"type": "text", "text": "c"}),
					}
					imageBatch := []any{}
					for _, img := range pngs(5) {
						imageBatch = append(imageBatch, conversation(map[string]any{
							"type": "image_url", "image_url": map[string]any{"url": img.DataURI()},
						}))
					}
					messages := []any{textBatch, textBatch, imageBatch, conversation(map[string]any{
						"type": "image_url", "image_url": map[string]any{"url": pngs(6)[5].DataURI()},
					})}
					require.Len(t, up.requests, len(messages), "batch texts and split six images into five and one")
					for i, message := range messages {
						require.Equal(t, "/v1/embeddings", up.requests[i].path)
						require.Equal(t, map[string]any{
							"model": name, "encoding_format": "float",
							"messages": message,
						}, up.requests[i].body, "no raw input, legacy truncation, or extra image prompt")
					}
				})
			}
		})
	}
}

// vLLM reads matryoshka_dimensions from WeMM's config and truncates before
// normalizing. Text and image requests must use the same requested width.
func TestWeMMDimensionOverride(t *testing.T) {
	for _, size := range []string{"2B", "4B", "9B"} {
		t.Run(size, func(t *testing.T) {
			up := newUpstream(t)
			e, err := NewEmbedder(Config{
				Source: types.ModelSourceRemote, Provider: "generic",
				ModelName: "tencent/WeMM-Embedding-" + size, BaseURL: up.url + "/v1",
				Dimensions: 1024, SupportsDimensionOverride: true,
			}, nil, nil)
			require.NoError(t, err)
			_, err = e.Embed(types.WithEmbedQuery(context.Background()), "query")
			require.NoError(t, err)
			ie, ok := AsImageEmbedder(e)
			require.True(t, ok)
			_, err = ie.BatchEmbedImages(context.Background(), pngs(2))
			require.NoError(t, err)
			require.Len(t, up.requests, 2)
			for _, req := range up.requests {
				require.Equal(t, float64(1024), req.body["dimensions"])
				require.NotContains(t, req.body, "truncate_prompt_tokens")
				require.NotContains(t, req.body, "add_special_tokens")
				require.NotContains(t, req.body, "add_generation_prompt")
				require.NotContains(t, req.body, "use_activation")
			}
		})
	}
}
