package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func newHTTPTestClient(server *httptest.Server) *client {
	return &client{
		baseURL:      server.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		operatorID:   "operator-union-id",
		httpClient:   server.Client(),
		limiter:      rate.NewLimiter(rate.Inf, 1),
		sleep: func(context.Context, time.Duration) error {
			return nil
		},
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}
}

func TestClientOfficialTokenReuseAndExpiry(t *testing.T) {
	tokenCalls := 0
	workspaceCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			tokenCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode token request: %v", err)
				return
			}
			if body["appKey"] != "app-key" || body["appSecret"] != "app-secret" {
				t.Errorf("token request body = %#v", body)
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": fmt.Sprintf("token-%d", tokenCalls), "expireIn": 7200})
		case "/v2.0/wiki/workspaces":
			workspaceCalls++
			if got := r.Header.Get("x-acs-dingtalk-access-token"); got != fmt.Sprintf("token-%d", tokenCalls) {
				t.Errorf("access token header = %q", got)
			}
			if r.URL.Query().Get("operatorId") != "operator-union-id" || r.URL.Query().Get("maxResults") != "30" {
				t.Errorf("workspace query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspaces": []interface{}{map[string]interface{}{"workspaceId": "w1", "rootNodeId": "root", "name": "Space"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newHTTPTestClient(server)

	for i := 0; i < 2; i++ {
		spaces, err := client.ListWorkspaces(context.Background())
		if err != nil {
			t.Fatalf("ListWorkspaces() error = %v", err)
		}
		if len(spaces) != 1 || spaces[0].WorkspaceID != "w1" {
			t.Fatalf("spaces = %#v", spaces)
		}
	}
	if tokenCalls != 1 || workspaceCalls != 2 {
		t.Fatalf("calls token=%d workspace=%d, want 1 and 2", tokenCalls, workspaceCalls)
	}

	client.tokenMu.Lock()
	client.tokenExpAt = time.Now().Add(-time.Second)
	client.tokenMu.Unlock()
	if _, err := client.ListWorkspaces(context.Background()); err != nil {
		t.Fatalf("ListWorkspaces() after expiry error = %v", err)
	}
	if tokenCalls != 2 {
		t.Fatalf("token calls after forced expiry = %d, want 2", tokenCalls)
	}
}

func TestClientRefreshesTokenOnceOnUnauthorized(t *testing.T) {
	tokenCalls := 0
	apiCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			tokenCalls++
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": fmt.Sprintf("token-%d", tokenCalls), "expireIn": 7200})
			return
		}
		apiCalls++
		if r.Header.Get("x-acs-dingtalk-access-token") == "token-1" {
			writeJSON(t, w, http.StatusUnauthorized, map[string]interface{}{"code": "InvalidToken", "message": "expired"})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspaces": []interface{}{}})
	}))
	defer server.Close()
	client := newHTTPTestClient(server)

	if _, err := client.ListWorkspaces(context.Background()); err != nil {
		t.Fatalf("ListWorkspaces() error = %v", err)
	}
	if tokenCalls != 2 || apiCalls != 2 {
		t.Fatalf("calls token=%d api=%d, want 2 and 2", tokenCalls, apiCalls)
	}
}

func TestClientForbiddenIsInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		writeJSON(t, w, http.StatusForbidden, map[string]interface{}{"code": "Forbidden", "message": "permission missing"})
	}))
	defer server.Close()
	client := newHTTPTestClient(server)
	_, err := client.ListWorkspaces(context.Background())
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("ListWorkspaces() error = %v, want invalid credentials", err)
	}
}

func TestClientRetries429And5xx(t *testing.T) {
	for _, tc := range []struct {
		name       string
		firstCode  int
		retryAfter string
		wantWait   time.Duration
	}{
		{name: "rate limited", firstCode: http.StatusTooManyRequests, retryAfter: "0", wantWait: 100 * time.Millisecond},
		{name: "server error", firstCode: http.StatusBadGateway, wantWait: 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiCalls := 0
			var waits []time.Duration
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1.0/oauth2/accessToken" {
					writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
					return
				}
				apiCalls++
				if apiCalls == 1 {
					if tc.retryAfter != "" {
						w.Header().Set("Retry-After", tc.retryAfter)
					}
					writeJSON(t, w, tc.firstCode, map[string]interface{}{"code": "temporary", "message": "retry"})
					return
				}
				writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspaces": []interface{}{}})
			}))
			defer server.Close()
			client := newHTTPTestClient(server)
			client.sleep = func(_ context.Context, wait time.Duration) error {
				waits = append(waits, wait)
				return nil
			}
			if _, err := client.ListWorkspaces(context.Background()); err != nil {
				t.Fatalf("ListWorkspaces() error = %v", err)
			}
			if apiCalls != 2 || len(waits) != 1 || waits[0] != tc.wantWait {
				t.Fatalf("apiCalls=%d waits=%v, want 2 and [%s]", apiCalls, waits, tc.wantWait)
			}
		})
	}
}

func TestClientWorkspaceAndNodePagination(t *testing.T) {
	workspacePages := 0
	nodePages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		switch r.URL.Path {
		case "/v2.0/wiki/workspaces":
			workspacePages++
			if workspacePages == 1 {
				writeJSON(t, w, http.StatusOK, map[string]interface{}{"nextToken": "next-w", "workspaces": []interface{}{map[string]interface{}{"workspaceId": "w1"}}})
				return
			}
			if r.URL.Query().Get("nextToken") != "next-w" {
				t.Errorf("workspace nextToken = %q", r.URL.Query().Get("nextToken"))
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspaces": []interface{}{map[string]interface{}{"workspaceId": "w2"}}})
		case "/v2.0/wiki/nodes":
			nodePages++
			if r.URL.Query().Get("parentNodeId") != "parent" {
				t.Errorf("parentNodeId = %q", r.URL.Query().Get("parentNodeId"))
			}
			if nodePages == 1 {
				writeJSON(t, w, http.StatusOK, map[string]interface{}{"nextToken": "next-n", "nodes": []interface{}{map[string]interface{}{"nodeId": "n1", "type": "FILE", "category": "ALIDOC"}}})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"nodes": []interface{}{map[string]interface{}{"nodeId": "n2", "type": "FOLDER"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newHTTPTestClient(server)
	spaces, err := client.ListWorkspaces(context.Background())
	if err != nil || len(spaces) != 2 {
		t.Fatalf("ListWorkspaces() spaces=%#v err=%v", spaces, err)
	}
	nodes, err := client.ListNodes(context.Background(), "parent")
	if err != nil || len(nodes) != 2 {
		t.Fatalf("ListNodes() nodes=%#v err=%v", nodes, err)
	}
	for _, node := range nodes {
		if node.ParentNodeID != "parent" {
			t.Errorf("node %s ParentNodeID = %q", node.NodeID, node.ParentNodeID)
		}
	}
}

func TestClientRejectsRepeatedPaginationToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]interface{}{"nextToken": "loop", "workspaces": []interface{}{}})
	}))
	defer server.Close()
	client := newHTTPTestClient(server)
	_, err := client.ListWorkspaces(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("ListWorkspaces() error = %v, want repeated token", err)
	}
}

func TestClientDocumentBlockIndexPagination(t *testing.T) {
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		if r.URL.Path != "/v1.0/doc/suites/documents/node%2Fone/blocks" && r.URL.Path != "/v1.0/doc/suites/documents/node/one/blocks" {
			t.Errorf("block path = %q", r.URL.Path)
		}
		ranges = append(ranges, r.URL.Query().Get("startIndex")+":"+r.URL.Query().Get("endIndex"))
		count := blockPageSize
		if len(ranges) == 2 {
			count = 1
		}
		data := make([]interface{}, count)
		for i := range data {
			data[i] = map[string]interface{}{"blockId": fmt.Sprintf("b-%d-%d", len(ranges), i), "blockType": "paragraph", "paragraph": map[string]interface{}{"text": "x"}}
		}
		writeJSON(t, w, http.StatusOK, map[string]interface{}{"success": true, "result": map[string]interface{}{"data": data}})
	}))
	defer server.Close()
	client := newHTTPTestClient(server)
	blocks, err := client.GetDocumentBlocks(context.Background(), "node/one")
	if err != nil {
		t.Fatalf("GetDocumentBlocks() error = %v", err)
	}
	if len(blocks) != blockPageSize+1 {
		t.Fatalf("block count = %d, want %d", len(blocks), blockPageSize+1)
	}
	if len(ranges) != 2 || ranges[0] != "0:99" || ranges[1] != "100:199" {
		t.Fatalf("ranges = %v", ranges)
	}
}

func TestClientOfficialWorkspaceAndNodeWrappers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		if r.URL.Query().Get("operatorId") != "operator-union-id" {
			t.Errorf("operatorId = %q", r.URL.Query().Get("operatorId"))
		}
		switch r.URL.Path {
		case "/v2.0/wiki/workspaces/w/one":
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspace": map[string]interface{}{"workspaceId": "w/one", "rootNodeId": "root", "name": "Space"}})
		case "/v2.0/wiki/nodes/n/one":
			if r.URL.Query().Get("withStatisticalInfo") != "true" {
				t.Errorf("withStatisticalInfo = %q", r.URL.Query().Get("withStatisticalInfo"))
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"node": map[string]interface{}{"nodeId": "n/one", "workspaceId": "w/one", "type": "FILE", "category": "ALIDOC", "extension": "adoc", "statisticalInfo": map[string]interface{}{"wordCount": 42}}})
		case "/v2.0/wiki/workspaces":
			if r.URL.Query().Get("maxResults") != "1" {
				t.Errorf("Ping maxResults = %q", r.URL.Query().Get("maxResults"))
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"workspaces": []interface{}{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newHTTPTestClient(server)

	space, err := client.GetWorkspace(context.Background(), "w/one")
	if err != nil || space.WorkspaceID != "w/one" || space.RootNodeID != "root" {
		t.Fatalf("GetWorkspace() space=%#v err=%v", space, err)
	}
	node, err := client.GetNode(context.Background(), "n/one")
	if err != nil || node.NodeID != "n/one" || node.StatisticalInfo.WordCount != 42 {
		t.Fatalf("GetNode() node=%#v err=%v", node, err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestClientDocumentBlocksSuccessFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(t, w, http.StatusOK, map[string]interface{}{"accessToken": "token", "expireIn": 7200})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]interface{}{"success": false, "result": map[string]interface{}{"data": []interface{}{}}})
	}))
	defer server.Close()
	client := newHTTPTestClient(server)
	_, err := client.GetDocumentBlocks(context.Background(), "doc")
	if err == nil || !strings.Contains(err.Error(), "success=false") {
		t.Fatalf("GetDocumentBlocks() error = %v", err)
	}
}

func TestClientResponseLimitAndCancellation(t *testing.T) {
	t.Run("body limit", func(t *testing.T) {
		_, err := readLimitedBody(io.LimitReader(strings.NewReader(strings.Repeat("x", maxResponseBytes+1)), maxResponseBytes+1))
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("readLimitedBody() error = %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		client := &client{
			token:      "token",
			tokenExpAt: time.Now().Add(time.Hour),
			limiter:    rate.NewLimiter(rate.Inf, 1),
			httpClient: http.DefaultClient,
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var result map[string]interface{}
		err := client.doRequest(ctx, http.MethodGet, "http://example.invalid", &result)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("doRequest() error = %v, want context canceled", err)
		}
	})
}

func TestParseRetryAfterCapsServerDelay(t *testing.T) {
	if got := parseRetryAfter("3600", time.Second); got != maxRetryAfter {
		t.Fatalf("parseRetryAfter() = %s, want %s", got, maxRetryAfter)
	}
}
