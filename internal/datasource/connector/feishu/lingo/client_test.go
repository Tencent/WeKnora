package lingo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticTokens string

func (s staticTokens) GetTenantAccessToken(context.Context) (string, error) {
	return string(s), nil
}

func newTestHTTPAPI(server *httptest.Server) *httpAPI {
	return &httpAPI{
		baseURL:    server.URL,
		tokens:     staticTokens("test-token"),
		httpClient: server.Client(),
	}
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func TestListRepos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/lingo/v1/repos" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"msg":  "success",
			"data": map[string]interface{}{
				"items": []map[string]string{{"id": "repo-1", "name": "Glossary"}},
			},
		})
	}))
	defer server.Close()

	repos, err := newTestHTTPAPI(server).ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos() error = %v", err)
	}
	if len(repos) != 1 || repos[0].ID != "repo-1" || repos[0].Name != "Glossary" {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestListEntitiesPaginates(t *testing.T) {
	var repoIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/lingo/v1/entities" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		repoIDs = append(repoIDs, r.URL.Query().Get("repo_id"))
		if r.URL.Query().Get("page_token") == "" {
			writeJSON(w, map[string]interface{}{
				"code": 0,
				"msg":  "success",
				"data": map[string]interface{}{
					"entities": []map[string]interface{}{{
						"id":          "e1",
						"main_keys":   []map[string]string{{"key": "OT"}},
						"description": "one",
						"update_time": "100",
					}},
					"has_more":   true,
					"page_token": "p2",
				},
			})
			return
		}
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"msg":  "success",
			"data": map[string]interface{}{
				"entities": []map[string]interface{}{{
					"id":          "e2",
					"main_keys":   []map[string]string{{"key": "AS"}},
					"description": "two",
					"update_time": "200",
				}},
				"has_more": false,
			},
		})
	}))
	defer server.Close()

	entities, err := newTestHTTPAPI(server).ListEntities(context.Background(), "repo-1", lingoPageSize)
	if err != nil {
		t.Fatalf("ListEntities() error = %v", err)
	}
	if len(entities) != 2 || entities[0].ID != "e1" || entities[1].ID != "e2" {
		t.Fatalf("entities = %#v", entities)
	}
	if len(repoIDs) != 2 || repoIDs[0] != "repo-1" || repoIDs[1] != "repo-1" {
		t.Fatalf("repo_id params = %#v", repoIDs)
	}
}

func TestListEntitiesRetriesRateLimit(t *testing.T) {
	oldDelay := lingoRetryBaseDelay
	lingoRetryBaseDelay = time.Millisecond
	defer func() { lingoRetryBaseDelay = oldDelay }()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{
				"entities": []map[string]string{{"id": "e1"}},
				"has_more": false,
			},
		})
	}))
	defer server.Close()

	entities, err := newTestHTTPAPI(server).ListEntities(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("ListEntities() error = %v", err)
	}
	if len(entities) != 1 || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("entities = %#v, calls = %d", entities, calls)
	}
}

func TestListEntitiesRejectsRepeatedPageToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{
				"entities":   []map[string]string{{"id": "e1"}},
				"has_more":   true,
				"page_token": "same",
			},
		})
	}))
	defer server.Close()

	_, err := newTestHTTPAPI(server).ListEntities(context.Background(), "", 100)
	if err == nil || !strings.Contains(err.Error(), "repeated page_token") {
		t.Fatalf("error = %v", err)
	}
}

func TestListEntitiesSurfacesAPICode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]interface{}{"code": 2250001, "msg": "invalid param"})
	}))
	defer server.Close()

	_, err := newTestHTTPAPI(server).ListEntities(context.Background(), "", 100)
	if err == nil || !strings.Contains(err.Error(), "code=2250001") {
		t.Fatalf("error = %v", err)
	}
}

func TestListEntitiesDoesNotRetryForbidden(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := newTestHTTPAPI(server).ListEntities(context.Background(), "", 100)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestListEntitiesPropagatesContextCancellation(t *testing.T) {
	oldDelay := lingoRetryBaseDelay
	lingoRetryBaseDelay = time.Second
	defer func() { lingoRetryBaseDelay = oldDelay }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newTestHTTPAPI(server).ListEntities(ctx, "", 100); err == nil {
		t.Fatal("expected cancellation error")
	}
}
