package outline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeOutline is an httptest-backed stand-in for an Outline instance.
type fakeOutline struct {
	server *httptest.Server
	mux    *http.ServeMux
	// docs is the document set returned by documents.list, in order.
	docs []document
	// hits counts requests per path so tests can assert pagination behaviour.
	hits map[string]*int32
	// attachments records which attachment ids the fake instance will serve.
	attachments map[string]bool
}

func newFakeOutline(docs []document) *fakeOutline {
	f := &fakeOutline{mux: http.NewServeMux(), docs: docs, hits: map[string]*int32{}}
	f.server = httptest.NewServer(f.mux)

	f.handleJSON("/api/auth.info", 200, map[string]interface{}{
		"data": map[string]interface{}{
			"team": map[string]string{"id": "team-1", "name": "Acme"},
			"user": map[string]string{"email": "must-not-be-logged@example.com"},
		},
	})
	f.handleJSON("/api/collections.list", 200, collectionsListResponse{
		Data: []collection{
			{ID: "col-1", Name: "Handbook", URL: "/collection/handbook-abc", UpdatedAt: "2026-09-01T00:00:00.000Z"},
			{ID: "col-2", Name: "Runbooks", URL: "/collection/runbooks-def", UpdatedAt: "2026-09-02T00:00:00.000Z"},
		},
		Pagination: pagination{Offset: 0, Limit: 100, Total: 2},
	})

	// documents.list paginates two documents per page so the client's paging
	// loop is actually exercised.
	f.mux.HandleFunc("/api/documents.list", func(w http.ResponseWriter, r *http.Request) {
		f.count("/api/documents.list")
		var body struct {
			CollectionID string `json:"collectionId"`
			Limit        int    `json:"limit"`
			Offset       int    `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		const pageSize = 2
		var page []document
		for i := body.Offset; i < len(f.docs) && i < body.Offset+pageSize; i++ {
			page = append(page, f.docs[i])
		}
		writeJSON(w, 200, documentsListResponse{
			Data:       page,
			Pagination: pagination{Offset: body.Offset, Limit: pageSize, Total: len(f.docs)},
		})
	})

	return f
}

func (f *fakeOutline) count(path string) {
	if f.hits[path] == nil {
		var z int32
		f.hits[path] = &z
	}
	atomic.AddInt32(f.hits[path], 1)
}

func (f *fakeOutline) hitCount(path string) int {
	if f.hits[path] == nil {
		return 0
	}
	return int(atomic.LoadInt32(f.hits[path]))
}

func (f *fakeOutline) handleJSON(path string, status int, payload interface{}) {
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		f.count(path)
		writeJSON(w, status, payload)
	})
}

// serveAttachment makes one attachment id downloadable from the fake instance,
// through the same 302 that a real Outline emits.
func (f *fakeOutline) serveAttachment(id string, data []byte, contentType string) {
	f.mux.HandleFunc("/api/files.get/"+id, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(200)
		_, _ = w.Write(data)
	})
	if f.attachments == nil {
		f.attachments = map[string]bool{}
		f.mux.HandleFunc("/api/attachments.redirect", func(w http.ResponseWriter, r *http.Request) {
			f.count("/api/attachments.redirect")
			aid := r.URL.Query().Get("id")
			if !f.attachments[aid] {
				w.WriteHeader(404)
				return
			}
			http.Redirect(w, r, f.server.URL+"/api/files.get/"+aid, http.StatusFound)
		})
	}
	f.attachments[id] = true
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (f *fakeOutline) Close() { f.server.Close() }

func (f *fakeOutline) cfg() *Config {
	return &Config{APIToken: "ol_api_test_token_value", BaseURL: f.server.URL}
}

func TestClient_Ping(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	if err := newClient(f.cfg()).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClient_Ping_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, apiErrorBody{Error: "authentication_required"})
	}))
	defer srv.Close()

	err := newClient(&Config{APIToken: "bad", BaseURL: srv.URL}).Ping(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestClient_SendsAuthAndUserAgent(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		writeJSON(w, 200, authInfoResponse{})
	}))
	defer srv.Close()

	if err := newClient(&Config{APIToken: "tok", BaseURL: srv.URL}).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
	}
	// Assert the connector's own User-Agent, not merely a non-empty one: Go's
	// http client substitutes "Go-http-client/1.1" when the header is unset, so
	// an emptiness check would pass even if the connector stopped setting it.
	// Cloudflare, a common fronting for self-hosted Outline, answers 403 with
	// error code 1010 for requests it considers UA-less.
	if gotUA != userAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, userAgent)
	}
}

func TestClient_ListCollections(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	cols, err := newClient(f.cfg()).ListCollections(context.Background())
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("len = %d, want 2", len(cols))
	}
	if cols[0].Name != "Handbook" {
		t.Errorf("first name = %q", cols[0].Name)
	}
}

func TestClient_ListCollectionDocuments_Paginates(t *testing.T) {
	docs := []document{
		{ID: "d1", Title: "One", Text: "# One", Revision: 1},
		{ID: "d2", Title: "Two", Text: "# Two", Revision: 1},
		{ID: "d3", Title: "Three", Text: "# Three", Revision: 1},
	}
	f := newFakeOutline(docs)
	defer f.Close()

	got, err := newClient(f.cfg()).ListCollectionDocuments(context.Background(), "col-1")
	if err != nil {
		t.Fatalf("ListCollectionDocuments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (pagination loop did not drain all pages)", len(got))
	}
	if got[2].Title != "Three" {
		t.Errorf("third title = %q", got[2].Title)
	}
	// 3 docs at 2 per page = 2 requests.
	if n := f.hitCount("/api/documents.list"); n != 2 {
		t.Errorf("documents.list called %d times, want 2", n)
	}
}

func TestClient_DownloadAttachment_FollowsRedirect(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'r', 'e', 's', 't'}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/api/attachments.redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/api/files.get?key=abc", http.StatusFound)
	})
	mux.HandleFunc("/api/files.get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write(png)
	})

	data, ctype, err := newClient(&Config{APIToken: "tok", BaseURL: srv.URL}).
		DownloadAttachment(context.Background(), "att-1")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	if len(data) != len(png) {
		t.Errorf("len = %d, want %d", len(data), len(png))
	}
	if ctype != "image/png" {
		t.Errorf("contentType = %q, want image/png", ctype)
	}
}

func TestClient_DownloadAttachment_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, _, err := newClient(&Config{APIToken: "tok", BaseURL: srv.URL}).
		DownloadAttachment(context.Background(), "gone")
	if err == nil {
		t.Fatal("expected error on 404 (a deleted attachment must not look like success)")
	}
}

func TestClient_RetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		writeJSON(w, 200, authInfoResponse{})
	}))
	defer srv.Close()

	if err := newClient(&Config{APIToken: "tok", BaseURL: srv.URL}).Ping(context.Background()); err != nil {
		t.Fatalf("Ping should have succeeded after retry: %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("calls = %d, want at least 2 (no retry happened)", calls)
	}
}
