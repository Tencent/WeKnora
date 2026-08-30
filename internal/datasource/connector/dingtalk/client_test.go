package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testClient(server *httptest.Server) *client {
	return &client{
		baseURL:   server.URL,
		operator:  "union/user",
		appKey:    "app",
		appSecret: "secret",
		http:      server.Client(),
		sleep:     func(context.Context, time.Duration) error { return nil },
	}
}

func TestClientUsesOfficialEndpointsAndPaginates(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"appKey":"app"`) ||
				!strings.Contains(string(body), `"appSecret":"secret"`) {
				t.Errorf("token request body = %s", body)
			}
			_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
		case "/v2.0/wiki/workspaces":
			if r.Header.Get("x-acs-dingtalk-access-token") != "token" ||
				r.URL.Query().Get("operatorId") != "union/user" ||
				r.URL.Query().Get("maxResults") != "30" {
				t.Errorf("workspace request = %#v, headers = %#v", r.URL.Query(), r.Header)
			}
			if r.URL.Query().Get("nextToken") == "" {
				_, _ = w.Write([]byte(`{
					"workspaces":[{"workspaceId":"a","rootNodeId":"root-a","name":"A"}],
					"nextToken":"page 2"
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"workspaces":[{"workspaceId":"b","rootNodeId":"root-b","name":"B"}]
				}`))
			}
		case "/v2.0/wiki/nodes":
			if r.URL.Query().Get("parentNodeId") != "root/a" ||
				r.URL.Query().Get("maxResults") != "50" {
				t.Errorf("node request query = %#v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"doc","type":"FILE","category":"ALIDOC","extension":"adoc"}]}`))
		case "/v1.0/doc/suites/documents/doc/key/blocks":
			if !strings.Contains(r.URL.EscapedPath(), "doc%2Fkey") {
				t.Errorf("document key was not path-escaped: %s", r.URL.EscapedPath())
			}
			if r.URL.Query().Get("startIndex") != "0" ||
				r.URL.Query().Get("endIndex") != "99" {
				t.Errorf("block request query = %#v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"data":[{"blockType":"paragraph"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testClient(server)
	workspaces, err := client.listWorkspaces(context.Background())
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("listWorkspaces() = %#v, %v", workspaces, err)
	}
	nodes, err := client.listNodes(context.Background(), "root/a")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("listNodes() = %#v, %v", nodes, err)
	}
	blocks, err := client.documentBlocks(context.Background(), "doc/key")
	if err != nil || len(blocks) != 1 {
		t.Fatalf("documentBlocks() = %#v, %v", blocks, err)
	}

	tokenRequests := 0
	for _, request := range requests {
		if request == "POST /v1.0/oauth2/accessToken" {
			tokenRequests++
		}
	}
	if tokenRequests != 1 {
		t.Fatalf("access token requested %d times, requests = %#v", tokenRequests, requests)
	}
}

func TestClientRefreshesUnauthorizedTokenAndRetriesRateLimit(t *testing.T) {
	tokenRequests, workspaceRequests, nodeRequests, waits := 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			tokenRequests++
			_, _ = w.Write([]byte(`{"accessToken":"token-` + string(rune('0'+tokenRequests)) + `","expireIn":7200}`))
		case "/v2.0/wiki/workspaces":
			workspaceRequests++
			if workspaceRequests == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"InvalidToken","message":"expired"}`))
				return
			}
			if r.Header.Get("x-acs-dingtalk-access-token") != "token-2" {
				t.Errorf("refreshed token header = %q", r.Header.Get("x-acs-dingtalk-access-token"))
			}
			_, _ = w.Write([]byte(`{"workspaces":[]}`))
		case "/v2.0/wiki/nodes":
			nodeRequests++
			if nodeRequests == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":"TooManyRequests"}`))
				return
			}
			_, _ = w.Write([]byte(`{"nodes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testClient(server)
	client.sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	if _, err := client.listWorkspaces(context.Background()); err != nil {
		t.Fatalf("listWorkspaces() error = %v", err)
	}
	if _, err := client.listNodes(context.Background(), "root"); err != nil {
		t.Fatalf("listNodes() error = %v", err)
	}
	if tokenRequests != 2 || workspaceRequests != 2 || nodeRequests != 2 || waits != 1 {
		t.Fatalf(
			"requests token=%d workspace=%d nodes=%d waits=%d",
			tokenRequests, workspaceRequests, nodeRequests, waits,
		)
	}
}

func TestDecodeAPIErrorDoesNotExposeUnstructuredBody(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"unexpected": "credential=secret"})
	err := decodeAPIError(http.StatusBadRequest, body)
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("decodeAPIError() leaked response body: %v", err)
	}
}

func TestClientDoesNotExposeQueryValuesInTransportErrors(t *testing.T) {
	client := &client{
		baseURL: "https://api.dingtalk.com",
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		})},
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	err := client.doJSON(
		context.Background(),
		http.MethodGet,
		"/v2.0/wiki/nodes?operatorId=sensitive-union-id",
		nil,
		false,
		nil,
	)
	if err == nil || strings.Contains(err.Error(), "sensitive-union-id") {
		t.Fatalf("doJSON() error = %v", err)
	}
}

func TestClientRejectsRepeatedPaginationTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2.0/wiki/workspaces":
			_, _ = w.Write([]byte(`{"workspaces":[],"nextToken":"same"}`))
		case "/v2.0/wiki/nodes":
			_, _ = w.Write([]byte(`{"nodes":[],"nextToken":"same"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testClient(server)
	client.token = "cached"
	client.tokenExpiry = time.Now().Add(time.Hour)

	if _, err := client.listWorkspaces(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("listWorkspaces() error = %v", err)
	}
	if _, err := client.listNodes(context.Background(), "root"); err == nil ||
		!strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("listNodes() error = %v", err)
	}
}

func TestClientRedactsCredentialsFromStructuredAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{
			"code":"Forbidden",
			"message":"app-key app-secret operator-union cached-token"
		}`))
	}))
	defer server.Close()

	client := testClient(server)
	client.appKey = "app-key"
	client.appSecret = "app-secret"
	client.operator = "operator-union"
	client.token = "cached-token"
	client.tokenExpiry = time.Now().Add(time.Hour)

	err := client.doJSON(context.Background(), http.MethodGet, "/forbidden", nil, true, nil)
	if err == nil {
		t.Fatal("doJSON() error = nil")
	}
	for _, sensitive := range []string{
		client.appKey, client.appSecret, client.operator, client.token,
	} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("doJSON() leaked %q: %v", sensitive, err)
		}
	}
}

func TestReadBodyRejectsOversizedResponses(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", maxResponseBytes+1))
	if _, err := readBody(body); err == nil ||
		!strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("readBody() error = %v", err)
	}
}
