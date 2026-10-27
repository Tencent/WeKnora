package rerank

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngs returns n images of 1..n bytes; the stand-in upstream scores an image
// by its size, so the largest ranks first.
func pngs(n int) []Image {
	out := make([]Image, n)
	for i := range out {
		out[i] = Image{Data: bytes.Repeat([]byte{0x89}, i+1), MIMEType: "image/png"}
	}
	return out
}

func newImageReranker(t *testing.T, up *upstream, provider, model, base string) Reranker {
	t.Helper()
	r, err := NewReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: provider, ModelName: model,
		BaseURL: up.url + base, APIKey: "k",
	})
	require.NoError(t, err)
	return r
}

// TestRerankImagesWireFormatPerVendor pins the image request each multimodal
// reranker is sent. The image part is the shape the vendor's reference gives,
// as cited on the vendor's rerank compat.
func TestRerankImagesWireFormatPerVendor(t *testing.T) {
	const query = "which chart shows revenue"
	uri := "data:image/png;base64,iQ==" // one 0x89 byte
	cases := []struct {
		name, provider, model, base string
		wantPath                    string
		wantRequests                int
		wantBody                    map[string]any // the first request's body, exactly
	}{
		{
			name: "jina m0 sends image documents, one per request", provider: "jina",
			model: "jina-reranker-m0", base: "/v1", wantPath: "/v1/rerank", wantRequests: 3,
			wantBody: map[string]any{
				"model": "jina-reranker-m0", "query": query, "return_documents": false,
				"documents": []any{map[string]any{"image": uri}},
			},
		},
		{
			name: "nvidia vl sends image-only passages without text", provider: "nvidia",
			model: "nvidia/llama-nemotron-rerank-vl-1b-v2", base: "/v1/retrieval/nvidia/reranking",
			wantPath: "/v1/retrieval/nvidia/reranking", wantRequests: 1,
			wantBody: map[string]any{
				"model": "nvidia/llama-nemotron-rerank-vl-1b-v2", "query": map[string]any{"text": query},
				"truncate": "END",
				"passages": []any{
					map[string]any{"image": uri},
					map[string]any{"image": "data:image/png;base64,iYk="},
					map[string]any{"image": "data:image/png;base64,iYmJ"},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			ir, ok := AsImageReranker(newImageReranker(t, up, tc.provider, tc.model, tc.base))
			require.True(t, ok, "the catalog lists this model as scoring images")

			got, err := ir.RerankImages(context.Background(), query, pngs(3))
			require.NoError(t, err)

			require.Len(t, up.requests, tc.wantRequests)
			assert.Equal(t, tc.wantPath, up.requests[0].path)
			bodies := make([]map[string]any, len(up.requests))
			for i, req := range up.requests {
				bodies[i] = req.body
			}
			assert.Contains(t, bodies, tc.wantBody)

			// Ranked best first, indices into the images, on the 0..1 scale.
			require.Len(t, got, 3)
			assert.Equal(t, []int{2, 1, 0}, []int{got[0].Index, got[1].Index, got[2].Index})
			for _, r := range got {
				assert.GreaterOrEqual(t, r.RelevanceScore, 0.0)
				assert.LessOrEqual(t, r.RelevanceScore, 1.0)
			}
		})
	}
}

func TestRerankImagesNeedsBothTheModelAndTheEndpoint(t *testing.T) {
	cases := []struct{ name, provider, model, base string }{
		{"text model on a vendor with an image field", "jina", "jina-reranker-v3", "/v1"},
		{"text model on the NIM shape", "nvidia", "nvidia/nv-rerankqa-mistral-4b-v3", "/v1/retrieval/nvidia/reranking"},
		{"generic Cohere-shaped endpoint", "generic", "bge-reranker-v2-m3", "/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			r := newImageReranker(t, up, tc.provider, tc.model, tc.base)
			_, ok := AsImageReranker(r)
			assert.False(t, ok)
			// Every decorator carries the method; skipping AsImageReranker
			// still gets a refusal rather than a request.
			_, err := r.(ImageReranker).RerankImages(context.Background(), "q", pngs(1))
			assert.True(t, errors.Is(err, ErrImagesUnsupported), "got %v", err)
			assert.Empty(t, up.requests)
		})
	}
}

func TestRerankImagesRefusesFormatsTheVendorDoesNotList(t *testing.T) {
	up := newUpstream(t)
	ir, ok := AsImageReranker(newImageReranker(t, up, "nvidia",
		"nvidia/llama-nemotron-rerank-vl-1b-v2", "/v1/retrieval/nvidia/reranking"))
	require.True(t, ok)
	assert.Contains(t, ir.ImageLimits().MIMETypes, "image/webp")
	_, err := ir.RerankImages(context.Background(), "q",
		append(pngs(1), Image{Data: []byte{1}, MIMEType: "image/svg+xml"}))
	assert.ErrorContains(t, err, "image 1")
	assert.Empty(t, up.requests, "refused before sending anything")
}

// The factory adds the debug decorator only when LLM debug logging is on.
func TestRerankImagesPassesThroughEveryDecorator(t *testing.T) {
	up := newUpstream(t)
	inner := newImageReranker(t, up, "jina", "jina-reranker-m0", "/v1")
	wrapped := &debugReranker{inner: &langfuseReranker{inner: inner}}
	ir, ok := AsImageReranker(wrapped)
	require.True(t, ok)
	got, err := ir.RerankImages(context.Background(), "q", pngs(2))
	require.NoError(t, err)
	assert.Equal(t, 1, got[0].Index)

	text := &debugReranker{inner: newImageReranker(t, up, "jina", "jina-reranker-v3", "/v1")}
	_, ok = AsImageReranker(text)
	assert.False(t, ok)
}
