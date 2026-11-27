package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestKnowledgeBaseResponsesPreserveIndexingStrategy(t *testing.T) {
	const strategy = `{"vector_enabled":false,"keyword_enabled":true,"wiki_enabled":true,"graph_enabled":false}`
	for _, operation := range []string{"get", "list", "patch"} {
		t.Run(operation, func(t *testing.T) {
			transport := tagTransport(func(req *http.Request) (*http.Response, error) {
				wantMethod := http.MethodGet
				if operation == "patch" {
					wantMethod = http.MethodPatch
				}
				if req.Method != wantMethod {
					t.Errorf("method = %s, want %s", req.Method, wantMethod)
				}
				data := `{"id":"kb-1","indexing_strategy":` + strategy + `}`
				if operation == "list" {
					data = `[` + data + `]`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":` + data + `}`)),
					Header:     make(http.Header),
				}, nil
			})
			c := NewClient("https://example.test", WithTransport(transport))
			ctx := context.Background()
			var kb *KnowledgeBase
			var err error
			switch operation {
			case "get":
				kb, err = c.GetKnowledgeBase(ctx, "kb-1")
			case "list":
				var rows []KnowledgeBase
				rows, err = c.ListKnowledgeBases(ctx)
				if err == nil && len(rows) == 1 {
					kb = &rows[0]
				}
			case "patch":
				kb, err = c.PatchKnowledgeBase(ctx, "kb-1", &PatchKnowledgeBaseRequest{})
			}
			if err != nil || kb == nil {
				t.Fatalf("knowledge base = %#v, err = %v", kb, err)
			}
			body, err := json.Marshal(kb)
			if err != nil {
				t.Fatal(err)
			}
			var received map[string]json.RawMessage
			if err := json.Unmarshal(body, &received); err != nil {
				t.Fatal(err)
			}
			var got, want map[string]bool
			if err := json.Unmarshal(received["indexing_strategy"], &got); err != nil {
				t.Fatalf("indexing strategy lost: %v", err)
			}
			if err := json.Unmarshal([]byte(strategy), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("indexing_strategy = %v, want %v", got, want)
			}
		})
	}
}

func TestKnowledgeBaseMissingIndexingStrategy(t *testing.T) {
	var kb KnowledgeBase
	if err := json.Unmarshal([]byte(`{"id":"legacy-kb"}`), &kb); err != nil {
		t.Fatal(err)
	}
	if kb.IndexingStrategy != nil {
		t.Fatal("missing strategy must remain distinguishable from disabled indexes")
	}
}
