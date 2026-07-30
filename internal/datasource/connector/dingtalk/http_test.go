package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/time/rate"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func testHTTPClient(server *httptest.Server) *httpClient {
	client := newHTTPClient(&Config{
		ClientID: "client-id", ClientSecret: "client-secret",
		OperatorID: "operator-id", BaseURL: server.URL,
	})
	client.transport = server.Client()
	client.limiter = rate.NewLimiter(rate.Inf, 1)
	client.sleep = func(context.Context, time.Duration) error { return nil }
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientCachesTokenAndPaginates(t *testing.T) {
	var tokenCalls atomic.Int32
	var workspacePages atomic.Int32
	var nodePages atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["appKey"] != "client-id" || body["appSecret"] != "client-secret" {
			t.Fatalf("token request = %#v", body)
		}
		writeJSON(t, w, accessTokenResponse{AccessToken: "token-1", ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(accessTokenHeader) != "token-1" {
			t.Fatalf("access token header = %q", r.Header.Get(accessTokenHeader))
		}
		if r.URL.Query().Get("operatorId") != "operator-id" ||
			r.URL.Query().Get("maxResults") != strconv.Itoa(workspacePageSize) {
			t.Fatalf("workspace query = %s", r.URL.RawQuery)
		}
		page := workspacePages.Add(1)
		if page == 1 {
			writeJSON(t, w, workspaceListResponse{
				Workspaces: []workspace{{WorkspaceID: "w1"}},
				NextToken:  "next",
			})
			return
		}
		if r.URL.Query().Get("nextToken") != "next" {
			t.Fatalf("nextToken = %q", r.URL.Query().Get("nextToken"))
		}
		writeJSON(t, w, workspaceListResponse{Workspaces: []workspace{{WorkspaceID: "w2"}}})
	})
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parentNodeId") != "root" ||
			r.URL.Query().Get("maxResults") != strconv.Itoa(nodePageSize) {
			t.Fatalf("node query = %s", r.URL.RawQuery)
		}
		page := nodePages.Add(1)
		if page == 1 {
			writeJSON(t, w, nodeListResponse{
				Nodes:     []node{{NodeID: "n1"}},
				NextToken: "nodes-next",
			})
			return
		}
		writeJSON(t, w, nodeListResponse{Nodes: []node{{NodeID: "n2"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := testHTTPClient(server)
	workspaces, err := client.listWorkspaces(context.Background())
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("listWorkspaces() = %#v, %v", workspaces, err)
	}
	nodes, err := client.listChildren(context.Background(), "root")
	if err != nil || len(nodes) != 2 || nodes[0].ParentNodeID != "root" {
		t.Fatalf("listChildren() = %#v, %v", nodes, err)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls.Load())
	}
}

func TestHTTPClientRefreshesTokenOnceAfterUnauthorized(t *testing.T) {
	var tokenCalls atomic.Int32
	var apiCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		n := tokenCalls.Add(1)
		writeJSON(t, w, accessTokenResponse{AccessToken: fmt.Sprintf("token-%d", n), ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		n := apiCalls.Add(1)
		if n == 1 {
			if r.Header.Get(accessTokenHeader) != "token-1" {
				t.Fatalf("first token = %q", r.Header.Get(accessTokenHeader))
			}
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]string{"code": "InvalidAuthentication"})
			return
		}
		if r.Header.Get(accessTokenHeader) != "token-2" {
			t.Fatalf("refreshed token = %q", r.Header.Get(accessTokenHeader))
		}
		writeJSON(t, w, workspaceListResponse{})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	if _, err := testHTTPClient(server).listWorkspaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 2 || apiCalls.Load() != 2 {
		t.Fatalf("token calls=%d api calls=%d", tokenCalls.Load(), apiCalls.Load())
	}
}

func TestHTTPClientRetriesTransientResponses(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, accessTokenResponse{AccessToken: "token", ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			writeJSON(t, w, workspaceListResponse{})
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	if _, err := testHTTPClient(server).listWorkspaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("API calls = %d, want 3", calls.Load())
	}
}

func TestHTTPClientPaginatesDocumentBlocks(t *testing.T) {
	var pageCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, accessTokenResponse{AccessToken: "token", ExpireIn: 7200})
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-1/blocks", func(w http.ResponseWriter, r *http.Request) {
		call := pageCalls.Add(1)
		start := r.URL.Query().Get("startIndex")
		if start != strconv.Itoa((int(call)-1)*blockPageSize) {
			t.Fatalf("startIndex = %q", start)
		}
		count := blockPageSize
		if call == 2 {
			count = 1
		}
		data := make([]json.RawMessage, count)
		for i := range data {
			data[i] = json.RawMessage(`{"blockType":"paragraph"}`)
		}
		response := documentBlocksResponse{Success: true}
		response.Result.Data = data
		writeJSON(t, w, response)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	blocks, err := testHTTPClient(server).listDocumentBlocks(context.Background(), "doc-1")
	if err != nil || len(blocks) != blockPageSize+1 {
		t.Fatalf("listDocumentBlocks() count=%d err=%v", len(blocks), err)
	}
}

func TestHTTPClientRejectsRepeatedPaginationToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, accessTokenResponse{AccessToken: "token", ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, workspaceListResponse{NextToken: "same-token"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	_, err := testHTTPClient(server).listWorkspaces(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("listWorkspaces() error = %v", err)
	}
}

func TestHTTPClientRejectsOversizedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, accessTokenResponse{AccessToken: "token", ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes+1))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	_, err := testHTTPClient(server).listWorkspaces(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("listWorkspaces() error = %v", err)
	}
}

func TestAPIErrorDoesNotIncludeUnknownResponseFields(t *testing.T) {
	err := decodeAPIError(http.StatusForbidden,
		[]byte(`{"code":"permissionDenied","message":"denied","requestId":"req-1","secret":"do-not-leak"}`))
	text := err.Error()
	if !strings.Contains(text, "permissionDenied") || !strings.Contains(text, "req-1") {
		t.Fatalf("safe fields missing: %s", text)
	}
	if strings.Contains(text, "do-not-leak") {
		t.Fatalf("unknown response field leaked: %s", text)
	}
}
