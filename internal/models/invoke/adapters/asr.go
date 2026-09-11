package adapters

// asr.go — P3 strangler: the ASR facet (design §6.2). Behavior port of v1
// internal/models/asr/openai.go: EVERY ASR vendor rides the OpenAI-compatible
// POST /audio/transcriptions multipart API, so one openai-shape facet covers
// the whole type.
//
// Wire notes (go-openai SDK createFormWriter order, mirrored here):
//   model → response_format → [language] → file. The v1 client always sent
//   response_format=verbose_json (segments + timestamps) and defaulted the
//   upload filename to audio.mp3 when the caller passed none.
//   The multipart body is fully in-memory bytes (v1 input was bytes), so it
//   rides Request.Body — replayable, hence executor-retryable (a mild v1
//   enhancement: the SDK path had no retry). Boundary randomization makes
//   byte-goldens impossible; the wire is asserted semantically instead.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
)

const asrDefaultBaseURL = "https://api.openai.com/v1"

// BuildASRRequest ports the v1 multipart transcription request.
func BuildASRRequest(
	ep invoke.Endpoint, model string, opts *invoke.ASROptions,
) (*invoke.Request, error) {
	if len(opts.Audio) == 0 {
		return nil, fmt.Errorf("audio bytes are empty")
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "audio.mp3" // v1 default for MIME detection
	}
	base := ep.BaseURL
	if base == "" {
		base = asrDefaultBaseURL
	}

	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	// Field order mirrors the v1 SDK writer.
	if err := writer.WriteField("model", model); err != nil {
		return nil, fmt.Errorf("writing model field: %w", err)
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("writing response_format field: %w", err)
	}
	if opts.Language != "" {
		if err := writer.WriteField("language", opts.Language); err != nil {
			return nil, fmt.Errorf("writing language field: %w", err)
		}
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("creating file field: %w", err)
	}
	if _, err := part.Write(opts.Audio); err != nil {
		return nil, fmt.Errorf("writing audio bytes: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart writer: %w", err)
	}

	header := http.Header{}
	header.Set("Content-Type", writer.FormDataContentType())
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    strings.TrimRight(base, "/") + "/audio/transcriptions",
		Header: header,
		Body:   buf.Bytes(),
		// multipart boundary Content-Type must not be overridden by user
		// custom headers (design §6.4 protected-header rule).
		ProtectedHeaders: []string{"Content-Type"},
	}, nil
}

type openaiASRResponse struct {
	Text     string `json:"text"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// ParseASRResponse ports the v1 verbose_json handling: TrimSpace on the text
// and every segment text.
func ParseASRResponse(_ int, _ http.Header, body []byte) (*invoke.ASRResponse, error) {
	var resp openaiASRResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	out := &invoke.ASRResponse{Text: strings.TrimSpace(resp.Text)}
	for _, seg := range resp.Segments {
		out.Segments = append(out.Segments, invoke.ASRSegment{
			Start: seg.Start,
			End:   seg.End,
			Text:  strings.TrimSpace(seg.Text),
		})
	}
	return out, nil
}

// asrCapsFor resolves the provider-level ASR shard (nil when the catalog does
// not serve ASR).
func asrCapsFor(name provider.ProviderName) *provider.ASRCaps {
	p, ok := provider.Get(name)
	if !ok {
		return nil
	}
	caps := p.Info().EffectiveCapabilities()
	return caps.ASR
}

// openaiASREmbeddingAdapter serves chat+embedding+ASR vendors without a
// rerank shard (azure_openai).
type openaiASREmbeddingAdapter struct {
	openaiEmbeddingAdapter
}

var _ invoke.ASRAdapter = (*openaiASREmbeddingAdapter)(nil)

// Capabilities unions the three served shards.
func (a *openaiASREmbeddingAdapter) Capabilities() provider.Capabilities {
	caps := a.openaiEmbeddingAdapter.Capabilities()
	caps.ASR = asrCapsFor(a.name)
	return caps
}

// BuildASRRequest builds the shared openai-shape transcription request.
func (a *openaiASREmbeddingAdapter) BuildASRRequest(
	ep invoke.Endpoint, model string, opts *invoke.ASROptions,
) (*invoke.Request, error) {
	return BuildASRRequest(ep, model, opts)
}

// ParseASRResponse parses the shared verbose_json transcription response.
func (a *openaiASREmbeddingAdapter) ParseASRResponse(
	status int, header http.Header, body []byte,
) (*invoke.ASRResponse, error) {
	return ParseASRResponse(status, header, body)
}

// openaiASRRerankAdapter is the quad-facet composite for the ASR-serving
// openai-family vendors (openai/generic/azure_openai/siliconflow/gpustack —
// all ride the shared transcriptions shape; v1 had exactly one ASR client).
type openaiASRRerankAdapter struct {
	openaiRerankAdapter
}

var _ invoke.ASRAdapter = (*openaiASRRerankAdapter)(nil)

// Capabilities unions all four served shards.
func (a *openaiASRRerankAdapter) Capabilities() provider.Capabilities {
	caps := a.openaiRerankAdapter.Capabilities()
	caps.ASR = asrCapsFor(a.name)
	return caps
}

// BuildASRRequest builds the shared openai-shape transcription request.
func (a *openaiASRRerankAdapter) BuildASRRequest(
	ep invoke.Endpoint, model string, opts *invoke.ASROptions,
) (*invoke.Request, error) {
	return BuildASRRequest(ep, model, opts)
}

// ParseASRResponse parses the shared verbose_json transcription response.
func (a *openaiASRRerankAdapter) ParseASRResponse(
	status int, header http.Header, body []byte,
) (*invoke.ASRResponse, error) {
	return ParseASRResponse(status, header, body)
}
