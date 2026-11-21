package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMoveKnowledgeIndicesDrainsBeyondOffsetLimitAndRetries(t *testing.T) {
	const count = 10005
	var mu sync.Mutex
	remaining := make(map[string]bool, count)
	for i := range count {
		remaining[fmt.Sprintf("00000000-0000-0000-0001-%012d", i)] = true
	}
	failed := false
	patches, queries := 0, 0
	fake := &fakeWeaviate{
		search: func(req *pb.SearchRequest) (*pb.SearchReply, error) {
			mu.Lock()
			defer mu.Unlock()
			queries++
			assert.Equal(t, "Move_3", req.GetCollection())
			assert.Equal(t, []string{"source"},
				filterTexts(filterCondition(req.GetFilters(), fieldKnowledgeBaseID)))
			assert.Equal(t, []string{"doc"},
				filterTexts(filterCondition(req.GetFilters(), fieldKnowledgeID)))
			assert.Empty(t, req.GetProperties().GetNonRefProperties(), "only IDs are needed")
			if req.GetOffset() != 0 || req.GetAfter() != "" {
				return nil, status.Error(codes.InvalidArgument, "pagination unsupported")
			}
			rows := make([]fakeRow, 0, req.GetLimit())
			for id := range remaining {
				rows = append(rows, fakeRow{id: id})
				if len(rows) == int(req.GetLimit()) {
					break
				}
			}
			return searchReply(rows...), nil
		},
		rest: func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.Method != http.MethodPatch {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			id := path.Base(r.URL.Path)
			assert.True(t, remaining[id], "only source-filtered IDs should be moved")
			var body struct {
				Properties map[string]any `json:"properties"`
				Vector     []float32      `json:"vector"`
				Vectors    map[string]any `json:"vectors"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(400)
				return
			}
			assert.Equal(t, map[string]any{fieldKnowledgeBaseID: "target", fieldTagID: ""}, body.Properties)
			assert.Empty(t, body.Vector, "metadata merge must preserve existing vectors")
			assert.Empty(t, body.Vectors, "metadata merge must preserve existing vectors")
			if patches == 137 && !failed {
				failed = true
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			patches++
			delete(remaining, id)
			w.WriteHeader(http.StatusNoContent)
		},
	}
	repo := fake.repository(t, "Move")
	require.Error(t, repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""))
	mu.Lock()
	left := len(remaining)
	mu.Unlock()
	require.Equal(t, count-137, left)
	require.NoError(t, repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""))
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, remaining)
	require.Equal(t, count, patches)
	require.Greater(t, queries, 100)
}

func TestMoveKnowledgeIndicesRejectsNoProgress(t *testing.T) {
	fake := &fakeWeaviate{
		search: func(*pb.SearchRequest) (*pb.SearchReply, error) {
			return searchReply(fakeRow{id: "00000000-0000-0000-0000-000000000001"}), nil
		},
		rest: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	}
	repo := fake.repository(t, "Move")
	require.ErrorContains(
		t,
		repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""),
		"no progress",
	)
}
