package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ============================================================================
// Test helpers
//
// newTestClient builds an *osapi.Client pointing at an httptest URL. No TLS,
// no auth — the test server is plain http:// on localhost. This helper lives
// in *_test.go so it can never bleed into production code (build tag).
// ============================================================================

func newTestClient(t *testing.T, url string) *osapi.Client {
	t.Helper()
	c, err := osapi.NewClient(osapi.Config{
		Client: opensearch.Config{
			Addresses: []string{url},
		},
	})
	if err != nil {
		t.Fatalf("newTestClient: %v", err)
	}
	return c
}

// newTestRepo builds a Repository directly (bypassing NewRepository's probe
// calls) for unit tests that exercise post-construction behavior. The
// caller supplies the http handler that simulates the cluster.
func newTestRepo(t *testing.T, handler http.HandlerFunc) (*Repository, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	client := newTestClient(t, ts.URL)
	repo := &Repository{
		client:    client,
		baseIndex: "weknora_test",
		cfg: internalCfg{
			shards: 1, replicas: 0,
			knnEngine:          "lucene",
			hnswM:              16,
			hnswEFConstruction: 100,
			efSearch:           100,
		},
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	return repo, ts
}

// ============================================================================
// Interface satisfaction (compile-time check — duplicates the var _ assertion
// in repository.go but documents the intent explicitly in tests).
// ============================================================================

func TestRepository_ImplementsRetrieveEngineRepository(t *testing.T) {
	t.Parallel()
	var _ interfaces.RetrieveEngineRepository = (*Repository)(nil)
}

func TestRepository_EngineType_ReturnsOpenSearch(t *testing.T) {
	t.Parallel()
	r := &Repository{}
	if got := r.EngineType(); got != types.OpenSearchRetrieverEngineType {
		t.Errorf("EngineType: want OpenSearch, got %v", got)
	}
}

func TestRepository_Support_KeywordsAndVector(t *testing.T) {
	t.Parallel()
	r := &Repository{}
	got := r.Support()
	want := map[types.RetrieverType]bool{
		types.KeywordsRetrieverType: true,
		types.VectorRetrieverType:   true,
	}
	if len(got) != len(want) {
		t.Fatalf("Support: want %d types, got %d (%v)", len(want), len(got), got)
	}
	for _, rt := range got {
		if !want[rt] {
			t.Errorf("Support returned unexpected %v", rt)
		}
	}
}

// ============================================================================
// Sentinel errors — errors.Is chain compatibility
// ============================================================================

func TestSentinelErrors_AreErrorsIsCompatible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sentinel error
	}{
		{"ErrIndexNotFound", ErrIndexNotFound},
		{"ErrDimensionMismatch", ErrDimensionMismatch},
		{"ErrAuth", ErrAuth},
		{"ErrTransport", ErrTransport},
		{"ErrVersionUnsupported", ErrVersionUnsupported},
		{"ErrConfigInvalid", ErrConfigInvalid},
		{"ErrFeatureNotEnabled", ErrFeatureNotEnabled},
		{"ErrBatchTooLarge", ErrBatchTooLarge},
		{"ErrCircuitBreaker", ErrCircuitBreaker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrap: %w", tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(%s wrapped) returned false", tc.name)
			}
		})
	}
}

func TestIsTransientErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
	}{
		{ErrTransport, true},
		{ErrCircuitBreaker, true},
		{fmt.Errorf("wrap: %w", ErrTransport), true},
		{ErrAuth, false},
		{ErrConfigInvalid, false},
		{ErrDimensionMismatch, false},
		{nil, false},
	}
	for _, tc := range cases {
		got := isTransientErr(tc.err)
		if got != tc.want {
			t.Errorf("isTransientErr(%v): want %v, got %v", tc.err, tc.want, got)
		}
	}
}

// ============================================================================
// sanitizeIndexName — strict OS-compatible name spec
// ============================================================================

func TestSanitizeIndexName_AcceptsValidNames(t *testing.T) {
	t.Parallel()
	cases := []string{
		"weknora",
		"weknora_abc123def456_768",
		"a",
		"1abc",
		"foo-bar_baz",
	}
	for _, name := range cases {
		got, err := sanitizeIndexName(name)
		if err != nil {
			t.Errorf("%q: want OK, got %v", name, err)
		}
		if got != name {
			t.Errorf("%q: must be returned unchanged, got %q", name, got)
		}
	}
}

func TestSanitizeIndexName_RejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		reason string
	}{
		{"", "empty"},
		{"Foo", "uppercase"},
		{"foo*", "wildcard"},
		{"foo?bar", "wildcard"},
		{"foo,bar", "comma"},
		{"foo\nbar", "newline"},
		{"foo\rbar", "carriage return"},
		{"foo\tbar", "tab"},
		{"../foo", "path traversal"},
		{"/foo", "leading slash"},
		{"foo/bar", "embedded slash"},
		{"_foo", "leading underscore"},
		{"-foo", "leading hyphen"},
		{"+foo", "leading plus"},
		{".foo", "leading dot"},
		{"foo.bar", "embedded dot"}, // regex rejects '.'
		{strings.Repeat("a", 256), "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			_, err := sanitizeIndexName(tc.name)
			if !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("%q (%s): want ErrConfigInvalid, got %v", tc.name, tc.reason, err)
			}
		})
	}
}

// ============================================================================
// parseMajorMinor — robust semver parsing
// ============================================================================

func TestParseMajorMinor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        string
		wantMaj   int
		wantMinor int
	}{
		{"3.3.2", 3, 3},
		{"2.10.0", 2, 10},
		{"2.10", 2, 10}, // missing patch — must still parse
		{"3.0.0-rc1", 3, 0},
		{"2.10.0-SNAPSHOT", 2, 10},
		{"1.3.18", 1, 3},
		{"abc", 0, 0}, // unparseable
		{"3", 0, 0},   // missing minor
		{"", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			maj, min := parseMajorMinor(tc.in)
			if maj != tc.wantMaj || min != tc.wantMinor {
				t.Errorf("parseMajorMinor(%q): want (%d, %d), got (%d, %d)",
					tc.in, tc.wantMaj, tc.wantMinor, maj, min)
			}
		})
	}
}

// ============================================================================
// buildIndexMapping — pure func, JSON shape pin
// ============================================================================

func TestBuildIndexMapping_LuceneEngine_768Dim(t *testing.T) {
	t.Parallel()
	cfg := internalCfg{
		shards: 4, replicas: 1, knnEngine: "lucene",
		hnswM: 16, hnswEFConstruction: 100, efSearch: 100,
	}
	body, err := buildIndexMapping(cfg, 768)
	if err != nil {
		t.Fatalf("buildIndexMapping: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	settings := parsed["settings"].(map[string]any)["index"].(map[string]any)
	if settings["knn"] != true {
		t.Errorf("knn should be true, got %v", settings["knn"])
	}
	if settings["number_of_shards"].(float64) != 4 {
		t.Errorf("number_of_shards: want 4, got %v", settings["number_of_shards"])
	}
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)

	// match_type must NOT be in the mapping — derived from RetrieverType at parse time.
	if _, ok := props["match_type"]; ok {
		t.Error("match_type must NOT be in mapping (derived from RetrieverType)")
	}
	// source_type must be `integer`, not `keyword`.
	if st := props["source_type"].(map[string]any); st["type"] != "integer" {
		t.Errorf("source_type: want type=integer, got %v", st["type"])
	}
	// embedding field structural check.
	emb := props["embedding"].(map[string]any)
	if emb["type"] != "knn_vector" {
		t.Errorf("embedding.type: want knn_vector, got %v", emb["type"])
	}
	if emb["dimension"].(float64) != 768 {
		t.Errorf("embedding.dimension: want 768, got %v", emb["dimension"])
	}
	method := emb["method"].(map[string]any)
	if method["space_type"] != "cosinesimil" || method["engine"] != "lucene" {
		t.Errorf("method: space_type/engine mismatch: %v", method)
	}
	params := method["parameters"].(map[string]any)
	if params["m"].(float64) != 16 || params["ef_construction"].(float64) != 100 {
		t.Errorf("HNSW params mismatch: %v", params)
	}

	// All *_id fields must be keyword.
	for _, f := range []string{"chunk_id", "knowledge_id", "knowledge_base_id", "tag_id", "source_id"} {
		if props[f].(map[string]any)["type"] != "keyword" {
			t.Errorf("%s: want keyword, got %v", f, props[f])
		}
	}
}

func TestBuildIndexMapping_FaissEngine_CustomParams(t *testing.T) {
	t.Parallel()
	cfg := internalCfg{
		shards: 8, replicas: 2, knnEngine: "faiss",
		hnswM: 32, hnswEFConstruction: 512, efSearch: 200,
	}
	body, err := buildIndexMapping(cfg, 1536)
	if err != nil {
		t.Fatalf("buildIndexMapping: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	emb := parsed["mappings"].(map[string]any)["properties"].(map[string]any)["embedding"].(map[string]any)
	method := emb["method"].(map[string]any)
	if method["engine"] != "faiss" {
		t.Errorf("engine: want faiss, got %v", method["engine"])
	}
	if emb["dimension"].(float64) != 1536 {
		t.Errorf("dimension: want 1536, got %v", emb["dimension"])
	}
}

func TestBuildKeywordsMapping_OmitsEmbeddingField(t *testing.T) {
	t.Parallel()
	cfg := internalCfg{shards: 1, replicas: 0}
	body, err := buildKeywordsMapping(cfg)
	if err != nil {
		t.Fatalf("buildKeywordsMapping: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["embedding"]; ok {
		t.Error("keywords mapping must NOT include embedding field")
	}
	if _, ok := props["content"]; !ok {
		t.Error("keywords mapping must include content field for BM25")
	}
}

// ============================================================================
// retrieveFilters — typed filter struct closes JSON injection surface
// ============================================================================

func TestRetrieveFilters_RejectsJSONFragmentInjection(t *testing.T) {
	t.Parallel()
	// Hostile caller embeds JSON fragments in chunk IDs. The driver must
	// treat them as opaque strings (JSON-injection regression guard).
	f := &retrieveFilters{
		ExcludeChunkIDs: []string{`{"$or":[{"_id":"x"}]}`, `*`, `"; DROP TABLE chunks; --`},
	}
	body, err := buildKNNQuery([]float32{0.1, 0.2}, 5, 0, f)
	if err != nil {
		t.Fatalf("buildKNNQuery: %v", err)
	}
	// The hostile strings must appear as JSON-escaped string literals
	// inside the terms array, not as parsed DSL.
	asStr := string(body)
	if !strings.Contains(asStr, `"{\"$or\":[{\"_id\":\"x\"}]}"`) {
		t.Errorf("hostile chunk_id must be JSON-escaped string literal in body: %s", asStr)
	}
}

func TestRetrieveFilters_AppliesExcludeKnowledgeIDs(t *testing.T) {
	t.Parallel()
	f := &retrieveFilters{ExcludeKnowledgeIDs: []string{"k1", "k2"}}
	body, err := buildKNNQuery([]float32{0.1}, 5, 0, f)
	if err != nil {
		t.Fatalf("buildKNNQuery: %v", err)
	}
	if !strings.Contains(string(body), `"knowledge_id"`) {
		t.Errorf("must_not.terms.knowledge_id missing from query body: %s", body)
	}
}

func TestRetrieveFilters_IncludeDisabled_SkipsIsEnabledClause(t *testing.T) {
	t.Parallel()
	f := &retrieveFilters{IncludeDisabled: true}
	clauses := f.toBoolMust()
	for _, c := range clauses {
		if term, ok := c["term"].(map[string]any); ok {
			if _, hasEnabled := term["is_enabled"]; hasEnabled {
				t.Error("IncludeDisabled=true must skip is_enabled clause")
			}
		}
	}
}

func TestRetrieveFilters_DefaultPinsIsEnabledTrue(t *testing.T) {
	t.Parallel()
	f := &retrieveFilters{}
	clauses := f.toBoolMust()
	found := false
	for _, c := range clauses {
		if term, ok := c["term"].(map[string]any); ok {
			if v, hasEnabled := term["is_enabled"]; hasEnabled {
				if v != true {
					t.Errorf("is_enabled term: want true, got %v", v)
				}
				found = true
			}
		}
	}
	if !found {
		t.Error("default filter must pin is_enabled=true")
	}
}

// ============================================================================
// buildKNNQuery / buildKeywordQuery — min_score from threshold
// ============================================================================

func TestBuildKNNQuery_AppliesMinScoreWhenThreshold(t *testing.T) {
	t.Parallel()
	body, err := buildKNNQuery([]float32{0.1}, 5, 0.75, &retrieveFilters{})
	if err != nil {
		t.Fatalf("buildKNNQuery: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	if ms, ok := parsed["min_score"]; !ok || ms.(float64) != 0.75 {
		t.Errorf("min_score: want 0.75, got %v (present=%v)", ms, ok)
	}
}

func TestBuildKNNQuery_OmitsMinScoreWhenThresholdZero(t *testing.T) {
	t.Parallel()
	body, err := buildKNNQuery([]float32{0.1}, 5, 0, &retrieveFilters{})
	if err != nil {
		t.Fatalf("buildKNNQuery: %v", err)
	}
	if strings.Contains(string(body), "min_score") {
		t.Errorf("min_score must be omitted when threshold=0: %s", body)
	}
}

func TestBuildKeywordQuery_AppendsContentMatch(t *testing.T) {
	t.Parallel()
	body, err := buildKeywordQuery("hello world", 5, 0, &retrieveFilters{})
	if err != nil {
		t.Fatalf("buildKeywordQuery: %v", err)
	}
	if !strings.Contains(string(body), `"content":"hello world"`) {
		t.Errorf("content match missing: %s", body)
	}
}

// ============================================================================
// toDoc — keyword-only path omits embedding field; source_type is integer
// ============================================================================

func TestToDoc_OmitsEmbeddingField_WhenEmpty(t *testing.T) {
	t.Parallel()
	info := &types.IndexInfo{ChunkID: "c1", Content: "x"}
	doc := toDoc(info, nil, true)
	if _, ok := doc["embedding"]; ok {
		t.Error("toDoc must omit embedding when emb is nil")
	}
	doc2 := toDoc(info, []float32{}, true)
	if _, ok := doc2["embedding"]; ok {
		t.Error("toDoc must omit embedding when emb is empty slice")
	}
}

func TestToDoc_IncludesEmbedding_WhenPresent(t *testing.T) {
	t.Parallel()
	info := &types.IndexInfo{ChunkID: "c1"}
	doc := toDoc(info, []float32{0.1, 0.2, 0.3}, true)
	if emb, ok := doc["embedding"]; !ok {
		t.Error("toDoc must include embedding when emb is non-empty")
	} else if len(emb.([]float32)) != 3 {
		t.Errorf("embedding length: want 3, got %d", len(emb.([]float32)))
	}
}

func TestToDoc_SourceTypeIsInteger(t *testing.T) {
	t.Parallel()
	info := &types.IndexInfo{
		ChunkID:    "c1",
		SourceType: types.PassageSourceType, // iota=1
	}
	doc := toDoc(info, []float32{0.1}, true)
	if st, ok := doc["source_type"].(int); !ok {
		t.Errorf("source_type: want int, got %T", doc["source_type"])
	} else if st != 1 {
		t.Errorf("source_type: want 1 (PassageSourceType), got %d", st)
	}
}

func TestToDoc_PreservesIsRecommended(t *testing.T) {
	t.Parallel()
	for _, want := range []bool{true, false} {
		info := &types.IndexInfo{ChunkID: "c1", IsRecommended: want}
		doc := toDoc(info, []float32{0.1}, true)
		got, ok := doc["is_recommended"].(bool)
		if !ok {
			t.Errorf("IsRecommended=%v: doc missing is_recommended field", want)
			continue
		}
		if got != want {
			t.Errorf("IsRecommended=%v: doc[is_recommended]=%v", want, got)
		}
	}
}

// ============================================================================
// lookupEmbedding / lookupChunkEnabled — additionalParams keying contract
// (embedding keyed by SourceID, chunk_enabled keyed by ChunkID)
// ============================================================================

func TestLookupEmbedding_KeyedBySourceID(t *testing.T) {
	t.Parallel()
	params := map[string]any{
		"embedding": map[string][]float32{
			"src-A": {1, 2, 3},
			"src-B": {4, 5, 6},
		},
	}
	if got := lookupEmbedding(params, "src-A"); len(got) != 3 || got[0] != 1 {
		t.Errorf("lookup src-A: want [1 2 3], got %v", got)
	}
	if got := lookupEmbedding(params, "src-B"); got[2] != 6 {
		t.Errorf("lookup src-B: want [4 5 6], got %v", got)
	}
	if got := lookupEmbedding(params, "nonexistent"); got != nil {
		t.Errorf("missing source: want nil, got %v", got)
	}
}

func TestLookupEmbedding_NilParams_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := lookupEmbedding(nil, "any"); got != nil {
		t.Errorf("nil params: want nil, got %v", got)
	}
}

func TestLookupEmbedding_WrongType_DegradesToNil(t *testing.T) {
	t.Parallel()
	// If the service layer sends the wrong shape, the driver must degrade
	// gracefully (keyword-only path) rather than panic. This is the
	// defense-in-depth from crud.go's documented contract.
	params := map[string]any{"embedding": "not a map"}
	if got := lookupEmbedding(params, "any"); got != nil {
		t.Errorf("wrong type: want nil (degrade), got %v", got)
	}
}

func TestLookupChunkEnabled_OverrideAndDefault(t *testing.T) {
	t.Parallel()
	params := map[string]any{
		"chunk_enabled": map[string]bool{
			"c1": true,
			"c2": false,
		},
	}
	if !lookupChunkEnabled(params, "c1", false) {
		t.Error("c1 overlay should be true")
	}
	if lookupChunkEnabled(params, "c2", true) {
		t.Error("c2 overlay should be false (overrides default)")
	}
	if !lookupChunkEnabled(params, "missing", true) {
		t.Error("missing key must fall back to default=true")
	}
	if lookupChunkEnabled(nil, "any", false) {
		t.Error("nil params must fall back to default=false")
	}
}

// ============================================================================
// extractBatchEmbeddings — mixed-dim rejection
// ============================================================================

func TestExtractBatchEmbeddings_MixedDims_Rejected(t *testing.T) {
	t.Parallel()
	infos := []*types.IndexInfo{
		{SourceID: "a"}, {SourceID: "b"},
	}
	params := map[string]any{
		"embedding": map[string][]float32{
			"a": {1, 2, 3, 4}, // dim 4
			"b": {5, 6, 7},    // dim 3 — mismatch
		},
	}
	_, _, err := extractBatchEmbeddings(params, infos)
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Errorf("mixed dims: want ErrDimensionMismatch, got %v", err)
	}
}

func TestExtractBatchEmbeddings_AllEmpty_DimZero(t *testing.T) {
	t.Parallel()
	infos := []*types.IndexInfo{
		{SourceID: "a"}, {SourceID: "b"},
	}
	embs, dim, err := extractBatchEmbeddings(nil, infos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dim != 0 {
		t.Errorf("dim: want 0 (keyword-only), got %d", dim)
	}
	if len(embs) != 2 {
		t.Errorf("embs length: want 2 (parallel to infos), got %d", len(embs))
	}
}

// ============================================================================
// estimateBulkBodyBytes
// ============================================================================

func TestEstimateBulkBodyBytes(t *testing.T) {
	t.Parallel()
	// 1000 docs at 768-dim should fit under 10MB; 1000 at 1536-dim should NOT.
	if got := estimateBulkBodyBytes(1000, 768); got > 10*1024*1024 {
		t.Errorf("1000@768 estimate %dB should be under 10MB cap", got)
	}
	if got := estimateBulkBodyBytes(2000, 1536); got <= 10*1024*1024 {
		t.Errorf("2000@1536 estimate %dB should exceed 10MB cap (got under)", got)
	}
}

// ============================================================================
// effectiveTopK
// ============================================================================

func TestEffectiveTopK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		in   int
		want int
	}{
		{0, 10},  // default
		{-5, 10}, // negative falls back to default
		{5, 5},   // normal
		{10000, 10000},
		{50000, 10000}, // cap
	}
	for _, tc := range cases {
		got := effectiveTopK(ctx, types.RetrieveParams{TopK: tc.in})
		if got != tc.want {
			t.Errorf("effectiveTopK(%d): want %d, got %d", tc.in, tc.want, got)
		}
	}
}

// ============================================================================
// resolveDim — dim resolution priority + cross-dim fallback
// ============================================================================

func TestResolveDim_AdditionalParamsTakesPrecedence(t *testing.T) {
	t.Parallel()
	p := types.RetrieveParams{
		Embedding:        []float32{1, 2, 3, 4}, // would suggest dim=4
		AdditionalParams: map[string]any{"dim": 768},
	}
	dim, multi := resolveDim(p)
	if dim != 768 || multi {
		t.Errorf("resolveDim: want (768, false), got (%d, %v)", dim, multi)
	}
}

func TestResolveDim_EmbeddingFallback(t *testing.T) {
	t.Parallel()
	p := types.RetrieveParams{Embedding: []float32{1, 2, 3}}
	dim, multi := resolveDim(p)
	if dim != 3 || multi {
		t.Errorf("resolveDim: want (3, false), got (%d, %v)", dim, multi)
	}
}

func TestResolveDim_NoneTriggersMultiIndex(t *testing.T) {
	t.Parallel()
	p := types.RetrieveParams{}
	dim, multi := resolveDim(p)
	if dim != 0 || !multi {
		t.Errorf("resolveDim: want (0, true), got (%d, %v)", dim, multi)
	}
}

// ============================================================================
// inspectBulkResponse — Reason leak guard + total count
// ============================================================================

func TestInspectBulkResponse_AllSucceeded_NoError(t *testing.T) {
	t.Parallel()
	body := `{"errors": false, "items": [{"index": {"_id": "c1", "status": 201}}]}`
	if err := inspectBulkResponse(strings.NewReader(body)); err != nil {
		t.Errorf("all-succeeded bulk: want nil, got %v", err)
	}
}

func TestInspectBulkResponse_TotalCount(t *testing.T) {
	t.Parallel()
	// 7 items, 3 failed. Public error must surface count=3 + up to 5
	// distinct messages, but never expose the per-item Reason field.
	body := `{
		"errors": true,
		"items": [
			{"index": {"_id": "c1", "status": 201}},
			{"index": {"_id": "c2", "status": 400, "error": {"type": "mapper_parsing_exception", "reason": "secret-content-snippet"}}},
			{"index": {"_id": "c3", "status": 400, "error": {"type": "mapper_parsing_exception", "reason": "another-secret"}}},
			{"index": {"_id": "c4", "status": 201}},
			{"index": {"_id": "c5", "status": 400, "error": {"type": "version_conflict_engine_exception", "reason": "another-secret"}}},
			{"index": {"_id": "c6", "status": 201}},
			{"index": {"_id": "c7", "status": 201}}
		]
	}`
	err := inspectBulkResponse(strings.NewReader(body))
	if err == nil {
		t.Fatal("partial failure must return non-nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3 items failed") {
		t.Errorf("error must report total count of failures: %s", msg)
	}
	// Leak guard: per-item Reason must NOT leak to public error.
	if strings.Contains(msg, "secret-content-snippet") || strings.Contains(msg, "another-secret") {
		t.Errorf("error must NOT leak cluster-side Reason: %s", msg)
	}
	// Type (bounded enum) is OK to include.
	if !strings.Contains(msg, "mapper_parsing_exception") {
		t.Errorf("error should surface error.type for diagnosis: %s", msg)
	}
}

// ============================================================================
// probeVersion — supported-version acceptance/rejection matrix
// ============================================================================

func versionHandler(distribution, number string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":{"distribution":%q,"number":%q}}`, distribution, number)
	}
}

func TestProbeVersion_AcceptsOpenSearch3x(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(versionHandler("opensearch", "3.3.2"))
	defer ts.Close()
	if err := probeVersion(context.Background(), newTestClient(t, ts.URL)); err != nil {
		t.Errorf("OS 3.3.2: want nil, got %v", err)
	}
}

func TestProbeVersion_AcceptsOpenSearch211(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(versionHandler("opensearch", "2.11.0"))
	defer ts.Close()
	if err := probeVersion(context.Background(), newTestClient(t, ts.URL)); err != nil {
		t.Errorf("OS 2.11: want nil, got %v", err)
	}
}

func TestProbeVersion_WarnsButAcceptsOS25(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(versionHandler("opensearch", "2.5.0"))
	defer ts.Close()
	if err := probeVersion(context.Background(), newTestClient(t, ts.URL)); err != nil {
		t.Errorf("OS 2.5 (warn-but-accept): want nil, got %v", err)
	}
}

func TestProbeVersion_RejectsOpenSearch1x(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(versionHandler("opensearch", "1.3.18"))
	defer ts.Close()
	err := probeVersion(context.Background(), newTestClient(t, ts.URL))
	if !errors.Is(err, ErrVersionUnsupported) {
		t.Errorf("OS 1.x: want ErrVersionUnsupported, got %v", err)
	}
}

func TestProbeVersion_RejectsOpenSearch22(t *testing.T) {
	t.Parallel()
	// OS 2.0~2.3 are pre-Lucene-HNSW-GA.
	ts := httptest.NewServer(versionHandler("opensearch", "2.2.0"))
	defer ts.Close()
	err := probeVersion(context.Background(), newTestClient(t, ts.URL))
	if !errors.Is(err, ErrVersionUnsupported) {
		t.Errorf("OS 2.2: want ErrVersionUnsupported, got %v", err)
	}
}

func TestProbeVersion_RejectsElasticsearch(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(versionHandler("elasticsearch", "8.10.4"))
	defer ts.Close()
	err := probeVersion(context.Background(), newTestClient(t, ts.URL))
	if !errors.Is(err, ErrVersionUnsupported) {
		t.Errorf("ES 8.10: want ErrVersionUnsupported, got %v", err)
	}
}

func TestProbeVersion_HandlesPreReleaseSuffix(t *testing.T) {
	t.Parallel()
	// 3.0.0-rc1 should be accepted (rc of a supported major).
	ts := httptest.NewServer(versionHandler("opensearch", "3.0.0-rc1"))
	defer ts.Close()
	if err := probeVersion(context.Background(), newTestClient(t, ts.URL)); err != nil {
		t.Errorf("OS 3.0.0-rc1: want nil, got %v", err)
	}
}

// ============================================================================
// probeKNNPlugin — multi-node coverage
// ============================================================================

func pluginsHandler(rows []osapi.CatPluginResp) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	}
}

func TestProbeKNNPlugin_AllNodesPresent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(pluginsHandler([]osapi.CatPluginResp{
		{Name: "node-1", Component: "opensearch-knn"},
		{Name: "node-1", Component: "opensearch-sql"},
		{Name: "node-2", Component: "opensearch-knn"},
		{Name: "node-2", Component: "opensearch-sql"},
	}))
	defer ts.Close()
	if err := probeKNNPlugin(context.Background(), newTestClient(t, ts.URL)); err != nil {
		t.Errorf("all nodes have plugin: want nil, got %v", err)
	}
}

func TestProbeKNNPlugin_OneNodeMissing(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(pluginsHandler([]osapi.CatPluginResp{
		{Name: "node-1", Component: "opensearch-knn"},
		{Name: "node-2", Component: "opensearch-sql"}, // missing knn
		{Name: "node-3", Component: "opensearch-knn"},
	}))
	defer ts.Close()
	err := probeKNNPlugin(context.Background(), newTestClient(t, ts.URL))
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("missing on one node: want ErrConfigInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "node-2") {
		t.Errorf("error should name the missing node: %v", err)
	}
}

func TestProbeKNNPlugin_EmptyResponse(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(pluginsHandler([]osapi.CatPluginResp{}))
	defer ts.Close()
	err := probeKNNPlugin(context.Background(), newTestClient(t, ts.URL))
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("empty response: want ErrConfigInvalid, got %v", err)
	}
}

// ============================================================================
// ensureReady — concurrency + transient vs permanent error caching
// ============================================================================

func TestEnsureReady_ZeroDim_Rejected(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	err := r.ensureReady(context.Background(), 0)
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Errorf("dim=0: want ErrDimensionMismatch, got %v", err)
	}
}

func TestEnsureReady_DimAboveLimit_Rejected(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	err := r.ensureReady(context.Background(), 16001)
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Errorf("dim>16000: want ErrDimensionMismatch, got %v", err)
	}
}

func TestEnsureReady_NilCtx_Rejected(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	err := r.ensureReady(nil, 768) //nolint:staticcheck — explicit nil ctx test
	if err == nil || !strings.Contains(err.Error(), "non-nil ctx") {
		t.Errorf("nil ctx: want error about non-nil ctx, got %v", err)
	}
}

// indexLifecycleHandler simulates a cluster where:
//   - HEAD /_alias/<name> returns 404 (alias does not exist).
//   - PUT /<index> returns 200 (create succeeds).
//   - POST /_aliases-style PUT alias returns 200.
//   - All other paths return 200 with empty JSON.
//
// Tracks PUT-index invocations via the put counter.
type indexLifecycleHandler struct {
	puts atomic.Int32
}

func (h *indexLifecycleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Distinguish index-create PUT (path ends with _v1) from alias-put
	// PUT (path contains /_alias/). Both PUTs hit URLs containing _v1
	// because the alias-put path is /<index>/_alias/<alias>.
	switch {
	case r.Method == http.MethodHead:
		// All HEAD requests (alias exists checks) return 404 so
		// createIndexAndAlias proceeds to PUT.
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/_alias/"):
		// Alias PUT — acknowledge, do NOT count.
		_, _ = w.Write([]byte(`{"acknowledged": true}`))
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "_v1"):
		// Index PUT — count it. Path is exactly /<base>_<dim>_v1.
		h.puts.Add(1)
		_, _ = w.Write([]byte(`{"acknowledged": true}`))
	case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
		_, _ = w.Write([]byte(`{"acknowledged": true}`))
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}
}

func TestEnsureReady_ConcurrentCallers_SingleCreate(t *testing.T) {
	t.Parallel()
	h := &indexLifecycleHandler{}
	ts := httptest.NewServer(h)
	defer ts.Close()

	repo := &Repository{
		client:    newTestClient(t, ts.URL),
		baseIndex: "weknora_test",
		cfg:       internalCfg{shards: 1, replicas: 0, knnEngine: "lucene", hnswM: 16, hnswEFConstruction: 100, efSearch: 100},
		once:      make(map[int]*sync.Once),
		initErr:   make(map[int]error),
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = repo.ensureReady(context.Background(), 768)
		}()
	}
	wg.Wait()

	if got := h.puts.Load(); got != 1 {
		t.Errorf("100 concurrent ensureReady(768): want 1 PUT, got %d", got)
	}
}

func TestEnsureReady_PerDimensionIsolation(t *testing.T) {
	t.Parallel()
	h := &indexLifecycleHandler{}
	ts := httptest.NewServer(h)
	defer ts.Close()

	repo := &Repository{
		client:    newTestClient(t, ts.URL),
		baseIndex: "weknora_test",
		cfg:       internalCfg{shards: 1, replicas: 0, knnEngine: "lucene", hnswM: 16, hnswEFConstruction: 100, efSearch: 100},
		once:      make(map[int]*sync.Once),
		initErr:   make(map[int]error),
	}

	for _, dim := range []int{768, 1024, 1536} {
		if err := repo.ensureReady(context.Background(), dim); err != nil {
			t.Errorf("ensureReady(dim=%d): %v", dim, err)
		}
	}
	if got := h.puts.Load(); got != 3 {
		t.Errorf("3 distinct dims: want 3 PUTs, got %d", got)
	}
}

// errorHandler returns a configurable status code for index PUTs to
// simulate transient (5xx) vs permanent (400) failures.
type errorHandler struct {
	statusForIndexPut  int
	indexPutsAttempted atomic.Int32
}

func (h *errorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodHead:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/_alias/"):
		// Alias PUT — do not count, just acknowledge (though this path
		// only triggers if index create succeeds, which it doesn't in
		// the error handler's case).
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "_v1"):
		h.indexPutsAttempted.Add(1)
		w.WriteHeader(h.statusForIndexPut)
		// Write a minimal OS error response so wrapTransport can parse it.
		errBody := `{"error":{"type":"server_error","reason":"simulated"},"status":` + fmt.Sprintf("%d", h.statusForIndexPut) + `}`
		_, _ = w.Write([]byte(errBody))
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}
}

func TestEnsureReady_TransientError_NotCached(t *testing.T) {
	t.Parallel()
	h := &errorHandler{statusForIndexPut: http.StatusInternalServerError}
	ts := httptest.NewServer(h)
	defer ts.Close()

	repo := &Repository{
		client:    newTestClient(t, ts.URL),
		baseIndex: "weknora_test",
		cfg:       internalCfg{shards: 1, replicas: 0, knnEngine: "lucene", hnswM: 16, hnswEFConstruction: 100, efSearch: 100},
		once:      make(map[int]*sync.Once),
		initErr:   make(map[int]error),
	}

	// First call fails with transient 5xx.
	_ = repo.ensureReady(context.Background(), 768)
	first := h.indexPutsAttempted.Load()

	// Second call MUST retry — transient errors are not persisted.
	_ = repo.ensureReady(context.Background(), 768)
	second := h.indexPutsAttempted.Load()

	if second <= first {
		t.Errorf("transient error must allow retry: first=%d, second=%d", first, second)
	}
}

// ============================================================================
// indexAlias / keywordsIndex naming
// ============================================================================

func TestIndexAlias(t *testing.T) {
	t.Parallel()
	r := &Repository{baseIndex: "weknora_abc123def456"}
	if got := r.indexAlias(768); got != "weknora_abc123def456_768" {
		t.Errorf("indexAlias(768): want weknora_abc123def456_768, got %s", got)
	}
}

func TestKeywordsIndex(t *testing.T) {
	t.Parallel()
	r := &Repository{baseIndex: "weknora_abc123def456"}
	if got := r.keywordsIndex(); got != "weknora_abc123def456_keywords" {
		t.Errorf("keywordsIndex: want weknora_abc123def456_keywords, got %s", got)
	}
}

// ============================================================================
// NewRepository storeID validation
// ============================================================================

// minimalCluster handles enough endpoints for NewRepository's probes to
// succeed when we want to test the constructor's pre-flight validation
// (which runs BEFORE the probes).
func minimalCluster(t *testing.T) (*httptest.Server, *osapi.Client) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`{"version":{"distribution":"opensearch","number":"3.3.2"}}`))
		case "/_cat/plugins":
			_, _ = w.Write([]byte(`[{"name":"node-1","component":"opensearch-knn"}]`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	return ts, newTestClient(t, ts.URL)
}

func TestNewRepository_RejectsShortStoreID(t *testing.T) {
	t.Parallel()
	ts, client := minimalCluster(t)
	defer ts.Close()
	cases := []string{"a", "abc", "1234567890abcde"} // 1, 3, 15 chars
	for _, sid := range cases {
		_, err := NewRepository(context.Background(), client, sid, nil)
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("storeID=%q (len=%d): want ErrConfigInvalid, got %v", sid, len(sid), err)
		}
	}
}

func TestNewRepository_AcceptsEmptyStoreID_EnvPath(t *testing.T) {
	t.Parallel()
	ts, client := minimalCluster(t)
	defer ts.Close()
	_, err := NewRepository(context.Background(), client, "", nil)
	if err != nil {
		t.Errorf("empty storeID (env-path): want nil, got %v", err)
	}
}

func TestNewRepository_AcceptsLongStoreID(t *testing.T) {
	t.Parallel()
	ts, client := minimalCluster(t)
	defer ts.Close()
	// 32-char UUID-like string (without hyphens).
	storeID := "abc123def4567890abcdef0123456789"
	_, err := NewRepository(context.Background(), client, storeID, nil)
	if err != nil {
		t.Errorf("32-char storeID: want nil, got %v", err)
	}
}

// ============================================================================
// Stub coverage — remaining stubs return the not-enabled sentinel
//
// CopyIndices / BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID are now
// implemented (see copy_bulk_test.go for their behavioral tests); their stub
// assertions were removed. EstimateStorageSize keeps its conservative
// lower-bound until the real _stats-based implementation lands.
// ============================================================================

func TestStub_EstimateStorageSize_EmptyZero_NonEmptyPositive(t *testing.T) {
	t.Parallel()
	r := &Repository{}
	if got := r.EstimateStorageSize(context.Background(), nil, nil); got != 0 {
		t.Errorf("empty list: want 0, got %d", got)
	}
	infos := []*types.IndexInfo{{ChunkID: "c1"}, {ChunkID: "c2"}}
	if got := r.EstimateStorageSize(context.Background(), infos, nil); got <= 0 {
		t.Errorf("non-empty list: want >0, got %d", got)
	}
}

func TestStub_SwapToVersion_ReturnsFeatureNotEnabled(t *testing.T) {
	t.Parallel()
	r := &Repository{}
	if err := r.swapToVersion(context.Background(), 768, 2); !errors.Is(err, ErrFeatureNotEnabled) {
		t.Errorf("swapToVersion: want ErrFeatureNotEnabled, got %v", err)
	}
}

// ============================================================================
// Delete path cap — ErrBatchTooLarge guard
// ============================================================================

func TestDeleteByChunkIDList_OverCap_ReturnsErrBatchTooLarge(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%d", i)
	}
	err := r.DeleteByChunkIDList(context.Background(), ids, 768, "")
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Errorf("1001 ids: want ErrBatchTooLarge, got %v", err)
	}
}

func TestDeleteByChunkIDList_Empty_NoOp(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	if err := r.DeleteByChunkIDList(context.Background(), nil, 768, ""); err != nil {
		t.Errorf("empty list: want nil, got %v", err)
	}
}

// ============================================================================
// BatchSave pre-marshal cap — ErrBatchTooLarge guard
// ============================================================================

func TestBatchSave_OverDocCountCap_ReturnsErrBatchTooLarge(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	infos := make([]*types.IndexInfo, 1001)
	embMap := make(map[string][]float32, 1001)
	for i := range infos {
		sid := fmt.Sprintf("s%d", i)
		infos[i] = &types.IndexInfo{SourceID: sid, ChunkID: fmt.Sprintf("c%d", i)}
		embMap[sid] = []float32{0.1, 0.2}
	}
	err := r.BatchSave(context.Background(), infos,
		map[string]any{"embedding": embMap})
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Errorf("1001 infos: want ErrBatchTooLarge, got %v", err)
	}
}

// ============================================================================
// Empty BatchSave is a no-op
// ============================================================================

func TestBatchSave_EmptyList_NoOp(t *testing.T) {
	t.Parallel()
	r := &Repository{
		once:    make(map[int]*sync.Once),
		initErr: make(map[int]error),
	}
	if err := r.BatchSave(context.Background(), nil, nil); err != nil {
		t.Errorf("empty infos: want nil, got %v", err)
	}
}

// ============================================================================
// wrapTransport — sentinel mapping + leak guard
// ============================================================================

func TestWrapTransport_NilPassthrough(t *testing.T) {
	t.Parallel()
	if err := wrapTransport(nil); err != nil {
		t.Errorf("nil in: want nil out, got %v", err)
	}
}

func TestWrapTransport_GenericErrorYieldsTransport(t *testing.T) {
	t.Parallel()
	in := errors.New("network unreachable")
	out := wrapTransport(in)
	if !errors.Is(out, ErrTransport) {
		t.Errorf("generic error: want ErrTransport, got %v", out)
	}
	// Leak guard: cluster-side message ("network unreachable") must NOT
	// be exposed in the wrapped public error.
	if strings.Contains(out.Error(), "network unreachable") {
		t.Errorf("wrapped error must NOT include raw cluster message: %v", out)
	}
}

func TestWrapTransport_401_YieldsErrAuth(t *testing.T) {
	t.Parallel()
	se := &opensearch.StructError{Status: 401, Err: opensearch.Err{Type: "security_exception"}}
	out := wrapTransport(se)
	if !errors.Is(out, ErrAuth) {
		t.Errorf("401: want ErrAuth, got %v", out)
	}
}

func TestWrapTransport_403_YieldsErrAuth(t *testing.T) {
	t.Parallel()
	se := &opensearch.StructError{Status: 403, Err: opensearch.Err{Type: "security_exception"}}
	out := wrapTransport(se)
	if !errors.Is(out, ErrAuth) {
		t.Errorf("403: want ErrAuth, got %v", out)
	}
}

func TestWrapTransport_429CircuitBreaker_YieldsErrCircuitBreaker(t *testing.T) {
	t.Parallel()
	se := &opensearch.StructError{
		Status: 429,
		Err:    opensearch.Err{Type: "knn_circuit_breaker_exception"},
	}
	out := wrapTransport(se)
	if !errors.Is(out, ErrCircuitBreaker) {
		t.Errorf("429 + knn_circuit_breaker_exception: want ErrCircuitBreaker, got %v", out)
	}
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	if isNotFound(nil) {
		t.Error("nil err: must not be NotFound")
	}
	if isNotFound(errors.New("generic")) {
		t.Error("generic err: must not be NotFound")
	}
	se := &opensearch.StructError{Status: 404}
	if !isNotFound(se) {
		t.Error("404 StructError: must be NotFound")
	}
	se2 := &opensearch.StructError{Status: 500}
	if isNotFound(se2) {
		t.Error("500 StructError: must not be NotFound")
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	t.Parallel()
	if isAlreadyExistsError(nil) {
		t.Error("nil err: must not be AlreadyExists")
	}
	se := &opensearch.StructError{
		Status: 400,
		Err:    opensearch.Err{Type: "resource_already_exists_exception"},
	}
	if !isAlreadyExistsError(se) {
		t.Error("400 + resource_already_exists_exception: must be AlreadyExists")
	}
	se2 := &opensearch.StructError{Status: 400, Err: opensearch.Err{Type: "other"}}
	if isAlreadyExistsError(se2) {
		t.Error("400 + other type: must not be AlreadyExists")
	}
}

// ============================================================================
// limitedDecode + drainAndClose helpers
// ============================================================================

func TestLimitedDecode_AppliesLimit(t *testing.T) {
	t.Parallel()
	// 100MB of zeros — limitedDecode with 1KB cap should fail to decode
	// (truncated JSON) rather than allocate the whole thing.
	huge := bytes.NewReader(make([]byte, 100<<20))
	var out map[string]any
	err := limitedDecode(huge, 1024, &out)
	if err == nil {
		t.Error("limitedDecode must fail on truncated input")
	}
}

func TestDrainAndClose_HandlesNil(t *testing.T) {
	t.Parallel()
	drainAndClose(nil) // must not panic
}

func TestDrainAndClose_DrainsAndClosesBody(t *testing.T) {
	t.Parallel()
	body := io.NopCloser(strings.NewReader("hello"))
	drainAndClose(body)
	// Second close should not panic (io.NopCloser is idempotent).
	drainAndClose(body)
}
