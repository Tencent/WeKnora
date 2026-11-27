package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChunkRevisionRoundTrip(t *testing.T) {
	for _, revision := range []int{0, 7} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			transport := tagTransport(func(req *http.Request) (*http.Response, error) {
				body := fmt.Sprintf(
					`{"success":true,"data":[{"id":"chunk-1","content_revision":%d}],"total":1}`, revision,
				)
				if req.Method == http.MethodPut {
					var input map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						t.Fatal(err)
					}
					if got := string(input["expected_revision"]); got != fmt.Sprint(revision) {
						t.Errorf("expected_revision = %q, want %d", got, revision)
					}
					body = fmt.Sprintf(`{"success":true,"data":{"id":"chunk-1","content_revision":%d}}`, revision+1)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			})
			c := NewClient("https://example.test", WithTransport(transport))
			chunks, _, err := c.ListKnowledgeChunks(context.Background(), "doc-1", 1, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(chunks) != 1 {
				t.Fatalf("got %d chunks, want 1", len(chunks))
			}
			if chunks[0].ContentRevision != revision {
				t.Fatalf("content_revision = %d, want %d", chunks[0].ContentRevision, revision)
			}
			updated, err := c.UpdateChunk(context.Background(), "doc-1", chunks[0].ID, &UpdateChunkRequest{
				Content: "Updated content", IsEnabled: true, ExpectedRevision: &chunks[0].ContentRevision,
			})
			if err != nil {
				t.Fatal(err)
			}
			if updated.ContentRevision != revision+1 {
				t.Errorf("updated content_revision = %d, want %d", updated.ContentRevision, revision+1)
			}
		})
	}
}

func TestUpdateChunkOmitsUnsetExpectedRevision(t *testing.T) {
	body, err := json.Marshal(UpdateChunkRequest{Content: "Updated content", IsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(body, &input); err != nil {
		t.Fatal(err)
	}
	if _, exists := input["expected_revision"]; exists {
		t.Fatal("legacy requests must omit an unset expected_revision")
	}
}
