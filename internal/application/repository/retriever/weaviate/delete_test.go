package weaviate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// batchDeleteTestServer answers the SDK's batch delete with the body handed to
// it. Weaviate reports the per-object outcome there and still answers HTTP 200
// when it could not delete everything, so the response body is the only place a
// partial deletion is visible.
type batchDeleteTestServer struct {
	// body is the JSON the server answers a DELETE /v1/batch/objects with.
	body string
	// status overrides the 200 the real endpoint answers with.
	status int
	// calls counts the batch deletes that reached the server.
	calls int
}

func (s *batchDeleteTestServer) repository(t *testing.T) *weaviateRepository {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/batch/objects":
			s.calls++
			// The filter has to be the one this repository builds: by source
			// id, plus the collection for the requested dimension. The SDK
			// sends more than one value as valueTextArray.
			var req struct {
				Output string `json:"output"`
				Match  struct {
					Class string `json:"class"`
					Where struct {
						Path     []string `json:"path"`
						Operator string   `json:"operator"`
						Value    []string `json:"valueTextArray"`
					} `json:"where"`
				} `json:"match"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, "Del_3", req.Match.Class)
			assert.Equal(t, []string{fieldSourceID}, req.Match.Where.Path)
			assert.Equal(t, "ContainsAny", req.Match.Where.Operator)
			assert.Equal(t, []string{"src-1", "src-2"}, req.Match.Where.Value)
			assert.Equal(t, "minimal", req.Output)
			status := s.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(s.body))
		case r.URL.Path == "/v1/meta":
			// SDK probes the server version before object writes.
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	return &weaviateRepository{client: client, collectionBaseName: "Del"}
}

// batchDeleteBody builds the response body Weaviate writes for a batch delete,
// from the counts it reports. objects mirrors the endpoint's output=minimal
// shape: only the objects it could not delete are listed.
func batchDeleteBody(matches, successful, failed, limit int64, failedObjects ...string) string {
	objects := make([]map[string]any, 0, len(failedObjects))
	for _, id := range failedObjects {
		objects = append(objects, map[string]any{
			"id":     id,
			"status": "FAILED",
			"errors": map[string]any{
				"error": []any{map[string]any{"message": "shard unavailable"}},
			},
		})
	}
	body, _ := json.Marshal(map[string]any{
		"output": "minimal",
		"results": map[string]any{
			"matches":    matches,
			"successful": successful,
			"failed":     failed,
			"limit":      limit,
			"objects":    objects,
		},
	})
	return string(body)
}

// The regression this guards: a batch delete whose objects could not be removed
// is answered with HTTP 200 and failed=1, successful=0, and the SDK returns a
// nil error for it. Trusting that error lets the caller delete the chunk rows
// that name the surviving objects, so nothing is left to retry the purge with
// while the stale vectors keep occupying TopK slots.
func TestDeleteBySourceIDListFailsWhenWeaviateReportsUndeletedObjects(t *testing.T) {
	// Real Weaviate wording for a delete that matched one object and failed it.
	server := &batchDeleteTestServer{
		body: batchDeleteBody(1, 0, 1, 10000, "00000000-0000-0000-0000-000000000001"),
	}
	repo := server.repository(t)

	err := repo.DeleteBySourceIDList(
		context.Background(), []string{"src-1", "src-2"}, 3, types.KnowledgeBaseTypeDocument)

	require.Error(t, err, "an object the index still holds must not be reported as deleted")
	assert.Contains(t, err.Error(), "1 object(s) still in Del_3")
	assert.Contains(t, err.Error(), "deleted 0 of 1 matched")
	assert.Equal(t, 1, server.calls)
}

// A batch delete that reports every matched object deleted stays a success,
// including the common case where the filter matched nothing at all: deleting an
// id that has no entry is a no-op, not a failed deletion.
func TestDeleteBySourceIDListSucceedsWhenEveryMatchedObjectIsDeleted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "all deleted", body: batchDeleteBody(2, 2, 0, 10000)},
		{name: "nothing matched", body: batchDeleteBody(0, 0, 0, 10000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &batchDeleteTestServer{body: tc.body}
			repo := server.repository(t)

			require.NoError(t, repo.DeleteBySourceIDList(
				context.Background(), []string{"src-1", "src-2"}, 3, types.KnowledgeBaseTypeDocument))
			assert.Equal(t, 1, server.calls)
		})
	}
}

// Weaviate deletes at most QUERY_MAXIMUM_RESULTS objects per call and reports
// the whole match count, so a truncated deletion can come back with failed=0
// and objects still in the index. Those objects are undeleted too.
func TestDeleteBySourceIDListFailsWhenWeaviateTruncatesAtTheQueryLimit(t *testing.T) {
	server := &batchDeleteTestServer{body: batchDeleteBody(3, 2, 0, 2)}
	repo := server.repository(t)

	err := repo.DeleteBySourceIDList(
		context.Background(), []string{"src-1", "src-2"}, 3, types.KnowledgeBaseTypeDocument)

	require.Error(t, err, "objects beyond the per-call limit are still in the index")
	assert.Contains(t, err.Error(), "1 object(s) still in Del_3")
	assert.Contains(t, err.Error(), "limit 2")
}

// A 200 without a results block acknowledges nothing, so the deletion cannot be
// treated as done.
func TestDeleteBySourceIDListFailsWithoutAnAcknowledgement(t *testing.T) {
	server := &batchDeleteTestServer{body: `{"output":"minimal"}`}
	repo := server.repository(t)

	err := repo.DeleteBySourceIDList(
		context.Background(), []string{"src-1", "src-2"}, 3, types.KnowledgeBaseTypeDocument)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no results block")
}

// The empty list short-circuits before any request, as before.
func TestDeleteBySourceIDListSkipsAnEmptyList(t *testing.T) {
	server := &batchDeleteTestServer{body: batchDeleteBody(0, 0, 0, 10000)}
	repo := server.repository(t)

	require.NoError(t, repo.DeleteBySourceIDList(
		context.Background(), nil, 3, types.KnowledgeBaseTypeDocument))
	assert.Zero(t, server.calls, "an empty source id list must not reach the server")
}
