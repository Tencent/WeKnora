package weknora

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

func TestCreateManualKnowledgeUsesPublicEndpointAndAPIKey(t *testing.T) {
	t.Helper()
	var got ManualKnowledgeInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/api/v1/knowledge-bases/kb-1/knowledge/manual" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "secret" {
			t.Fatalf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"knowledge-1","knowledge_base_id":"kb-1"}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL + "/", APIKey: "secret", KBID: "kb-1"})
	result, err := client.CreateManualKnowledge(context.Background(), "", ManualKnowledgeInput{
		Title: "transcript/video-1/000000", Content: "原文", Status: "publish", Channel: "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "knowledge-1" || result.KnowledgeBaseID != "kb-1" {
		t.Fatalf("result = %#v", result)
	}
	if got.Title != "transcript/video-1/000000" || got.Content != "原文" || got.Status != "publish" || got.Channel != "api" {
		t.Fatalf("request = %#v", got)
	}
}

func TestCreateManualKnowledgeRejectsEmptyResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
	_, err := client.CreateManualKnowledge(context.Background(), "", ManualKnowledgeInput{Title: "title", Content: "content"})
	if err == nil {
		t.Fatal("expected empty knowledge id error")
	}
}

func TestGetKnowledgeUsesMetadataContentWhenTopLevelContentIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/knowledge/knowledge-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"knowledge-1","knowledge_base_id":"kb-1","content":"","metadata":{"content":"来自元数据的正文"},"parse_status":"completed"}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
	result, err := client.GetKnowledge(context.Background(), "knowledge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "来自元数据的正文" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestGetKnowledgePrefersTopLevelContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"knowledge-1","content":"顶层正文","metadata":{"content":"元数据正文"},"parse_status":"completed"}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL})
	result, err := client.GetKnowledge(context.Background(), "knowledge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "顶层正文" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestHybridSearchScopesToKnowledgeAndRequiresSuccessfulResponse(t *testing.T) {
	client := New(config.WeKnoraConfig{BaseURL: "http://weknora.test", KBID: "kb-1"})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/knowledge-bases/kb-1/hybrid-search" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request SearchParams
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.QueryText != "视频定位信息" || len(request.KnowledgeIDs) != 1 || request.KnowledgeIDs[0] != "knowledge-1" {
			t.Fatalf("search request = %#v", request)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":[{"id":"chunk-1","knowledge_id":"knowledge-1","content":"原文"}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	searchable, err := client.IsKnowledgeSearchable(context.Background(), "", "knowledge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !searchable {
		t.Fatal("knowledge should be searchable")
	}
}

func TestListKnowledgeChunksReadsAllPagesAndPreservesChunkFields(t *testing.T) {
	client := New(config.WeKnoraConfig{BaseURL: "http://weknora.test", APIKey: "secret", TenantID: "tenant-1"})
	pageRequests := 0
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/chunks/knowledge-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "secret" || r.Header.Get("X-Tenant-ID") != "tenant-1" {
			t.Fatalf("headers missing: %#v", r.Header)
		}
		pageRequests++
		if pageRequests == 1 && r.URL.Query().Get("page") != "1" {
			t.Fatalf("first page = %q", r.URL.Query().Get("page"))
		}
		if pageRequests == 2 && r.URL.Query().Get("page") != "2" {
			t.Fatalf("second page = %q", r.URL.Query().Get("page"))
		}
		pageChunks := make([]KnowledgeChunk, 1)
		pageChunks[0] = KnowledgeChunk{ID: "chunk-1", KnowledgeID: "knowledge-1", Content: "a", ChunkIndex: 0, ChunkType: "text"}
		if pageRequests == 1 {
			pageChunks = make([]KnowledgeChunk, 100)
			for index := range pageChunks {
				pageChunks[index] = KnowledgeChunk{ID: "chunk-1", KnowledgeID: "knowledge-1", Content: "a", ChunkIndex: index, ChunkType: "text"}
			}
		} else {
			pageChunks[0] = KnowledgeChunk{ID: "chunk-2", KnowledgeID: "knowledge-1", Content: "b", ChunkIndex: 100, ChunkType: "text"}
		}
		data, err := json.Marshal(pageChunks)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":` + string(data) + `,"total":101}`)),
			Header:     make(http.Header),
		}, nil
	})}
	chunks, err := client.ListKnowledgeChunks(context.Background(), "knowledge-1")
	if err != nil {
		t.Fatal(err)
	}
	if pageRequests != 2 || len(chunks) != 101 || chunks[100].Content != "b" {
		t.Fatalf("requests=%d chunks=%+v", pageRequests, chunks)
	}
}

func TestJoinKnowledgeChunksRemovesSplitterOverlap(t *testing.T) {
	chunks := []KnowledgeChunk{
		{ChunkIndex: 0, Content: "prefix abc"},
		{ChunkIndex: 1, Content: "abc suffix"},
	}
	got, err := JoinKnowledgeChunks(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if got != "prefix abc suffix" {
		t.Fatalf("joined content = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGetEntityGraphReadsOfficialGraphEndpoint(t *testing.T) {
	client := New(config.WeKnoraConfig{BaseURL: "http://weknora.test", APIKey: "secret", TenantID: "tenant-1", KBID: "kb-1"})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/knowledgebase/kb-1/graph" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("attributes") != "concept,entity" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-API-Key") != "secret" || r.Header.Get("X-Tenant-ID") != "tenant-1" {
			t.Fatalf("headers missing: %#v", r.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"nodes":[{"name":"概念","knowledge_id":"knowledge-1"}],"edges":[],"meta":{"total":1,"returned":1}}}`)),
			Header:     make(http.Header),
		}, nil
	})}
	graph, err := client.GetEntityGraph(context.Background(), "", 20, []string{"concept", "entity"})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || graph.Nodes[0].KnowledgeID != "knowledge-1" {
		t.Fatalf("graph = %#v", graph)
	}
}
