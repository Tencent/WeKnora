package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"golang.org/x/sync/errgroup"
)

// multimodalEmbedder decorates a resolved remote embedder with the multimodal
// request envelopes.
//
// It is a decorator rather than another protocol in the vendor catalog because
// the envelope is not a vendor dialect: it rides on whatever OpenAI-compatible
// endpoint the model is served from. Text calls keep using the
// catalog-resolved protocol, so a KB without image vectors is never moved into
// another vector space.
type multimodalEmbedder struct {
	inner Embedder

	endpoint   string
	apiKey     string
	modelName  string
	dimensions int
	envelope   MultimodalEnvelopeStyle
	client     *http.Client
	headers    map[string]string
}

// newMultimodalEmbedder wraps inner with multimodal support. The result still
// answers the plain Embedder interface through inner; only
// BatchEmbedMultimodal changes shape.
func newMultimodalEmbedder(inner Embedder, config Config) (Embedder, error) {
	base := strings.TrimRight(config.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("multimodal embedding requires a base URL for model %s", config.ModelName)
	}
	if err := validateEmbeddingBaseURL(base); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(base, "/embeddings") {
		base += "/embeddings"
	}
	// Same rule the text path applies: a row that did not opt in keeps the
	// model's native width even where the endpoint could narrow it.
	dimensions := 0
	if config.SupportsDimensionOverride {
		dimensions = config.Dimensions
	}
	return &multimodalEmbedder{
		inner:      inner,
		endpoint:   base,
		apiKey:     config.APIKey,
		modelName:  config.ModelName,
		dimensions: dimensions,
		envelope:   ResolveMultimodalEnvelope(config.ExtraConfig),
		client:     newEmbeddingHTTPClient(60 * time.Second),
		headers:    config.CustomHeaders,
	}, nil
}

// --- Embedder: delegate to the catalog-resolved protocol ---

func (e *multimodalEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.inner.Embed(ctx, text)
}

func (e *multimodalEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.inner.BatchEmbed(ctx, texts)
}

func (e *multimodalEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *multimodalEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *multimodalEmbedder) GetModelID() string   { return e.inner.GetModelID() }

func (e *multimodalEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return e.inner.BatchEmbedWithPool(ctx, model, texts)
}

func (e *multimodalEmbedder) Capabilities() Capabilities {
	return CapabilitiesFor(true)
}

// perInputEmbedConcurrency caps the requests issued per batch: both envelopes
// are one-input-per-request, so N inputs means N round-trips.
const perInputEmbedConcurrency = 8

// BatchEmbedMultimodal encodes inputs one request at a time.
//
// A chat-envelope request encodes the whole `messages` array as ONE vector, so
// sending N inputs in one request yields a vector for their concatenation and
// leaves the other N-1 slots empty — which the vector store rejects ("halfvec
// must have at least 1 dimension") or, worse, stores as zeros. SGLang answers
// one vector per item but cannot fold text and image together, so it gets the
// same treatment for a different reason.
func (e *multimodalEmbedder) BatchEmbedMultimodal(
	ctx context.Context, inputs []Input,
) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(inputs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(perInputEmbedConcurrency)
	for i := range inputs {
		i := i
		g.Go(func() error {
			vec, err := e.embedOne(gctx, i, inputs[i])
			if err != nil {
				return err
			}
			out[i] = vec
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *multimodalEmbedder) embedOne(ctx context.Context, idx int, in Input) ([]float32, error) {
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("input[%d]: %w", idx, err)
	}
	var body any
	switch e.envelope {
	case EnvelopeSGLang:
		sglangBody, err := e.sglangBody(in)
		if err != nil {
			return nil, fmt.Errorf("input[%d]: %w", idx, err)
		}
		body = sglangBody
	default:
		body = e.chatBody(in)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("input[%d]: marshal request: %w", idx, err)
	}
	return e.post(ctx, idx, raw)
}

// --- vLLM / chat envelope ---

type chatEmbedRequest struct {
	Model          string             `json:"model"`
	Messages       []chatEmbedMessage `json:"messages"`
	EncodingFormat string             `json:"encoding_format,omitempty"`
	Dimensions     int                `json:"dimensions,omitempty"`
}

type chatEmbedMessage struct {
	Role    string          `json:"role"`
	Content []chatEmbedPart `json:"content"`
}

type chatEmbedPart struct {
	Type     string             `json:"type"`
	Text     string             `json:"text,omitempty"`
	ImageURL *chatEmbedImageURL `json:"image_url,omitempty"`
}

type chatEmbedImageURL struct {
	URL string `json:"url"`
}

// chatEmbedRole is the only role vLLM's embedding extension accepts.
const chatEmbedRole = "user"

func (e *multimodalEmbedder) chatBody(in Input) chatEmbedRequest {
	content := make([]chatEmbedPart, 0, len(in.Images)+1)
	for _, img := range in.Images {
		ref, err := img.ImageRef()
		if err != nil {
			// Validate() already rejected empty parts; a failure here means the
			// data URI could not be built, which embedOne reports as an error.
			continue
		}
		content = append(content, chatEmbedPart{
			Type:     "image_url",
			ImageURL: &chatEmbedImageURL{URL: ref},
		})
	}
	if text := strings.TrimSpace(in.Text); text != "" {
		content = append(content, chatEmbedPart{Type: "text", Text: text})
	}
	req := chatEmbedRequest{
		Model:          e.modelName,
		Messages:       []chatEmbedMessage{{Role: chatEmbedRole, Content: content}},
		EncodingFormat: "float",
	}
	if e.dimensions > 0 {
		req.Dimensions = e.dimensions
	}
	return req
}

// --- SGLang item envelope ---

type sglangEmbedRequest struct {
	Model          string            `json:"model"`
	Input          []sglangEmbedItem `json:"input"`
	EncodingFormat string            `json:"encoding_format,omitempty"`
}

type sglangEmbedItem struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

// sglangBody builds SGLang's flat item array. SGLang returns one vector PER
// ITEM and never folds them, so anything that needs more than one item — a
// mixed text+image input, or several images — would come back as several
// unrelated vectors for one requested vector. Reject it instead.
func (e *multimodalEmbedder) sglangBody(in Input) (sglangEmbedRequest, error) {
	if in.IsMixed() || len(in.Images) > 1 {
		return sglangEmbedRequest{}, fmt.Errorf("%w: %s", ErrMixedModalityUnsupported, e.modelName)
	}
	items := make([]sglangEmbedItem, 0, len(in.Images)+1)
	for _, img := range in.Images {
		ref, err := img.ImageRef()
		if err != nil {
			return sglangEmbedRequest{}, err
		}
		items = append(items, sglangEmbedItem{Image: ref})
	}
	if text := strings.TrimSpace(in.Text); text != "" {
		items = append(items, sglangEmbedItem{Text: text})
	}
	return sglangEmbedRequest{
		Model:          e.modelName,
		Input:          items,
		EncodingFormat: "float",
	}, nil
}

// --- transport ---

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (e *multimodalEmbedder) post(ctx context.Context, idx int, raw []byte) ([]float32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("input[%d]: build request: %w", idx, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("input[%d]: send request: %w", idx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("input[%d]: read response: %w", idx, err)
	}
	if resp.StatusCode != http.StatusOK {
		if len(body) > 1000 {
			body = append(body[:1000], []byte("... (truncated)")...)
		}
		logger.Errorf(ctx, "[Embedding] multimodal request failed: status=%s body=%s", resp.Status, body)
		return nil, fmt.Errorf("input[%d]: embedding API error: status %s: %s", idx, resp.Status, body)
	}

	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("input[%d]: unmarshal response: %w", idx, err)
	}
	if parsed.Error != nil && len(parsed.Data) == 0 {
		return nil, fmt.Errorf("input[%d]: embedding API error: %s", idx, parsed.Error.Message)
	}
	// One request, one vector: a server that answers otherwise read the input as
	// separate items, and picking one arbitrarily would store a vector that
	// belongs to something else.
	if len(parsed.Data) != 1 {
		return nil, fmt.Errorf("input[%d]: embedding returned %d vectors, want exactly 1", idx, len(parsed.Data))
	}
	if len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("input[%d]: embedding returned an empty vector", idx)
	}
	return parsed.Data[0].Embedding, nil
}
