package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestPatchKnowledgeBasePreservesFieldPresence(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"asr_config":{"language":"en"}}`,
		`{"asr_config":{"enabled":false}}`,
		`{"asr_config":{"model_id":""}}`,
		`{"asr_config":{"language":""}}`,
		`{"faq_config":{"question_index_mode":"separate"}}`,
		`{"faq_config":{"index_mode":"question_only"}}`,
		`{"auto_tag_config":{"max_tags":5}}`,
		`{"auto_tag_config":{"enabled":false,"skip_if_tagged":false}}`,
		`{"auto_tag_config":{"model_id":"","max_tags":0}}`,
		`{"image_processing_config":{}}`,
		`{"image_processing_config":{"model_id":""}}`,
		`{"chunking_config":{"chunk_overlap":0,"separators":[]}}`,
		`{"indexing_strategy":{"vector_enabled":false}}`,
		`{"vlm_config":{"enabled":false}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request PatchKnowledgeBaseRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(body), &want); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/knowledge-bases/kb-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(want, got) {
					t.Errorf("request changed field presence: got %#v, want %#v", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"success": true, "data": map[string]any{"id": "kb-1"},
				}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			kb, err := NewClient(srv.URL).PatchKnowledgeBase(context.Background(), "kb-1", &request)
			if err != nil {
				t.Fatal(err)
			}
			if kb.ID != "kb-1" {
				t.Errorf("unexpected knowledge base: %#v", kb)
			}
		})
	}
}
