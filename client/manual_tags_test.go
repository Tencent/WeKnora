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

func TestCreateManualKnowledgeTags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request CreateManualKnowledgeRequest
		want    []string
	}{
		{"legacy", CreateManualKnowledgeRequest{TagID: "tag-1"}, []string{"tag-1"}},
		{"multiple", CreateManualKnowledgeRequest{TagIDs: []string{"tag-1", "tag-2"}}, []string{"tag-1", "tag-2"}},
		{"precedence", CreateManualKnowledgeRequest{TagID: "old", TagIDs: []string{"new"}}, []string{"new"}},
		{"untagged", CreateManualKnowledgeRequest{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.request
			transport := tagTransport(func(req *http.Request) (*http.Response, error) {
				var received struct {
					TagIDs []string `json:"tag_ids"`
				}
				if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(received.TagIDs, tc.want) {
					t.Errorf("tag_ids = %v, want %v", received.TagIDs, tc.want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"id":"doc-1"}}`)),
					Header:     make(http.Header),
				}, nil
			})
			c := NewClient("https://example.test", WithTransport(transport))
			_, err := c.CreateManualKnowledge(context.Background(), "kb-1", &tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tc.request, original) {
				t.Fatal("request was mutated")
			}
		})
	}
}
