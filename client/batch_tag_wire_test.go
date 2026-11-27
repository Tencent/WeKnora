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

func TestBatchUpdateKnowledgeTagsWireFormat(t *testing.T) {
	tag := "tag-1"
	updates := map[string]*string{"doc-1": &tag, "doc-2": nil}
	transport := tagTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut || req.URL.Path != "/api/v1/knowledge/tags" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		// Decode the body using the same type as KnowledgeTagBatchRequest.
		var received struct {
			Updates map[string][]string `json:"updates"`
		}
		if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
			t.Errorf("server cannot decode request: %v", err)
		}
		want := map[string][]string{"doc-1": {"tag-1"}, "doc-2": {}}
		if !reflect.DeepEqual(received.Updates, want) {
			t.Errorf("updates = %#v, want %#v", received.Updates, want)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
			Header:     make(http.Header),
		}, nil
	})
	c := NewClient("https://example.test", WithTransport(transport))
	if err := c.BatchUpdateKnowledgeTags(context.Background(), updates); err != nil {
		t.Fatal(err)
	}
	if updates["doc-1"] != &tag || updates["doc-2"] != nil {
		t.Fatal("caller-owned updates were mutated")
	}
}
