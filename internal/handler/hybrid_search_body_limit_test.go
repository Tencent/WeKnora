package handler

import (
	"net/http"
	"strings"
	"testing"
)

// The search body carries a query, filters and an optional precomputed
// `query_embedding`. HybridSearch is reachable by any Viewer of the knowledge
// base, so an uncapped read lets a low-privilege caller allocate an arbitrary
// body per request; the Logger middleware bounds only its own log line.
func TestHybridSearchRejectsAnOversizedBody(t *testing.T) {
	svc := &hybridSearchTestService{}
	body := `{"query_text":"` + strings.Repeat("a", hybridSearchJSONMaxBytes) + `"}`

	response := performHybridSearchRequest(svc, body)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized body, got %d body=%s", response.Code, response.Body.String())
	}
	if svc.searchCalls != 0 {
		t.Fatalf("an oversized body must not reach the search service, got %d calls", svc.searchCalls)
	}
	if !strings.Contains(response.Body.String(), "too large") {
		t.Fatalf("expected the size error, got body=%s", response.Body.String())
	}
}

// The cap must accept the widest realistic vector: an 8192-dimension float32
// array is roughly 160 KiB of JSON, well inside 1 MiB.
func TestHybridSearchAcceptsAWideVectorBody(t *testing.T) {
	embedding := make([]string, 8192)
	for i := range embedding {
		embedding[i] = "0.5"
	}
	body := `{"query_embedding":[` + strings.Join(embedding, ",") + `],"disable_keywords_match":true}`
	if len(body) >= hybridSearchJSONMaxBytes {
		t.Fatalf("test fixture is %d bytes, over the %d cap", len(body), hybridSearchJSONMaxBytes)
	}

	svc := &hybridSearchTestService{}
	response := performHybridSearchRequest(svc, body)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 for a wide vector, got %d body=%s", response.Code, response.Body.String())
	}
	if len(svc.searchParams.QueryEmbedding) != 8192 {
		t.Fatalf("embedding length = %d, want 8192", len(svc.searchParams.QueryEmbedding))
	}
}

// A malformed but small body must still report as invalid parameters, not as a
// size limit: the cap changes only what is rejected for size.
func TestHybridSearchKeepsTheParseErrorForMalformedBodies(t *testing.T) {
	svc := &hybridSearchTestService{}

	response := performHybridSearchRequest(svc, `{"query_text":`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "too large") {
		t.Fatalf("a syntax error must not be reported as a size limit, got %s", response.Body.String())
	}
}
