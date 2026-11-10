package client

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBatchKnowledgeTagsPreservesEmptyList(t *testing.T) {
	for _, tags := range [][]string{nil, {}, {"tag-1"}} {
		request := BatchUpdateKnowledgeTagsRequest{KnowledgeIDs: []string{"k-1"}, TagIDs: tags}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		// Decode as the server does: nil is rejected, [] deliberately clears tags.
		var received struct {
			KnowledgeIDs []string `json:"knowledge_ids"`
			TagIDs       []string `json:"tag_ids"`
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tags, received.TagIDs) {
			t.Errorf("tags = %#v, want %#v; body = %s", received.TagIDs, tags, body)
		}
		if !reflect.DeepEqual(request.KnowledgeIDs, received.KnowledgeIDs) {
			t.Errorf("knowledge IDs = %#v, want %#v", received.KnowledgeIDs, request.KnowledgeIDs)
		}
	}
}
