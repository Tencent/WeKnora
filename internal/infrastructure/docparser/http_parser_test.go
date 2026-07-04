package docparser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestHTTPDocumentReaderHealthCheckUsesHealthEndpoint(t *testing.T) {
	healthCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathHealth {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		healthCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	reader, err := NewHTTPDocumentReader(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader: %v", err)
	}
	if err := reader.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if healthCalls != 1 {
		t.Fatalf("health calls = %d, want 1", healthCalls)
	}
}

func TestHTTPDocumentReaderHealthCheckFallsBackToListEngines(t *testing.T) {
	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathHealth:
			http.NotFound(w, r)
		case PathListEngines:
			if r.Method != http.MethodPost {
				t.Fatalf("list-engines method = %s, want POST", r.Method)
			}
			listCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"engines":[{"name":"builtin","description":"ok","file_types":["pdf"],"available":true}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	reader, err := NewHTTPDocumentReader(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader: %v", err)
	}
	if err := reader.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("list-engines calls = %d, want 1", listCalls)
	}
}

func TestHTTPDocumentReaderReadRetriesOnceOnUnavailable(t *testing.T) {
	readCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathRead {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		readCalls++
		if readCalls == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"markdown_content":"ok"}`))
	}))
	defer server.Close()

	reader, err := NewHTTPDocumentReader(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader: %v", err)
	}
	result, err := reader.Read(context.Background(), &types.ReadRequest{FileName: "a.md", FileType: "md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if result.MarkdownContent != "ok" {
		t.Fatalf("markdown = %q, want ok", result.MarkdownContent)
	}
	if readCalls != 2 {
		t.Fatalf("read calls = %d, want 2", readCalls)
	}
}

func TestHTTPDocumentReaderReadDoesNotRetryOnBadRequest(t *testing.T) {
	readCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathRead {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		readCalls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	reader, err := NewHTTPDocumentReader(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader: %v", err)
	}
	if _, err := reader.Read(context.Background(), &types.ReadRequest{}); err == nil {
		t.Fatal("Read error = nil, want error")
	}
	if readCalls != 1 {
		t.Fatalf("read calls = %d, want 1", readCalls)
	}
}
