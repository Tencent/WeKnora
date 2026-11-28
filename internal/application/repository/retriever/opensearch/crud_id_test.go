package opensearch

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRepositoryPreservesChunkAndGeneratedQuestions(t *testing.T) {
	t.Parallel()

	type source struct {
		ChunkID  string `json:"chunk_id"`
		SourceID string `json:"source_id"`
		Content  string `json:"content"`
	}

	var mu sync.Mutex
	index := make(map[string]source)
	write := func(decoder *json.Decoder, id string) error {
		var item source
		if err := decoder.Decode(&item); err != nil {
			return err
		}
		mu.Lock()
		index[id] = item
		mu.Unlock()
		return nil
	}
	repo, server := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			for {
				var action struct {
					Index struct {
						ID string `json:"_id"`
					} `json:"index"`
				}
				if err := decoder.Decode(&action); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					t.Errorf("decode bulk action: %v", err)
					http.Error(w, "invalid action", http.StatusBadRequest)
					return
				}
				if err := write(decoder, action.Index.ID); err != nil {
					t.Errorf("decode bulk source: %v", err)
					http.Error(w, "invalid source", http.StatusBadRequest)
					return
				}
			}
			_, _ = io.WriteString(w, `{"errors":false,"items":[]}`)
		case strings.Contains(r.URL.Path, "/_doc/"):
			if err := write(decoder, path.Base(r.URL.Path)); err != nil {
				t.Errorf("decode source: %v", err)
				http.Error(w, "invalid source", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"result":"created"}`)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	const chunkID = "chunk"
	question1 := types.GeneratedQuestionSourceID(chunkID, "first")
	question2 := types.GeneratedQuestionSourceID(chunkID, "second")
	infos := []*types.IndexInfo{
		{ChunkID: chunkID, SourceID: chunkID, Content: "Original text"},
		{ChunkID: chunkID, SourceID: question1, Content: "First related question?"},
		{ChunkID: chunkID, SourceID: question2, Content: "Second related question?"},
		{ChunkID: "legacy", Content: "Source ID omitted"},
	}
	assertIndex := func(t *testing.T) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(index) != len(infos) {
			t.Fatalf("indexed %d sources, want %d", len(index), len(infos))
		}
		for _, info := range infos {
			id := info.SourceID
			if id == "" {
				id = info.ChunkID
			}
			want := source{ChunkID: info.ChunkID, SourceID: info.SourceID, Content: info.Content}
			if got := index[id]; got != want {
				t.Errorf("index[%q] = %+v, want %+v", id, got, want)
			}
		}
	}
	ctx := t.Context()
	for _, info := range infos {
		if err := repo.Save(ctx, info, nil); err != nil {
			t.Fatalf("save %q: %v", info.SourceID, err)
		}
	}
	assertIndex(t)

	infos[1].Content = "Updated first question?"
	if err := repo.BatchSave(ctx, infos, nil); err != nil {
		t.Fatalf("batch save: %v", err)
	}
	assertIndex(t)
}
