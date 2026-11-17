package opensearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// chunkWithQuestions returns the index entries the service layer writes for
// one text chunk that has two generated questions: the chunk text plus one
// entry per question, all sharing the chunk's chunk_id.
func chunkWithQuestions() []*types.IndexInfo {
	return []*types.IndexInfo{
		{SourceID: "c1", ChunkID: "c1", Content: "chunk text"},
		{SourceID: types.GeneratedQuestionSourceID("c1", "q1"), ChunkID: "c1", Content: "question one"},
		{SourceID: types.GeneratedQuestionSourceID("c1", "q2"), ChunkID: "c1", Content: "question two"},
	}
}

// bulkActionIDs extracts the _id of every action line in an NDJSON bulk body.
func bulkActionIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var ids []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for line := 0; sc.Scan(); line++ {
		if line%2 != 0 {
			continue // document line
		}
		var action struct {
			Index struct {
				ID string `json:"_id"`
			} `json:"index"`
		}
		if err := json.Unmarshal(sc.Bytes(), &action); err != nil {
			t.Fatalf("parse action line %d: %v", line, err)
		}
		ids = append(ids, action.Index.ID)
	}
	return ids
}

func TestBuildBulkBody_ChunkAndItsQuestionsGetDistinctDocumentIDs(t *testing.T) {
	t.Parallel()
	infos := chunkWithQuestions()
	body, err := buildBulkBody("alias", infos, make([][]float32, len(infos)), nil)
	if err != nil {
		t.Fatalf("buildBulkBody: %v", err)
	}
	got := bulkActionIDs(t, body)
	want := []string{"c1", "c1-q1", "c1-q2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("bulk _id = %v, want %v (entries of one chunk must not overwrite each other)", got, want)
	}
	// chunk_id stays the chunk's id on every entry so chunk-scoped deletes
	// and updates still reach the question documents.
	if n := strings.Count(string(body), `"chunk_id":"c1"`); n != len(infos) {
		t.Errorf("chunk_id c1 on %d docs, want %d", n, len(infos))
	}
}

func TestSave_UsesSourceIDAsDocumentID(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		paths []string
	)
	repo, ts := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK) // alias exists → ensureReady no-op
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"result":"created"}`))
	})
	defer ts.Close()

	for _, info := range chunkWithQuestions() {
		params := map[string]any{"embedding": map[string][]float32{info.SourceID: {0.1, 0.2, 0.3}}}
		if err := repo.Save(context.Background(), info, params); err != nil {
			t.Fatalf("Save(%s): %v", info.SourceID, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 {
		t.Fatalf("want 3 index requests, got %v", paths)
	}
	for i, want := range []string{"/_doc/c1", "/_doc/c1-q1", "/_doc/c1-q2"} {
		if !strings.HasSuffix(paths[i], want) {
			t.Errorf("request %d path = %s, want suffix %s", i, paths[i], want)
		}
	}
}

func TestDocumentID_FallsBackToChunkIDWhenSourceIDEmpty(t *testing.T) {
	t.Parallel()
	if got := documentID(&types.IndexInfo{ChunkID: "c1"}); got != "c1" {
		t.Errorf("documentID = %q, want c1", got)
	}
}
