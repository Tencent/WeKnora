package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakePNG is a truncated but signature-valid PNG payload: enough for MIME
// sniffing, without pulling a real image into the test binary.
var fakePNG = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))

func testImageInput() Input {
	return Input{Images: []ImagePart{{Data: fakePNG}}}
}

// A vision embedding server answers ONE request with a single vector. Both
// envelopes send one input per request, so a stub echoing one vector per input
// would hide the batching defect entirely.
const oneVectorResponse = `{"data":[{"embedding":[0.1,0.2],"index":0}]}`

// multimodalTestConfig is the Config a self-hosted vision embedding model
// produces: a generic OpenAI-compatible endpoint plus the operator's
// declarations in ExtraConfig.
func multimodalTestConfig(url, model string, extra map[string]string) Config {
	return Config{
		Source:      types.ModelSourceRemote,
		Provider:    "generic",
		BaseURL:     url,
		ModelName:   model,
		APIKey:      "key",
		Dimensions:  1536,
		ExtraConfig: extra,
	}
}

// newTestEmbedder builds the factory's embedder without the decorators.
func newTestEmbedder(t *testing.T, cfg Config) Embedder {
	t.Helper()
	allowLoopback(t)
	e, err := newEmbedder(cfg, nil, nil)
	if err != nil {
		t.Fatalf("newEmbedder: %v", err)
	}
	return e
}

// bodyRecorder collects every request body a server receives: both envelopes
// issue one request per input, so how many went out and in what shape is
// exactly what these tests pin down.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (r *bodyRecorder) add(b map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, b)
}

func (r *bodyRecorder) all() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.bodies))
	copy(out, r.bodies)
	return out
}

func newRecordingServer(t *testing.T, rec *bodyRecorder, respond func(body map[string]any) string) *httptest.Server {
	t.Helper()
	allowLoopback(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		rec.add(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respond(body)))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSupportsImageTracksDeclaredCapability(t *testing.T) {
	imageModel := newTestEmbedder(t, multimodalTestConfig("http://127.0.0.1:1",
		"gme-Qwen2-VL-2B-Instruct", map[string]string{ExtraConfigSupportsImage: "true"}))
	mm, ok := imageModel.(MultimodalEmbedder)
	if !ok {
		t.Fatal("a model declared as image-capable must expose the multimodal capability")
	}
	if !SupportsImage(imageModel) {
		t.Fatal("expected vision model to support images")
	}
	if !mm.Capabilities().UnifiedSpace {
		t.Fatal("expected multimodal model to declare a unified vector space")
	}

	textModel := newTestEmbedder(t, multimodalTestConfig("http://127.0.0.1:1", "text-embedding-3-small", nil))
	if SupportsImage(textModel) {
		t.Fatal("text-only model must not report image support")
	}
	if CapabilitiesOf(textModel).Supports(ModalityImage) {
		t.Fatal("text-only model must not list the image modality")
	}
}

func TestEmbedMultimodalFailsWithTypedErrorForTextOnlyModel(t *testing.T) {
	textModel := newTestEmbedder(t, multimodalTestConfig("http://127.0.0.1:1", "text-embedding-3-small", nil))

	_, err := EmbedMultimodal(context.Background(), textModel, testImageInput())
	if !errors.Is(err, ErrMultimodalUnsupported) {
		t.Fatalf("expected ErrMultimodalUnsupported, got %v", err)
	}
}

// NewEmbedder wraps every provider in decorators; if they drop the capability,
// image support silently disappears in production but not in unit tests.
func TestMultimodalCapabilitySurvivesDecoratorWrapping(t *testing.T) {
	allowLoopback(t)
	inner, err := newEmbedder(multimodalTestConfig("http://127.0.0.1:1", "Qwen/Qwen3-VL-Embedding-2B", nil), nil, nil)
	if err != nil {
		t.Fatalf("newEmbedder: %v", err)
	}

	wrapped := Embedder(inner)
	wrapped = wrapEmbeddingConcurrency(wrapped, 0)
	wrapped = &debugEmbedder{inner: wrapped}
	wrapped = &langfuseEmbedder{inner: wrapped}

	if !SupportsImage(wrapped) {
		t.Fatal("multimodal capability lost through the decorator chain")
	}
	if !wrapped.(MultimodalEmbedder).Capabilities().Supports(ModalityImage) {
		t.Fatal("Capabilities must report image support through the decorator chain")
	}

	// A text-only model must stay text-only through the same chain, otherwise
	// capabilities become a lie and downstream code sends images to a model
	// that rejects them.
	textInner, err := newEmbedder(multimodalTestConfig("http://127.0.0.1:1", "text-embedding-v3", nil), nil, nil)
	if err != nil {
		t.Fatalf("newEmbedder: %v", err)
	}
	textWrapped := Embedder(&langfuseEmbedder{inner: &debugEmbedder{inner: wrapEmbeddingConcurrency(textInner, 0)}})
	if SupportsImage(textWrapped) {
		t.Fatal("decorated text-only model must not report image support")
	}
}

// A batch must fan out into one request per input: N inputs in ONE `messages`
// array embed their concatenation and leave the other slots empty, which reach
// the vector store as zero-dimension rows and fail the whole save. The stub
// replies with a single vector like a real vLLM server.
func TestChatEnvelopeUsesOneRequestPerInput(t *testing.T) {
	rec := &bodyRecorder{}
	server := newRecordingServer(t, rec, func(map[string]any) string { return oneVectorResponse })

	embedder := newTestEmbedder(t, multimodalTestConfig(server.URL, "tencent/WeMM-Embedding-2B", nil))

	vectors, err := BatchEmbedMultimodalWith(context.Background(), embedder, []Input{
		{Text: "find the bridge bearing layout", Images: []ImagePart{{Data: fakePNG}}},
		{Text: "a second query"},
	})
	if err != nil {
		t.Fatalf("BatchEmbedMultimodal: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vectors))
	}
	// Every slot must carry a real vector. A batch that returned one vector and
	// left the rest nil is the regression this guards.
	for i, v := range vectors {
		if len(v) == 0 {
			t.Fatalf("vectors[%d] is empty: batch was answered as a single conversation", i)
		}
	}

	bodies := rec.all()
	if len(bodies) != 2 {
		t.Fatalf("expected one request per input, got %d request(s)", len(bodies))
	}

	// Images cannot travel in the plain `input` array — the chat envelope is
	// the only way to reach a vision embedding server.
	for i, body := range bodies {
		if _, ok := body["input"]; ok {
			t.Fatalf("chat-style request must not carry an input array: %v", body)
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 1 {
			t.Fatalf("request %d: expected 1 chat message, got %v", i, body["messages"])
		}
	}

	// Requests are issued concurrently, so identify them by payload rather
	// than by arrival order.
	imageIdx, textIdx := -1, -1
	for i, body := range bodies {
		parts, _ := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		hasImage := false
		for _, p := range parts {
			if p.(map[string]any)["type"] == "image_url" {
				hasImage = true
			}
		}
		if hasImage {
			imageIdx = i
		} else {
			textIdx = i
		}
	}
	if imageIdx < 0 {
		t.Fatalf("no request carried the image: %v", bodies)
	}
	if textIdx < 0 {
		t.Fatalf("no request carried the plain text input: %v", bodies)
	}
	if imageIdx == textIdx {
		t.Fatalf("both inputs travelled in one request: %v", bodies[imageIdx])
	}
	imageReq, textReq := bodies[imageIdx], bodies[textIdx]

	parts, ok := imageReq["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %v", parts)
	}
	imagePart := parts[0].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("expected image_url part, got %v", imagePart)
	}
	url := imagePart["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("expected a png data URI, got %q", url)
	}
	if parts[1].(map[string]any)["type"] != "text" {
		t.Fatalf("expected text part, got %v", parts[1])
	}

	// The text input must travel on its own request, not appended to the
	// image request's messages.
	textParts, ok := textReq["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if !ok || len(textParts) != 1 || textParts[0].(map[string]any)["text"] != "a second query" {
		t.Fatalf("text request must carry only its own input, got %v", textParts)
	}
}

func TestImagePartImageRefFallsBackToURLAndSniffsMIME(t *testing.T) {
	byURL, err := ImagePart{URL: "https://example.com/page.png"}.ImageRef()
	if err != nil {
		t.Fatalf("ImageRef: %v", err)
	}
	if byURL != "https://example.com/page.png" {
		t.Fatalf("expected the URL to pass through, got %q", byURL)
	}

	byData, err := ImagePart{Data: fakePNG}.ImageRef()
	if err != nil {
		t.Fatalf("ImageRef: %v", err)
	}
	if !strings.HasPrefix(byData, "data:image/png;base64,iVBOR") {
		t.Fatalf("MIME should be sniffed as png, got %q", byData)
	}

	if _, err := (ImagePart{}).ImageRef(); err == nil {
		t.Fatal("expected an error for an image with neither data nor url")
	}
}

func TestInputValidate(t *testing.T) {
	if err := (Input{}).Validate(); err == nil {
		t.Fatal("empty input must be rejected")
	}
	if err := (Input{Text: "query"}).Validate(); err != nil {
		t.Fatalf("text-only input is valid: %v", err)
	}
	if err := (Input{Images: []ImagePart{{}}}).Validate(); err == nil {
		t.Fatal("image without payload must be rejected")
	}
}

func TestResolveMultimodalPrefersExplicitDeclaration(t *testing.T) {
	// A self-hosted checkpoint whose name carries no hint must still be usable.
	if !ResolveMultimodal("wemm-local", map[string]string{ExtraConfigSupportsImage: "true"}) {
		t.Fatal("explicit opt-in must win over the name heuristic")
	}
	// ...and a "vision"-named model fronting a text-only endpoint must be
	// switchable off without renaming anything.
	if ResolveMultimodal("tongyi-embedding-vision-plus", map[string]string{ExtraConfigSupportsImage: "false"}) {
		t.Fatal("explicit opt-out must win over the name heuristic")
	}
	if !ResolveMultimodal("tongyi-embedding-vision-plus", nil) {
		t.Fatal("name heuristic should detect vision models")
	}
	if ResolveMultimodal("text-embedding-3-small", nil) {
		t.Fatal("name heuristic should not flag text-only models")
	}
}

// The most common self-hosted vision embedding models do not contain the word
// "vision"; without these hints the switch is rejected for exactly the
// deployments the documentation recommends.
func TestLooksMultimodalModelCoversSelfHostedVisionEmbedders(t *testing.T) {
	for _, name := range []string{
		"Qwen/Qwen3-VL-Embedding-2B",
		"Qwen3-VL-Embedding-8B",
		"nvidia/llama-nemotron-embed-vl-1b-v2",
		"TIGER-Lab/VLM2Vec-Full",
	} {
		if !LooksMultimodalModel(name) {
			t.Fatalf("%s should be detected as image-capable", name)
		}
	}
	for _, name := range []string{
		"text-embedding-3-small",
		"bge-m3",
		"Qwen3-Embedding-8B",
	} {
		if LooksMultimodalModel(name) {
			t.Fatalf("%s must not be detected as image-capable", name)
		}
	}
}
