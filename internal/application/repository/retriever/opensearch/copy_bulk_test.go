package opensearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestTransformSourceID covers the generated-question / regular / fallback
// branches that CopyIndices uses to remap source_id.
func TestTransformSourceID(t *testing.T) {
	t.Run("regular chunk uses target chunk id", func(t *testing.T) {
		if got := transformSourceID("chunk1", "chunk1", "tgt1"); got != "tgt1" {
			t.Errorf("want tgt1, got %s", got)
		}
	})
	t.Run("generated question preserves question id", func(t *testing.T) {
		if got := transformSourceID("chunk1-q7", "chunk1", "tgt1"); got != "tgt1-q7" {
			t.Errorf("want tgt1-q7, got %s", got)
		}
	})
	t.Run("unrelated source id gets fresh uuid", func(t *testing.T) {
		got := transformSourceID("totally-different", "chunk1", "tgt1")
		if got == "totally-different" || got == "tgt1" || len(got) != 36 {
			t.Errorf("want fresh uuid, got %q", got)
		}
	})
}

// TestCopyIndices_EmptyMapping_NoOp verifies an empty chunk map short-circuits
// before any HTTP call.
func TestCopyIndices_EmptyMapping_NoOp(t *testing.T) {
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()
	err := repo.CopyIndices(context.Background(), "kbSrc", map[string]string{}, map[string]string{}, "kbDst", 768, "manual")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// TestCopyIndices_ScanThenBatchSave verifies the search→BatchSave path:
// remaps IDs, keys the embedding by the *target source id* (OpenSearch
// BatchSave's lookup key), and emits one reindex audit event.
func TestCopyIndices_ScanThenBatchSave(t *testing.T) {
	var (
		mu        sync.Mutex
		bulkBody  string
		searchCnt int
	)
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK) // alias exists → ensureReady no-op
		case strings.Contains(r.URL.Path, "_search"):
			mu.Lock()
			searchCnt++
			first := searchCnt == 1
			mu.Unlock()
			if first {
				_, _ = w.Write([]byte(`{"hits":{"hits":[
					{"_source":{"content":"c","source_id":"srcChunk","source_type":1,"chunk_id":"srcChunk","knowledge_id":"srcKnow","knowledge_base_id":"kbSrc","tag_id":"t","is_enabled":true,"is_recommended":false,"embedding":[0.1,0.2,0.3]}}
				]}}`))
			} else {
				_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
			}
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bulkBody = string(b)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"errors":false,"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}
	repo, ts := newTestRepo(t, handler)
	defer ts.Close()
	spy := &spySink{}
	repo.sink = spy

	err := repo.CopyIndices(context.Background(), "kbSrc",
		map[string]string{"srcKnow": "tgtKnow"}, // knowledge_id remap (sourceToTargetKBIDMap is keyed by knowledge_id, mirroring ES)
		map[string]string{"srcChunk": "tgtChunk"},
		"kbDst", 768, "manual")
	if err != nil {
		t.Fatalf("CopyIndices: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if bulkBody == "" {
		t.Fatal("no bulk request captured")
	}
	// Target IDs present, source IDs gone from the written doc.
	for _, want := range []string{`"chunk_id":"tgtChunk"`, `"knowledge_id":"tgtKnow"`, `"knowledge_base_id":"kbDst"`, `"source_id":"tgtChunk"`} {
		if !strings.Contains(bulkBody, want) {
			t.Errorf("bulk body missing %q\n%s", want, bulkBody)
		}
	}
	if strings.Contains(bulkBody, `"knowledge_base_id":"kbSrc"`) {
		t.Errorf("bulk body leaked source KB id\n%s", bulkBody)
	}
	// Embedding written (keyed by target source id internally).
	if !strings.Contains(bulkBody, "0.1") {
		t.Errorf("embedding not written\n%s", bulkBody)
	}
	if len(spy.reindex) != 1 || spy.reindex[0].docs != 1 {
		t.Errorf("want 1 reindex event with docs=1, got %+v", spy.reindex)
	}
}

// TestBatchUpdateChunkEnabledStatus_GroupedUpdateByQuery verifies the status
// map is grouped by value into one _update_by_query per distinct value, each
// passing chunk ids via terms + the new value via bound script params.
func TestBatchUpdateChunkEnabledStatus_GroupedUpdateByQuery(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_update_by_query") {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"updated":1,"version_conflicts":0,"failures":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}
	repo, ts := newTestRepo(t, handler)
	defer ts.Close()

	err := repo.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{
		"c1": true, "c2": false, "c3": true,
	})
	if err != nil {
		t.Fatalf("BatchUpdateChunkEnabledStatus: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("want 2 grouped update_by_query calls (true/false), got %d: %v", len(bodies), bodies)
	}
	joined := strings.Join(bodies, "\n")
	for _, want := range []string{"c1", "c2", "c3", "is_enabled", "params"} {
		if !strings.Contains(joined, want) {
			t.Errorf("update_by_query bodies missing %q\n%s", want, joined)
		}
	}
}

func TestBatchUpdateChunkEnabledStatus_Empty_NoOp(t *testing.T) {
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()
	if err := repo.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// TestInspectByQueryResponse covers the success path and the failure path,
// asserting cluster-side reason text is NOT surfaced in the returned error.
func TestInspectByQueryResponse(t *testing.T) {
	t.Run("clean response", func(t *testing.T) {
		body := strings.NewReader(`{"updated":5,"version_conflicts":0,"failures":[]}`)
		if err := inspectByQueryResponse(body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("failures do not leak reason", func(t *testing.T) {
		body := strings.NewReader(`{"updated":1,"version_conflicts":0,"failures":[
			{"id":"c9","cause":{"type":"version_conflict_engine_exception","reason":"SECRET document body leaked here"}}
		]}`)
		err := inspectByQueryResponse(body)
		if err == nil {
			t.Fatal("want error for failures, got nil")
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("error leaked cluster reason: %v", err)
		}
		if !strings.Contains(err.Error(), "version_conflict_engine_exception") {
			t.Errorf("error should surface bounded type: %v", err)
		}
	})
}
