package opensearch

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Issue: https://github.com/Tencent/WeKnora/issues/5608
// A chunk emits several index documents that share one ChunkID and differ
// only in SourceID: the chunk text and each generated question. The bulk
// _id must therefore key on SourceID, or every write replaces the previous
// one and only one question per chunk survives.
func TestBulkBodyUsesSourceIDAsDocumentID(t *testing.T) {
	chunkID := "chunk-1"
	infos := []*types.IndexInfo{
		{ChunkID: chunkID, SourceID: chunkID, Content: "chunk text"},
		{ChunkID: chunkID, SourceID: chunkID + "-q1", Content: "question one"},
		{ChunkID: chunkID, SourceID: chunkID + "-q2", Content: "question two"},
	}
	body, err := buildBulkBody("idx", infos, [][]float32{nil, nil, nil}, nil)
	if err != nil {
		t.Fatalf("buildBulkBody: %v", err)
	}
	ids := map[string]bool{}
	sep := []byte("\n")
	lines := bytes.Split(bytes.TrimSpace(body), sep)
	for i := 0; i+1 < len(lines); i += 2 {
		var action struct {
			Index map[string]string `json:"index"`
		}
		if err := json.Unmarshal(lines[i], &action); err != nil {
			t.Fatalf("unmarshal action line %d: %v", i, err)
		}
		inner, ok := action.Index["_id"]
		if !ok {
			t.Fatalf("action line %d has no _id: %s", i, lines[i])
		}
		if inner != "chunk-1" && inner != "chunk-1-q1" && inner != "chunk-1-q2" {
			t.Fatalf("unexpected _id %s", inner)
		}
		if ids[inner] {
			t.Fatalf("duplicate _id %s: entries overwrite each other", inner)
		}
		ids[inner] = true
	}
	if len(ids) != 3 {
		t.Fatalf("got %d distinct _ids, want 3 (chunk text + 2 questions)", len(ids))
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5608
// Save must key the single-document _id on SourceID for the same reason:
// a generated question indexed via Save would otherwise replace the chunk
// text that shares its ChunkID.
func TestDocumentIDForUsesSourceID(t *testing.T) {
	info := &types.IndexInfo{ChunkID: "c9", SourceID: "c9-q7"}
	if got := documentIDFor(info); got != "c9-q7" {
		t.Fatalf("documentIDFor = %s, want SourceID c9-q7", got)
	}
	plain := &types.IndexInfo{ChunkID: "c9", SourceID: "c9"}
	if got := documentIDFor(plain); got != "c9" {
		t.Fatalf("documentIDFor = %s, want c9", got)
	}
}
