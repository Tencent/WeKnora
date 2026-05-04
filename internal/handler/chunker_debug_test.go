package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestComputeChunkSizeStats_Empty(t *testing.T) {
	stats := computeChunkSizeStats(nil)
	if stats.Count != 0 || stats.AvgChars != 0 || stats.MaxChars != 0 {
		t.Errorf("empty input should yield zero stats, got %+v", stats)
	}
}

func TestComputeChunkSizeStats_SingleChunk(t *testing.T) {
	stats := computeChunkSizeStats([]int{500})
	if stats.Count != 1 {
		t.Errorf("count: got %d want 1", stats.Count)
	}
	if stats.AvgChars != 500 || stats.MinChars != 500 || stats.MaxChars != 500 {
		t.Errorf("single-chunk stats should all equal 500, got %+v", stats)
	}
	if stats.StddevChars != 0 {
		t.Errorf("stddev for one element should be 0, got %d", stats.StddevChars)
	}
}

func TestComputeChunkSizeStats_VaryingSizes(t *testing.T) {
	// 100, 200, 300, 400, 500 → avg 300, stddev ≈ 141
	stats := computeChunkSizeStats([]int{100, 200, 300, 400, 500})
	if stats.Count != 5 {
		t.Errorf("count: got %d want 5", stats.Count)
	}
	if stats.AvgChars != 300 {
		t.Errorf("avg: got %d want 300", stats.AvgChars)
	}
	if stats.MinChars != 100 || stats.MaxChars != 500 {
		t.Errorf("min/max: got %d/%d want 100/500", stats.MinChars, stats.MaxChars)
	}
	if stats.StddevChars < 130 || stats.StddevChars > 150 {
		t.Errorf("stddev: got %d, want ~141", stats.StddevChars)
	}
}

func TestComputeChunkSizeStats_NoVarianceUnderflow(t *testing.T) {
	// All identical — variance must clamp to 0 not flip negative on
	// float-precision rounding.
	stats := computeChunkSizeStats([]int{1234, 1234, 1234, 1234})
	if stats.StddevChars != 0 {
		t.Errorf("identical values must yield stddev=0, got %d", stats.StddevChars)
	}
}

// --- PreviewChunking httptest -------------------------------------------------

func newPreviewRouter() *gin.Engine {
	r := gin.New()
	r.POST("/chunker/preview", PreviewChunking)
	return r
}

func postPreview(t *testing.T, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := newPreviewRouter()
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/chunker/preview", buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var parsed map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	}
	return w, parsed
}

func TestPreviewChunking_HappyPath_AutoStrategy(t *testing.T) {
	body := PreviewChunkingRequest{
		Text: "# Top\nintro paragraph here.\n\n## Section A\nbody A.\n\n## Section B\nbody B.",
		ChunkingConfig: PreviewChunkingPayload{
			ChunkSize:    200,
			ChunkOverlap: 20,
			Separators:   []string{"\n\n", "\n"},
			Strategy:     "auto",
		},
	}
	w, parsed := postPreview(t, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if parsed["success"] != true {
		t.Fatalf("success flag missing or false: %v", parsed)
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing: %v", parsed)
	}
	if data["selected_tier"] == "" {
		t.Errorf("selected_tier must be set, got %v", data["selected_tier"])
	}
	if _, ok := data["chunks"].([]any); !ok {
		t.Errorf("chunks must be an array, got %T", data["chunks"])
	}
	stats, ok := data["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats must be an object, got %T", data["stats"])
	}
	if c, _ := stats["count"].(float64); c <= 0 {
		t.Errorf("stats.count should be > 0, got %v", stats["count"])
	}
}

func TestPreviewChunking_RejectsEmptyText(t *testing.T) {
	w, parsed := postPreview(t, PreviewChunkingRequest{Text: "   \n\t   "})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", w.Code)
	}
	if errStr, _ := parsed["error"].(string); !strings.Contains(errStr, "empty") {
		t.Errorf("error should mention 'empty', got %q", errStr)
	}
}

func TestPreviewChunking_RejectsOversizedText(t *testing.T) {
	body := PreviewChunkingRequest{Text: strings.Repeat("a", previewMaxChars+1)}
	w, parsed := postPreview(t, body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d want 413", w.Code)
	}
	if parsed["limit"] == nil {
		t.Errorf("response should include limit hint, got %v", parsed)
	}
}

func TestPreviewChunking_LegacyStrategy_NoProfile(t *testing.T) {
	// Auto-strategy is the only path that produces a profile inside
	// SplitWithDiagnostics. For explicit strategies the handler
	// materializes one itself so the UI always sees stats.
	body := PreviewChunkingRequest{
		Text: "para one.\n\npara two.\n\npara three.\n\npara four.",
		ChunkingConfig: PreviewChunkingPayload{
			ChunkSize:    100,
			ChunkOverlap: 10,
			Separators:   []string{"\n\n"},
			Strategy:     "legacy",
		},
	}
	w, parsed := postPreview(t, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	data := parsed["data"].(map[string]any)
	if data["profile"] == nil {
		t.Error("profile should be materialized for explicit strategy too")
	}
	if string(chunker.StrategyTier(data["selected_tier"].(string))) != string(chunker.TierLegacy) {
		t.Errorf("selected_tier: got %v want %s", data["selected_tier"], chunker.TierLegacy)
	}
}

func TestPreviewChunking_ChunkTruncation(t *testing.T) {
	// Build text that produces > previewMaxChunks chunks.
	body := PreviewChunkingRequest{
		Text: strings.Repeat("x.\n\n", previewMaxChunks+50),
		ChunkingConfig: PreviewChunkingPayload{
			ChunkSize:    3,
			ChunkOverlap: 0,
			Separators:   []string{"\n\n"},
			Strategy:     "legacy",
		},
	}
	w, parsed := postPreview(t, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	data := parsed["data"].(map[string]any)
	chunks := data["chunks"].([]any)
	if len(chunks) > previewMaxChunks {
		t.Errorf("chunks should be truncated to ≤%d, got %d", previewMaxChunks, len(chunks))
	}
	stats := data["stats"].(map[string]any)
	if truncated, _ := stats["truncated_to"].(float64); int(truncated) <= previewMaxChunks {
		t.Errorf("stats.truncated_to should reflect ORIGINAL count > %d, got %v", previewMaxChunks, truncated)
	}
}
