package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateKnowledgeFromYouTube(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/knowledge-bases/kb-1/knowledge/youtube" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body struct {
			URLs []string `json:"urls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.URLs) != 2 || body.URLs[1] != "https://www.youtube.com/playlist?list=PL1" {
			t.Fatalf("urls = %v", body.URLs)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"total_videos": 2,
				"truncated":    false,
				"playlists": []map[string]interface{}{{
					"url":         "https://www.youtube.com/playlist?list=PL1",
					"playlist_id": "PL1", "title": "Course", "videos": 1,
				}},
				"created": []map[string]interface{}{{"id": "k-1", "title": "Lesson 1"}},
				"duplicates": []map[string]interface{}{{
					"url":      "https://www.youtube.com/watch?v=bbbbbbbbbbb",
					"video_id": "bbbbbbbbbbb", "knowledge_id": "k-0",
				}},
				"failed": []interface{}{},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("sk-test"))
	result, err := c.CreateKnowledgeFromYouTube(context.Background(), "kb-1", CreateKnowledgeFromYouTubeRequest{
		URLs: []string{"https://youtu.be/aaaaaaaaaaa", "https://www.youtube.com/playlist?list=PL1"},
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeFromYouTube() error = %v", err)
	}
	if result.TotalVideos != 2 || len(result.Playlists) != 1 || result.Playlists[0].Title != "Course" {
		t.Fatalf("unexpected result %+v", result)
	}
	if len(result.Created) != 1 || result.Created[0].ID != "k-1" {
		t.Fatalf("Created = %+v", result.Created)
	}
	if len(result.Duplicates) != 1 || result.Duplicates[0].KnowledgeID != "k-0" {
		t.Fatalf("Duplicates = %+v", result.Duplicates)
	}
}
