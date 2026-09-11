package invoke

// Minimal acceptance tests (P1a): entry dispatch, executor retry/SSRF,
// custom-header overlay rules, and the multimodal degradation retry — all
// over httptest.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowLoopbackSSRF whitelists loopback for the duration of one test so
// httptest servers are reachable through the executor's unified SSRF gate.
func allowLoopbackSSRF(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	secutils.ResetSSRFOutboundValidationCacheForTest()
	t.Cleanup(func() {
		secutils.ResetSSRFWhitelistForTest()
		secutils.ResetSSRFOutboundValidationCacheForTest()
	})
}

// fakeChatAdapter is a minimal ChatAdapter for entry/executor tests. It
// records the wire request and answers with canned bodies.
type fakeChatAdapter struct {
	baseURL string
}

func (f fakeChatAdapter) Provider() string { return "fake" }

func (f fakeChatAdapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: &provider.ChatCaps{}}
}

func (f fakeChatAdapter) BuildChatRequest(ep Endpoint, model string, opts *ChatOptions) (*Request, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "images": hasImageParts(opts)})
	return &Request{
		Method: http.MethodPost,
		URL:    f.baseURL + "/v1/chat/completions",
		Header: http.Header{
			"Authorization": []string{"Bearer " + ep.Credentials.APIKey},
			"Content-Type":  []string{"application/json"},
			"X-Adapter":     []string{"adapter-default"},
		},
		Body:             body,
		Stream:           opts != nil && opts.Stream,
		ProtectedHeaders: []string{"Authorization", "X-Adapter"},
	}, nil
}

func hasImageParts(opts *ChatOptions) bool { return HasImages(opts.Messages) }

func (f fakeChatAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*ChatResponse, error) {
	var raw struct {
		Content          string `json:"content"`
		ReasoningContent string `json:"reasoning_content"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return &ChatResponse{Content: raw.Content, ReasoningContent: raw.ReasoningContent}, nil
}

func (f fakeChatAdapter) TranslateStreamEvent(state *StreamBridgeState, chunk StreamChunk) (*StreamEvent, error) {
	return OpenAIStreamBridge{}.TranslateStreamEvent(state, chunk)
}

// registerFake swaps in a fresh Default registry with the fake adapter and
// restores the previous registry when the test ends.
func registerFake(t *testing.T, a Adapter) {
	t.Helper()
	old := Default
	r := &Registry{adapters: make(map[string]Adapter)}
	require.NoError(t, r.Register(a))
	Default = r
	t.Cleanup(func() { Default = old })
}

func testModelConfig(baseURL string) *ModelConfig {
	return &ModelConfig{
		Provider:    "fake",
		ModelID:     "m-1",
		ModelName:   "fake-model",
		BaseURL:     baseURL,
		Credentials: Credentials{APIKey: "sk-test", AppID: "app", AppSecret: "sec"},
		CustomHeaders: map[string]string{
			"X-Adapter":      "user-override", // protected → must NOT override
			"X-Custom":       "user-value",    // free → applied
			"Connection":     "close",         // transport → dropped
			"Content-Length": "999",           // transport → dropped
			"X-Sign":         "user-sig",      // not protected → allowed
		},
	}
}

func TestChatEmptyRegistryFailsExplicitly(t *testing.T) {
	old := Default
	Default = &Registry{}
	t.Cleanup(func() { Default = old })
	_, err := Chat(context.Background(), testModelConfig("http://example.invalid"), &ChatOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no model adapters registered")
}

func TestChatUnknownProviderFailsExplicitly(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})
	m := testModelConfig(srv.URL)
	m.Provider = "nope"
	_, err := Chat(context.Background(), m, &ChatOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider not registered")
}

func TestChatDispatchWithoutFacetReturnsUnsupportedType(t *testing.T) {
	// Empty registry: resolveAdapter fails before any facet assertion, so the
	// UnsupportedType path needs no server — the empty registry error covers
	// the no-panic requirement; facet-mismatch lock is covered in the
	// registry tests below.
	old := Default
	Default = &Registry{}
	t.Cleanup(func() { Default = old })
	m := testModelConfig("http://example.invalid")
	m.Provider = "ghost"
	_, err := Chat(context.Background(), m, &ChatOptions{})
	require.Error(t, err)
	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, ErrUnsupportedType, pe.Kind)
}

func TestChatEndToEndWithHeaderOverlay(t *testing.T) {
	allowLoopbackSSRF(t)
	var gotHeader http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"hello","reasoning_content":"thought"}`))
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	resp, err := Chat(context.Background(), testModelConfig(srv.URL), &ChatOptions{MaxCompletionTokens: 42})
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "thought", resp.ReasoningContent)

	// Adapter default headers + auth present.
	assert.Equal(t, []string{"Bearer sk-test"}, gotHeader.Values("Authorization"))
	// Protected header: user override ignored, adapter default kept.
	assert.Equal(t, []string{"adapter-default"}, gotHeader.Values("X-Adapter"))
	// Free header: user value applied.
	assert.Equal(t, []string{"user-value"}, gotHeader.Values("X-Custom"))
	// Transport-header dropping and the full overlay matrix are unit-tested
	// in TestApplyCustomHeaders (the HTTP transport manages these headers on
	// the wire regardless of the overlay).
	// Wire body carried the model name.
	assert.Contains(t, string(gotBody), "fake-model")
}

func TestApplyCustomHeaders(t *testing.T) {
	req := &Request{
		Header: http.Header{
			"Authorization": []string{"Bearer adapter"},
			"Content-Type":  []string{"application/json"},
			"X-Adapter":     []string{"adapter-default"},
		},
		ProtectedHeaders: []string{"Authorization", "X-Adapter"},
	}
	applyCustomHeaders(req, map[string]string{
		"Authorization":     "Bearer user",  // protected → ignored
		"x-adapter":         "user",         // protected (case-insensitive) → ignored
		"X-Custom":          "user-value",   // free → set
		"Content-Type":      "text/plain",   // not protected → overridable
		"Host":              "evil.example", // transport → dropped
		"Content-Length":    "1",            // transport → dropped
		"Connection":        "close",        // transport → dropped
		"Transfer-Encoding": "chunked",      // transport → dropped
	})
	assert.Equal(t, []string{"Bearer adapter"}, req.Header.Values("Authorization"))
	assert.Equal(t, []string{"adapter-default"}, req.Header.Values("X-Adapter"))
	assert.Equal(t, []string{"user-value"}, req.Header.Values("X-Custom"))
	assert.Equal(t, []string{"text/plain"}, req.Header.Values("Content-Type"))
	assert.Empty(t, req.Header.Values("Host"))
	assert.Empty(t, req.Header.Values("Content-Length"))
	assert.Empty(t, req.Header.Values("Connection"))
	assert.Empty(t, req.Header.Values("Transfer-Encoding"))
}

// Auth headers are ALWAYS protected at the entry (P2 review finding 3 — v1
// reservedHeaderKeys behavior), even when the adapter declared no
// ProtectedHeaders of its own: overriding Authorization / Api-Key /
// X-Api-Key / X-Goog-Api-Key would break vendor auth.
func TestApplyCustomHeadersAuthHeadersAlwaysProtected(t *testing.T) {
	req := &Request{
		Header: http.Header{
			"Authorization":  []string{"Bearer adapter"},
			"Api-Key":        []string{"adapter-az"},
			"X-Goog-Api-Key": []string{"adapter-g"},
		},
		// NOTE: no ProtectedHeaders — protection comes from isAuthHeader.
	}
	applyCustomHeaders(req, map[string]string{
		"Authorization":  "Bearer attacker",
		"api-key":        "attacker-az", // case-insensitive
		"x-goog-api-key": "attacker-g",
		"X-Custom":       "user-value", // free header still applies
	})
	assert.Equal(t, []string{"Bearer adapter"}, req.Header.Values("Authorization"))
	assert.Equal(t, []string{"adapter-az"}, req.Header.Values("Api-Key"))
	assert.Equal(t, []string{"adapter-g"}, req.Header.Values("X-Goog-Api-Key"))
	assert.Equal(t, []string{"user-value"}, req.Header.Values("X-Custom"))
}

func TestChatRetriesOn429ThenSucceeds(t *testing.T) {
	allowLoopbackSSRF(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"ok"}`))
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	resp, err := Chat(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.EqualValues(t, 2, calls.Load())
}

func TestChatErrorClassification(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`invalid api key`))
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	_, err := Chat(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, ErrAuth, pe.Kind)
	assert.Equal(t, http.StatusUnauthorized, pe.Status)
}

func TestSSRFRejectionCarriesWhitelistGuidance(t *testing.T) {
	// Loopback deliberately NOT whitelisted here: direct-IP targets are
	// blocked, and the rejection must carry the SSRF_WHITELIST guidance
	// (design §11 — local Ollama hosts need the same one-time whitelist
	// entry when upgrading local deployments).
	t.Setenv("SSRF_WHITELIST", "example.invalid")
	secutils.ResetSSRFWhitelistForTest()
	secutils.ResetSSRFOutboundValidationCacheForTest()
	t.Cleanup(func() {
		secutils.ResetSSRFWhitelistForTest()
		secutils.ResetSSRFOutboundValidationCacheForTest()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("request must never reach the server")
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	_, err := Chat(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF_WHITELIST", "rejection must carry the whitelist guidance")
}

func TestMultimodalDegradeRetryStripsImages(t *testing.T) {
	allowLoopbackSSRF(t)
	var payloads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		payloads = append(payloads, string(buf))
		if strings.Contains(string(buf), `"images":true`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"image input not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"done"}`))
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	m := testModelConfig(srv.URL)
	opts := &ChatOptions{Messages: []Message{{
		Role: "user",
		Content: []Part{
			{Text: "what is this"},
			{Image: &ImageRef{URL: "data:image/png;base64,AAAA", Detail: "low"}},
		},
	}}}
	resp, err := Chat(context.Background(), m, opts)
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	require.Len(t, payloads, 2)
	assert.Contains(t, payloads[0], `"images":true`)
	assert.Contains(t, payloads[1], `"images":false`)
	// Original opts untouched by the degradation retry.
	require.Len(t, opts.Messages[0].Content, 2)
}

func TestListModelsOptionalFacet(t *testing.T) {
	old := Default
	Default = &Registry{adapters: make(map[string]Adapter)}
	t.Cleanup(func() { Default = old })
	// The fake implements ChatAdapter only → List must refuse explicitly.
	// (Whitelisted host: must clear the entry's SSRF gate to reach the facet
	// check; the request never dials.)
	t.Setenv("SSRF_WHITELIST", "probe.example.com")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	require.NoError(t, Default.Register(fakeChatAdapter{}))
	_, err := List(context.Background(), "fake", &ListOptions{BaseURL: "https://probe.example.com"})
	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, ErrUnsupportedType, pe.Kind)
}

// mismatchAdapter implements only the chat facet but also declares an
// embedding shard — registration must reject the mismatch (three-lock #2,
// design §6.2).
type mismatchAdapter struct{ fakeChatAdapter }

func (m mismatchAdapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: &provider.ChatCaps{}, Embedding: &provider.EmbeddingCaps{}}
}

func TestRegistryRejectsFacetCapabilityMismatch(t *testing.T) {
	err := (&Registry{}).Register(mismatchAdapter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding facet")
}
