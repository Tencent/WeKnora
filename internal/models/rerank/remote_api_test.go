package rerank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newRerankScoreTestServer emulates an OpenAI-compatible /rerank endpoint the
// way a vLLM-backed provider (e.g. SiliconFlow) behaves, and records the last
// decoded request body.
//
// If the request carries truncate_prompt_tokens, the backend keeps only the
// last N tokens of the templated rerank prompt — the query gets cut off long
// documents and every relevance score collapses to near zero (issue #2143).
// Otherwise it returns the real scores, sorted by relevance_score descending,
// with index pointing back at the original input-document position.
func newRerankScoreTestServer(t *testing.T, lastRequest *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rerank request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*lastRequest = req

		w.Header().Set("Content-Type", "application/json")
		if _, truncated := req["truncate_prompt_tokens"]; truncated {
			// Query truncated away from the prompt: scores collapse.
			_, _ = w.Write([]byte(`{
				"id": "rerank-collapsed",
				"results": [
					{"index": 0, "relevance_score": 0.00670681, "document": {"text": "A"}},
					{"index": 1, "relevance_score": 0.00587342, "document": {"text": "B"}},
					{"index": 2, "relevance_score": 0.00412907, "document": {"text": "C"}}
				],
				"usage": {"total_tokens": 42}
			}`))
			return
		}
		// Healthy response: sorted by relevance_score descending, index values
		// out of order relative to the input documents [A, B, C].
		_, _ = w.Write([]byte(`{
			"id": "rerank-ok",
			"results": [
				{"index": 2, "relevance_score": 0.998, "document": {"text": "C"}},
				{"index": 0, "relevance_score": 0.51, "document": {"text": "A"}},
				{"index": 1, "relevance_score": 0.006, "document": {"text": "B"}}
			],
			"usage": {"total_tokens": 42}
		}`))
	}))
}

// TestOpenAIRerankerDoesNotTruncatePromptByDefault is the regression test for
// issue #2143: the generic OpenAI-compatible reranker must not send the
// vLLM-specific truncate_prompt_tokens field unless explicitly configured, and
// each input document must keep its own relevance score via the index field.
func TestOpenAIRerankerDoesNotTruncatePromptByDefault(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newRerankScoreTestServer(t, &lastRequest)
	defer server.Close()

	reranker, err := NewOpenAIReranker(&RerankerConfig{
		BaseURL:   server.URL,
		ModelName: "Qwen/Qwen3-VL-Reranker-8B",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIReranker: %v", err)
	}

	documents := []string{"A", "B", "C"}
	results, err := reranker.Rerank(t.Context(), "query", documents)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	if v, ok := lastRequest["truncate_prompt_tokens"]; ok {
		t.Errorf("request contains truncate_prompt_tokens=%v; it must not be sent unless configured", v)
	}
	if v, ok := lastRequest["additional_data"]; ok {
		t.Errorf("request contains additional_data=%v; empty optional fields must be omitted", v)
	}

	if len(results) != len(documents) {
		t.Fatalf("got %d results, want %d", len(results), len(documents))
	}

	// Results stay in the order returned by the API (sorted by score desc).
	if results[0].Index != 2 || results[0].RelevanceScore != 0.998 {
		t.Errorf("top result = {index: %d, score: %v}, want {index: 2, score: 0.998}",
			results[0].Index, results[0].RelevanceScore)
	}

	// Each input document keeps its own score, resolved through the index field.
	wantScoreByDoc := map[string]float64{"C": 0.998, "A": 0.51, "B": 0.006}
	for _, rr := range results {
		if rr.Index < 0 || rr.Index >= len(documents) {
			t.Fatalf("result index %d out of range for %d documents", rr.Index, len(documents))
		}
		doc := documents[rr.Index]
		if want := wantScoreByDoc[doc]; rr.RelevanceScore != want {
			t.Errorf("document %q got score %v, want %v", doc, rr.RelevanceScore, want)
		}
	}
}

// TestOpenAIRerankerTruncatePromptTokensOptIn verifies that deployments which
// really need server-side prompt truncation (self-hosted vLLM with small-
// context rerankers) can still opt in via extra_config.
func TestOpenAIRerankerTruncatePromptTokensOptIn(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newRerankScoreTestServer(t, &lastRequest)
	defer server.Close()

	reranker, err := NewOpenAIReranker(&RerankerConfig{
		BaseURL:     server.URL,
		ModelName:   "bge-reranker-base",
		APIKey:      "sk-test",
		ExtraConfig: map[string]string{"truncate_prompt_tokens": "511"},
	})
	if err != nil {
		t.Fatalf("NewOpenAIReranker: %v", err)
	}

	if _, err := reranker.Rerank(t.Context(), "query", []string{"A", "B", "C"}); err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	v, ok := lastRequest["truncate_prompt_tokens"]
	if !ok {
		t.Fatal("request is missing truncate_prompt_tokens despite extra_config opt-in")
	}
	if n, _ := v.(float64); n != 511 {
		t.Errorf("truncate_prompt_tokens = %v, want 511", v)
	}
}

func TestNewOpenAIRerankerRejectsInvalidTruncatePromptTokens(t *testing.T) {
	for _, raw := range []string{"abc", "-1", "0"} {
		_, err := NewOpenAIReranker(&RerankerConfig{
			ModelName:   "rerank-test",
			ExtraConfig: map[string]string{"truncate_prompt_tokens": raw},
		})
		if err == nil {
			t.Errorf("NewOpenAIReranker accepted invalid truncate_prompt_tokens %q", raw)
		}
	}
}

// TestOpenAIRerankerSupportsHuggingFaceTEIFormat verifies compatibility with
// Hugging Face text-embeddings-inference (TEI) and Infinity:
// 1. The outgoing request must include the `texts` field expected by TEI.
// 2. The parser must decode top-level array responses: [{"index": i, "score": s}].
func TestOpenAIRerankerSupportsHuggingFaceTEIFormat(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	var receivedRequest map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedRequest); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		// TEI returns a top-level JSON array directly with `score` rather than `relevance_score`
		_, _ = w.Write([]byte(`[
			{"index": 1, "score": 0.9876},
			{"index": 0, "score": 0.1234}
		]`))
	}))
	defer server.Close()

	reranker, err := NewOpenAIReranker(&RerankerConfig{
		BaseURL:   server.URL,
		ModelName: "BAAI/bge-reranker-v2-m3",
		APIKey:    "test-key",
	})
	if err != nil {
		t.Fatalf("NewOpenAIReranker: %v", err)
	}

	documents := []string{"Doc A", "Doc B"}
	results, err := reranker.Rerank(t.Context(), "test query", documents)
	if err != nil {
		t.Fatalf("Rerank failed on TEI-style endpoint: %v", err)
	}

	// 1. Verify `texts` field was sent and equals documents
	rawTexts, ok := receivedRequest["texts"]
	if !ok {
		t.Fatal("request is missing `texts` field required by Hugging Face TEI")
	}
	textsSlice, ok := rawTexts.([]interface{})
	if !ok || len(textsSlice) != len(documents) {
		t.Fatalf("texts = %v, want slice of length %d", rawTexts, len(documents))
	}
	for i, d := range documents {
		if textsSlice[i] != d {
			t.Errorf("texts[%d] = %v, want %v", i, textsSlice[i], d)
		}
	}

	// 2. Verify results parsed from top-level array
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Index != 1 || results[0].RelevanceScore != 0.9876 {
		t.Errorf("results[0] = {index: %d, score: %v}, want {index: 1, score: 0.9876}",
			results[0].Index, results[0].RelevanceScore)
	}
	if results[1].Index != 0 || results[1].RelevanceScore != 0.1234 {
		t.Errorf("results[1] = {index: %d, score: %v}, want {index: 0, score: 0.1234}",
			results[1].Index, results[1].RelevanceScore)
	}
}
