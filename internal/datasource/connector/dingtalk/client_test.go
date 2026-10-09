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

func TestDocumentBlocksRejectsIncompleteResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"success":false}`, `{"success":true}`, `{"success":true,"result":null}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := testClient(server)
			c.token = "cached"
			c.tokenExpiry = time.Now().Add(time.Hour)
			blocks, err := c.documentBlocks(context.Background(), "doc")
			if err == nil || len(blocks) != 0 {
				t.Fatalf("invalid response accepted: %#v, %v", blocks, err)
			}
		})
	}
}

func TestDocumentBlocksReadsMultiplePagesAndAcceptsEmptyDocument(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var blocks []json.RawMessage
		if r.URL.Query().Get("startIndex") == "0" && strings.Contains(r.URL.Path, "/long/") {
			for i := 0; i < 100; i++ {
				blocks = append(blocks, json.RawMessage(`{"blockType":"paragraph","paragraph":{"text":"first page"}}`))
			}
		} else if strings.Contains(r.URL.Path, "/long/") {
			if r.URL.Query().Get("startIndex") != "100" {
				t.Errorf("unexpected page: %s", r.URL.RawQuery)
			}
			blocks = []json.RawMessage{json.RawMessage(`{"blockType":"paragraph","paragraph":{"text":"last page"}}`)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"data": blocks}})
	}))
	defer server.Close()
	c := testClient(server)
	c.token = "cached"
	c.tokenExpiry = time.Now().Add(time.Hour)
	blocks, err := c.documentBlocks(context.Background(), "long")
	if err != nil || len(blocks) != 101 || calls != 2 {
		t.Fatalf("long document: %d blocks, %d calls, %v", len(blocks), calls, err)
	}
	blocks, err = c.documentBlocks(context.Background(), "empty")
	if err != nil || len(blocks) != 0 {
		t.Fatalf("empty document: %#v, %v", blocks, err)
	}
}

// A node is readable by its own id. That endpoint is what makes a personal-space
// selection possible: such a workspace never appears in the workspace list, so
// the node cannot be found by walking a tree.
func TestClientReadsNodeByItsOwnID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.EscapedPath() == "/v2.0/wiki/nodes/personal%2Fbook":
			if r.Method != http.MethodGet {
				t.Errorf("node request method = %s", r.Method)
			}
			if r.URL.Query().Get("operatorId") != "union/user" ||
				r.Header.Get("x-acs-dingtalk-access-token") != "token" {
				t.Errorf("node request query = %#v, headers = %#v", r.URL.Query(), r.Header)
			}
			_, _ = w.Write([]byte(`{"node":{
				"nodeId":"personal/book","workspaceId":"personal-space","name":"Weekly.axls",
				"type":"FILE","category":"ALIDOC","extension":"axls",
				"modifiedTime":"2026-01-04T14:27Z","modifiedTimestamp":1767508041000,
				"url":"https://alidocs.dingtalk.com/i/nodes/personal/book?utm_scene=person_space"
			}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"nodeNotExist","message":"Data not found"}`))
		}
	}))
	defer server.Close()

	c := testClient(server)
	c.token = "token"
	c.tokenExpiry = time.Now().Add(time.Hour)

	item, err := c.getNode(context.Background(), "personal/book")
	if err != nil {
		t.Fatalf("getNode() error = %v", err)
	}
	if item.ID != "personal/book" || item.WorkspaceID != "personal-space" ||
		item.Extension != "axls" || !item.modifiedAt().Equal(time.UnixMilli(1767508041000)) {
		t.Fatalf("getNode() = %#v", item)
	}

	if _, err := c.getNode(context.Background(), "missing"); err == nil ||
		!strings.Contains(err.Error(), "nodeNotExist") {
		t.Fatalf("getNode(missing) error = %v, want the provider refusal", err)
	}
}

// A Base is read through the notable API. Two facts that cost real time are
// pinned here: records/list is POST-only and takes maxResults in the body, and
// every call carries the operator's unionId. A transient failure on the same
// endpoint must be retried, not surfaced as a broken Base.
func TestClientReadsNotableBaseThroughOfficialEndpoints(t *testing.T) {
	var (
		mu             sync.Mutex
		requests       []string
		recordBodies   []string
		recordAttempts int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("operatorId") != "union/user" {
			t.Errorf("notable request query = %#v", r.URL.Query())
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/v1.0/notable/bases/base-1/sheets":
			_, _ = w.Write([]byte(`{"value":[{"name":"清单","id":"tbl-1"}]}`))
		case "/v1.0/notable/bases/base-1/sheets/tbl-1/fields":
			_, _ = w.Write([]byte(`{"value":[
				{"name":"名称","id":"f1","type":"text"},
				{"name":"开始日期","id":"f2","type":"date"}
			]}`))
		case "/v1.0/notable/bases/base-1/sheets/tbl-1/records/list":
			recordAttempts++
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			recordBodies = append(recordBodies, string(body))
			mu.Unlock()
			if r.Method != http.MethodPost {
				t.Errorf("records/list method = %s, want POST", r.Method)
			}
			// Both pagination values belong to the body; the endpoint ignores
			// them in the query string.
			if r.URL.Query().Get("maxResults") != "" || r.URL.Query().Get("nextToken") != "" {
				t.Errorf("pagination leaked into the query: %#v", r.URL.Query())
			}
			if recordAttempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"ServiceUnavailable","message":"try later"}`))
				return
			}
			_, _ = w.Write([]byte(`{"records":[{"id":"r1","fields":{"名称":"第一条"}}],
				"hasMore":false,"nextToken":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := testClient(server)
	c.token = "cached"
	c.tokenExpiry = time.Now().Add(time.Hour)

	tables, err := c.listNotableTables(context.Background(), "base-1")
	if err != nil || len(tables) != 1 || tables[0].ID != "tbl-1" || tables[0].Name != "清单" {
		t.Fatalf("listNotableTables() = %#v, %v", tables, err)
	}
	fields, err := c.listNotableFields(context.Background(), "base-1", "tbl-1")
	if err != nil || len(fields) != 2 || fields[0].Name != "名称" || fields[1].Type != "date" {
		t.Fatalf("listNotableFields() = %#v, %v", fields, err)
	}

	page, err := c.listNotableRecords(context.Background(), "base-1", "tbl-1", "")
	if err != nil {
		t.Fatalf("listNotableRecords() error = %v", err)
	}
	if recordAttempts != 2 {
		t.Fatalf("records/list attempts = %d, want a retry after the transient failure", recordAttempts)
	}
	if len(page.Records) != 1 || page.Records[0].Fields["名称"] != "第一条" || page.HasMore {
		t.Fatalf("listNotableRecords() = %#v", page)
	}
	if !strings.Contains(recordBodies[1], `"maxResults":100`) {
		t.Fatalf("records/list body = %s, want maxResults in the body", recordBodies[1])
	}

	if _, err := c.listNotableRecords(context.Background(), "base-1", "tbl-1", "next-token"); err != nil {
		t.Fatalf("listNotableRecords(next) error = %v", err)
	}
	if !strings.Contains(recordBodies[2], `"nextToken":"next-token"`) {
		t.Fatalf("paged records/list body = %s, want the token in the body", recordBodies[2])
	}

	wantRequests := []string{
		"GET /v1.0/notable/bases/base-1/sheets",
		"GET /v1.0/notable/bases/base-1/sheets/tbl-1/fields",
		"POST /v1.0/notable/bases/base-1/sheets/tbl-1/records/list",
		"POST /v1.0/notable/bases/base-1/sheets/tbl-1/records/list",
		"POST /v1.0/notable/bases/base-1/sheets/tbl-1/records/list",
	}
	if strings.Join(requests, " ") != strings.Join(wantRequests, " ") {
		t.Fatalf("notable endpoints called with %#v, want %#v", requests, wantRequests)
	}
}
