package embedding

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngs returns n images of 1..n bytes, so each answer's vector names its image.
func pngs(n int) []Image {
	out := make([]Image, n)
	for i := range out {
		out[i] = Image{Data: bytes.Repeat([]byte{0x89}, i+1), MIMEType: "image/png"}
	}
	return out
}

func imageEmbedder(t *testing.T, up *upstream, provider, model, base string, spec *types.ModelSpecOverride) Embedder {
	t.Helper()
	e, err := NewEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: provider, ModelName: model,
		BaseURL: up.url + base, APIKey: "k", Dimensions: 256, Spec: spec,
	}, nil, nil)
	require.NoError(t, err)
	return e
}

// TestImageEmbeddingWireFormatPerVendor pins the image request each
// multimodal vendor is sent, and how a batch is split at the documented
// number of images per request. The image part of every body is the shape
// the vendor's reference gives, as cited on the protocol client.
func TestImageEmbeddingWireFormatPerVendor(t *testing.T) {
	uri := "data:image/png;base64,iQ==" // one 0x89 byte
	const dashPath = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"
	cases := []struct {
		name, provider, model, base string
		images                      int
		wantPath                    string
		wantRequests                int
		wantBody                    map[string]any // the first request's body, exactly
	}{
		{
			name: "volcengine fuses, so one image per request", provider: "volcengine",
			model: "doubao-embedding-vision-251215", base: "/api/v3", images: 2, wantRequests: 2,
			wantPath: "/api/v3/embeddings/multimodal",
			wantBody: map[string]any{
				"model": "doubao-embedding-vision-251215", "encoding_format": "float",
				"input": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}}},
			},
		},
		{
			name: "aliyun tongyi vision plus takes eight images", provider: "aliyun",
			model: "tongyi-embedding-vision-plus", base: "/compatible-mode/v1", images: 9, wantRequests: 2,
			wantPath: dashPath,
			wantBody: map[string]any{
				"model": "tongyi-embedding-vision-plus",
				"input": map[string]any{"contents": func() []any {
					out := []any{}
					for _, img := range pngs(8) {
						out = append(out, map[string]any{"image": img.DataURI()})
					}
					return out
				}()},
			},
		},
		{
			name: "aliyun multimodal-embedding-v1 takes one image", provider: "aliyun",
			model: "multimodal-embedding-v1", base: "/compatible-mode/v1", images: 2, wantRequests: 2,
			wantPath: dashPath,
			wantBody: map[string]any{
				"model": "multimodal-embedding-v1",
				"input": map[string]any{"contents": []any{map[string]any{"image": uri}}},
			},
		},
		{
			name: "gemini embeds each image as its own inlineData request", provider: "gemini",
			model: "gemini-embedding-2", base: "/v1beta", images: 2, wantRequests: 1,
			wantPath: "/v1beta/models/gemini-embedding-2:batchEmbedContents",
			wantBody: map[string]any{"requests": []any{
				map[string]any{
					"model": "models/gemini-embedding-2",
					"content": map[string]any{"parts": []any{
						map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "iQ=="}},
					}},
				},
				map[string]any{
					"model": "models/gemini-embedding-2",
					"content": map[string]any{"parts": []any{
						map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "iYk="}},
					}},
				},
			}},
		},
		{
			name: "jina sends an image object in input", provider: "jina",
			model: "jina-embeddings-v5-omni-small", base: "/v1", images: 2, wantRequests: 2,
			wantPath: "/v1/embeddings",
			wantBody: map[string]any{
				"model": "jina-embeddings-v5-omni-small", "truncate": true,
				"input": []any{map[string]any{"image": uri}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			ie, ok := AsImageEmbedder(imageEmbedder(t, up, tc.provider, tc.model, tc.base, nil))
			require.True(t, ok, "the catalog lists this model as taking images")

			images := pngs(tc.images)
			got, err := ie.BatchEmbedImages(context.Background(), images)
			require.NoError(t, err)
			want := make([][]float32, len(images))
			for i, img := range images {
				want[i] = []float32{float32(len(img.Data))}
			}
			assert.Equal(t, want, got, "every vector must come back in its own slot")

			require.Len(t, up.requests, tc.wantRequests)
			assert.Equal(t, tc.wantPath, up.requests[0].path)
			assert.Equal(t, tc.wantBody, up.requests[0].body)
		})
	}
}

func TestImageEmbeddingNeedsBothTheModelAndTheEndpoint(t *testing.T) {
	declare := &types.ModelSpecOverride{Input: []string{"text", "image"}}
	cases := []struct {
		name, provider, model string
		spec                  *types.ModelSpecOverride
		want                  bool
	}{
		{"text model on a vendor with an image field", "jina", "jina-embeddings-v3", nil, false},
		{"text model on the OpenAI shape", "openai", "text-embedding-3-small", nil, false},
		// A generic OpenAI-compatible server has no image input to send to,
		// whatever the row declares.
		{"declared, but the endpoint names no image field", "generic", "my-clip", declare, false},
		{"declared on a vendor that names one", "jina", "my-jina-omni", declare, true},
		{
			"the row narrows a multimodal catalog entry", "volcengine", "doubao-embedding-vision-251215",
			&types.ModelSpecOverride{Input: []string{"text"}}, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			e := imageEmbedder(t, up, tc.provider, tc.model, "/v1", tc.spec)
			_, ok := AsImageEmbedder(e)
			assert.Equal(t, tc.want, ok)
			if !tc.want {
				// Every decorator carries the method, so a caller that skips
				// AsImageEmbedder still gets a refusal rather than a request.
				_, err := e.(ImageEmbedder).BatchEmbedImages(context.Background(), pngs(1))
				assert.True(t, errors.Is(err, ErrImagesUnsupported), "got %v", err)
				assert.Empty(t, up.requests)
			}
		})
	}
}

func TestImageEmbeddingRefusesWhatTheVendorDocumentsItWillNotTake(t *testing.T) {
	up := newUpstream(t)
	gemini, ok := AsImageEmbedder(imageEmbedder(t, up, "gemini", "gemini-embedding-2", "/v1beta", nil))
	require.True(t, ok)
	assert.Equal(t, []string{"image/png", "image/jpeg"}, gemini.ImageLimits().MIMETypes)
	_, err := gemini.BatchEmbedImages(context.Background(),
		append(pngs(1), Image{Data: []byte{1}, MIMEType: "image/webp"}))
	assert.ErrorContains(t, err, "image 1")

	jina, ok := AsImageEmbedder(imageEmbedder(t, up, "jina", "jina-embeddings-v4", "/v1", nil))
	require.True(t, ok)
	limit := jina.ImageLimits().MaxBytes
	require.Equal(t, 5_000_000, limit)
	_, err = jina.BatchEmbedImages(context.Background(),
		[]Image{{Data: make([]byte, limit+1), MIMEType: "image/png"}})
	assert.ErrorContains(t, err, "limit")

	_, err = jina.BatchEmbedImages(context.Background(), []Image{{MIMEType: "image/png"}})
	assert.ErrorContains(t, err, "empty")

	// Refused before sending anything: no half-embedded batch.
	assert.Empty(t, up.requests)
}

// The factory adds the debug and Langfuse decorators only when those are
// switched on, so the image side through them is exercised here directly.
func TestImageEmbeddingPassesThroughEveryDecorator(t *testing.T) {
	up := newUpstream(t)
	inner := imageEmbedder(t, up, "jina", "jina-embeddings-v4", "/v1", nil)
	wrapped := &debugEmbedder{inner: &langfuseEmbedder{inner: inner}}

	ie, ok := AsImageEmbedder(wrapped)
	require.True(t, ok)
	assert.Equal(t, 5_000_000, ie.ImageLimits().MaxBytes)
	got, err := ie.BatchEmbedImages(context.Background(), pngs(2))
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, got)

	textOnly := imageEmbedder(t, up, "openai", "text-embedding-3-small", "/v1", nil)
	text := &debugEmbedder{inner: &langfuseEmbedder{inner: textOnly}}
	_, ok = AsImageEmbedder(text)
	assert.False(t, ok)
	assert.Equal(t, ImageLimits{}, text.ImageLimits())
}
