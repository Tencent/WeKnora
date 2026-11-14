// Package voyageembeddings implements Voyage AI's three embedding endpoints.
//
//   - Text: POST {base}/embeddings with {model, input[]} answering
//     {data: [{embedding, index}]}.
//   - Contextualized: POST {base}/contextualizedembeddings, where each input is
//     one document and each result carries that document's chunk vectors.
//   - Multimodal: POST {base}/multimodalembeddings, where each input is a
//     {"content": [...]} of text and image parts and yields one vector.
//
// References:
//
//	https://docs.voyageai.com/reference/embeddings-api
//	https://docs.voyageai.com/reference/contextualized-embeddings-api
//	https://docs.voyageai.com/reference/multimodal-embeddings-api
//
// The three share the parameters WeKnora uses (input_type, output_dimension),
// the index-based placement of vectors and Voyage's {"detail": "..."} error
// body, which is why they live in one package. Their request bodies differ
// and are pinned separately by the golden tests. There is no official Go SDK
// for Voyage, so this is a REST client over the shared endpoint, retry and
// placement helpers, like every other protocol here.
//
// A request carries exactly what the model entry declares: output_dimension
// goes out only when the row asked for a width and the model accepts one;
// truncation, output_dtype and encoding_format stay at the API's defaults
// (float vectors, an over-long input truncated).
package voyageembeddings

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/voyageapi"
)

// AutoChunkSize is the chunk_size sent with every auto-chunked contextualized
// request: the documented ceiling (32K tokens, "chunk_size must not exceed
// 32K"). A WeKnora chunk is far shorter, so each text stays one chunk.
const AutoChunkSize = 32_000

// Config is everything a client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.EmbeddingsSettings
	// Dimensions is the width the row asked for; it is sent only when the
	// model entry names a field for it.
	Dimensions int
	Retry      api.RetryPolicy
}

type base struct {
	cfg         Config
	defaultPath string
}

func (b base) url() string {
	if b.cfg.Endpoint.URL != "" {
		return b.cfg.Endpoint.Resolve("")
	}
	path := b.cfg.Settings.Path
	if path == "" {
		path = b.defaultPath
	}
	if strings.HasSuffix(strings.TrimRight(b.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return b.cfg.Endpoint.Resolve(path)
}

// body starts a request with the fields every endpoint takes. input_type is
// document or query only: those are the two values Voyage defines, and the
// neutral kinds already match them.
func (b base) body(key string, input any, kind api.EmbedInputType) (map[string]any, error) {
	s := b.cfg.Settings
	if kind != api.EmbedDocument && kind != api.EmbedQuery {
		return nil, fmt.Errorf("voyage embedding: input type must be %q or %q, got %q",
			api.EmbedDocument, api.EmbedQuery, kind)
	}
	body := map[string]any{
		"model": b.cfg.Endpoint.Model,
		key:     input,
	}
	if field := s.InputTypeField; field != "" {
		body[field] = s.InputTypeValue(kind)
	}
	if s.DimensionsField != "" && b.cfg.Dimensions > 0 {
		body[s.DimensionsField] = b.cfg.Dimensions
	}
	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body, nil
}

func (b base) post(ctx context.Context, body map[string]any, out any) error {
	err := b.cfg.Endpoint.PostJSONWithRetry(ctx, b.url(), body, out, b.cfg.Retry, "embedding")
	return voyageapi.WrapError(err)
}

// vector is one embedding as the endpoints return it. Index is a pointer so
// that an absent index is an error instead of slot 0.
type vector struct {
	Embedding []float32 `json:"embedding"`
	Index     *int      `json:"index"`
}

// place puts vectors back in input order and refuses a reply that does not
// cover every input exactly once.
func place(want int, got []vector, what string) ([][]float32, error) {
	for i := range got {
		if got[i].Index == nil {
			return nil, fmt.Errorf("voyage embedding: %s %d has no index", what, i)
		}
	}
	return api.PlaceEmbeddings(want, len(got), func(i int) (int, []float32) {
		return *got[i].Index, got[i].Embedding
	})
}

// Text is the plain text embedding endpoint.
type Text struct{ base }

// NewText builds a client for POST /embeddings.
func NewText(cfg Config) *Text { return &Text{base{cfg: cfg, defaultPath: "/embeddings"}} }

// BuildRequestBody is the golden-test entry point.
func (c *Text) BuildRequestBody(texts []string, kind api.EmbedInputType) (map[string]any, error) {
	return c.body("input", texts, kind)
}

// Embed vectorizes one batch, in the order it was given.
func (c *Text) Embed(ctx context.Context, texts []string, kind api.EmbedInputType) ([][]float32, error) {
	body, err := c.BuildRequestBody(texts, kind)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Data []vector `json:"data"`
	}
	if err := c.post(ctx, body, &decoded); err != nil {
		return nil, err
	}
	return place(len(texts), decoded.Data, "embedding")
}

// Contextualized is the contextualized chunk embedding endpoint.
//
// WeKnora hands an embedder N texts and expects N vectors back, one per
// stored chunk. A document-mode request therefore sends the texts as a flat
// list of documents with auto-chunking at AutoChunkSize, and every text must
// come back as exactly one chunk; a text the API splits further has no single
// vector, and is an error rather than a silently dropped or averaged one.
// Auto-chunking is only valid for input_type "document", so queries are sent
// as one-element lists without it.
type Contextualized struct{ base }

// NewContextualized builds a client for POST /contextualizedembeddings.
func NewContextualized(cfg Config) *Contextualized {
	return &Contextualized{base{cfg: cfg, defaultPath: "/contextualizedembeddings"}}
}

// BuildRequestBody is the golden-test entry point.
func (c *Contextualized) BuildRequestBody(texts []string, kind api.EmbedInputType) (map[string]any, error) {
	if kind == api.EmbedQuery {
		inputs := make([][]string, len(texts))
		for i, text := range texts {
			inputs[i] = []string{text}
		}
		return c.body("inputs", inputs, kind)
	}
	body, err := c.body("inputs", texts, kind)
	if err != nil {
		return nil, err
	}
	body["enable_auto_chunking"] = true
	body["chunk_size"] = AutoChunkSize
	return body, nil
}

// Embed vectorizes one batch, in the order it was given.
func (c *Contextualized) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	body, err := c.BuildRequestBody(texts, kind)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Data []struct {
			Index *int     `json:"index"`
			Data  []vector `json:"data"`
		} `json:"data"`
	}
	if err := c.post(ctx, body, &decoded); err != nil {
		return nil, err
	}
	perText := make([]vector, len(decoded.Data))
	for i, doc := range decoded.Data {
		if len(doc.Data) != 1 {
			// Name the input by the index Voyage reports; the reply may not
			// be in input order.
			at := i
			if doc.Index != nil {
				at = *doc.Index
			}
			return nil, fmt.Errorf(
				"voyage contextualized embedding: input %d came back as %d chunks; one vector per input is required",
				at, len(doc.Data))
		}
		perText[i] = vector{Embedding: doc.Data[0].Embedding, Index: doc.Index}
	}
	return place(len(texts), perText, "document")
}

// Multimodal is the multimodal embedding endpoint. Every input is a content
// list; WeKnora sends a text or an image per input, never a mix, so that a
// vector is comparable with any other text or image vector of the same model.
type Multimodal struct{ base }

// NewMultimodal builds a client for POST /multimodalembeddings.
func NewMultimodal(cfg Config) *Multimodal {
	return &Multimodal{base{cfg: cfg, defaultPath: "/multimodalembeddings"}}
}

func content(parts ...map[string]any) map[string]any {
	list := make([]any, len(parts))
	for i, part := range parts {
		list[i] = part
	}
	return map[string]any{"content": list}
}

// BuildRequestBody is the golden-test entry point for texts.
func (c *Multimodal) BuildRequestBody(texts []string, kind api.EmbedInputType) (map[string]any, error) {
	inputs := make([]any, len(texts))
	for i, text := range texts {
		inputs[i] = content(map[string]any{"type": "text", "text": text})
	}
	return c.body("inputs", inputs, kind)
}

// BuildImageRequestBody is the golden-test entry point for images, which
// travel as image_base64 data URLs. A request is all base64 or all URLs,
// and WeKnora holds image bytes, never URLs the vendor could reach.
func (c *Multimodal) BuildImageRequestBody(
	images []api.EmbedImage, kind api.EmbedInputType,
) (map[string]any, error) {
	inputs := make([]any, len(images))
	for i, img := range images {
		inputs[i] = content(map[string]any{"type": "image_base64", "image_base64": img.DataURI()})
	}
	return c.body("inputs", inputs, kind)
}

// Embed vectorizes one batch of texts, in the order it was given.
func (c *Multimodal) Embed(ctx context.Context, texts []string, kind api.EmbedInputType) ([][]float32, error) {
	body, err := c.BuildRequestBody(texts, kind)
	if err != nil {
		return nil, err
	}
	return c.postVectors(ctx, body, len(texts))
}

// AcceptsImages is always true: the image part is part of the schema.
func (c *Multimodal) AcceptsImages() bool { return true }

// EmbedImages vectorizes one batch of images, in the order it was given.
func (c *Multimodal) EmbedImages(
	ctx context.Context, images []api.EmbedImage, kind api.EmbedInputType,
) ([][]float32, error) {
	body, err := c.BuildImageRequestBody(images, kind)
	if err != nil {
		return nil, err
	}
	return c.postVectors(ctx, body, len(images))
}

func (c *Multimodal) postVectors(ctx context.Context, body map[string]any, want int) ([][]float32, error) {
	var decoded struct {
		Data []vector `json:"data"`
	}
	if err := c.post(ctx, body, &decoded); err != nil {
		return nil, err
	}
	return place(want, decoded.Data, "embedding")
}
