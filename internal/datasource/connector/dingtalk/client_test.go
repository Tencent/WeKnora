package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// The whole uploaded-file chain, as verified against the live tenant:
// node UUID → numeric 钉盘 ids → pre-signed URL → raw bytes.
func TestClientDownloadsUploadedDocumentThroughStorageAPI(t *testing.T) {
	const payload = "PK\x03\x04word-bytes"
	var dentryQueries, downloadQueries, objectFetches int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
		case strings.HasSuffix(r.URL.Path, "/queryDentryId"):
			dentryQueries++
			if r.Method != http.MethodGet ||
				r.URL.EscapedPath() != "/v2.0/doc/dentries/node%2Fid/queryDentryId" {
				t.Errorf("dentry request = %s %s", r.Method, r.URL.EscapedPath())
			}
			if r.URL.Query().Get("operatorId") != "union/user" ||
				r.Header.Get("x-acs-dingtalk-access-token") != "token" {
				t.Errorf("dentry request query = %#v, headers = %#v", r.URL.Query(), r.Header)
			}
			_, _ = w.Write([]byte(
				`{"dentryId":"123456789012","spaceId":"9876543210","dentryUuid":"node/id"}`,
			))
		case strings.HasSuffix(r.URL.Path, "/downloadInfos/query"):
			downloadQueries++
			if r.Method != http.MethodPost ||
				r.URL.Path != "/v1.0/storage/spaces/9876543210/dentries/123456789012/downloadInfos/query" {
				t.Errorf("download request = %s %s", r.Method, r.URL.Path)
			}
			if r.URL.Query().Get("unionId") != "union/user" {
				t.Errorf("download request query = %#v", r.URL.Query())
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"preferIntranet":false`) ||
				!strings.Contains(string(body), `"version":1`) {
				t.Errorf("download request body = %s", body)
			}
			_, _ = w.Write([]byte(`{
				"protocol":"HEADER_SIGNATURE",
				"headerSignatureInfo":{
					"headers":{"Authorization":"OSS key:signature","x-oss-date":"Sun, 27 Sep 2026 03:34:30 GMT"},
					"resourceUrls":["http://` + r.Host + `/oss/object"],
					"expirationSeconds":900
				}
			}`))
		case r.URL.Path == "/oss/object":
			objectFetches++
			if r.Header.Get("Authorization") != "OSS key:signature" ||
				r.Header.Get("x-oss-date") != "Sun, 27 Sep 2026 03:34:30 GMT" {
				t.Errorf("signed request headers = %#v", r.Header)
			}
			if r.Header.Get("Accept-Encoding") != "identity" {
				t.Errorf("signed request encoding = %q", r.Header.Get("Accept-Encoding"))
			}
			_, _ = w.Write([]byte(payload))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testClient(server)
	data, err := client.downloadDocument(context.Background(), "node/id")
	if err != nil {
		t.Fatalf("downloadDocument() error = %v", err)
	}
	if string(data) != payload {
		t.Fatalf("downloadDocument() = %q, want %q", data, payload)
	}
	if dentryQueries != 1 || downloadQueries != 1 || objectFetches != 1 {
		t.Fatalf("requests dentry=%d download=%d object=%d",
			dentryQueries, downloadQueries, objectFetches)
	}

	// Validation resolves the same location without transferring the payload.
	if err := client.verifyDocumentDownload(context.Background(), "node/id"); err != nil {
		t.Fatalf("verifyDocumentDownload() error = %v", err)
	}
	if downloadQueries != 2 || objectFetches != 1 {
		t.Fatalf("verify transferred bytes: download=%d object=%d", downloadQueries, objectFetches)
	}
}

func TestClientRejectsIncompleteDentryResponse(t *testing.T) {
	for _, body := range []string{
		`{"dentryId":"1"}`,
		`{"spaceId":"2"}`,
		`{"dentryId":" ","spaceId":"2"}`,
		`{}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := testClient(server)
			c.token = "cached"
			c.tokenExpiry = time.Now().Add(time.Hour)
			if _, err := c.resolveDentry(context.Background(), "doc"); err == nil {
				t.Fatalf("resolveDentry() accepted %s", body)
			}
		})
	}
}

func TestClientRejectsDownloadWithoutResourceURL(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":null}`,
		`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":{"resourceUrls":[]}}`,
		`{"protocol":"SIGNATURE","headerSignatureInfo":{"resourceUrls":[" "]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := testClient(server)
			c.token = "cached"
			c.tokenExpiry = time.Now().Add(time.Hour)
			_, err := c.requestDownloadURL(context.Background(), dentryRef{DentryID: "1", SpaceID: "2"})
			if err == nil {
				t.Fatalf("requestDownloadURL() accepted %s", body)
			}
		})
	}
}

// An expired pre-signed URL must be re-signed rather than failing the document.
func TestClientResignsDownloadURLAfterObjectStoreRefusal(t *testing.T) {
	var downloadQueries, objectFetches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/queryDentryId"):
			_, _ = w.Write([]byte(`{"dentryId":"1","spaceId":"2"}`))
		case strings.HasSuffix(r.URL.Path, "/downloadInfos/query"):
			downloadQueries++
			_, _ = w.Write([]byte(`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":{
				"headers":{"Authorization":"OSS key:sig` + string(rune('0'+downloadQueries)) + `"},
				"resourceUrls":["http://` + r.Host + `/oss/object"]}}`))
		case r.URL.Path == "/oss/object":
			objectFetches++
			if objectFetches == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code></Error>`))
				return
			}
			if r.Header.Get("Authorization") != "OSS key:sig2" {
				t.Errorf("retry reused the stale signature: %#v", r.Header)
			}
			_, _ = w.Write([]byte("bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := testClient(server)
	c.token = "cached"
	c.tokenExpiry = time.Now().Add(time.Hour)
	data, err := c.downloadDocument(context.Background(), "doc")
	if err != nil {
		t.Fatalf("downloadDocument() error = %v", err)
	}
	if string(data) != "bytes" || downloadQueries != 2 || objectFetches != 2 {
		t.Fatalf("data = %q, download=%d object=%d", data, downloadQueries, objectFetches)
	}
}

func TestClientBoundsDocumentDownloads(t *testing.T) {
	t.Run("declared length", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/queryDentryId"):
				_, _ = w.Write([]byte(`{"dentryId":"1","spaceId":"2"}`))
			case strings.HasSuffix(r.URL.Path, "/downloadInfos/query"):
				_, _ = w.Write([]byte(`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":{
					"resourceUrls":["http://` + r.Host + `/oss/object"]}}`))
			default:
				w.Header().Set("Content-Length", strconv.Itoa(4096))
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		c := testClient(server)
		c.token = "cached"
		c.tokenExpiry = time.Now().Add(time.Hour)
		c.downloadLimit = 1024
		data, err := c.downloadDocument(context.Background(), "doc")
		if err == nil || !strings.Contains(err.Error(), "exceeds the 1024 byte download limit") {
			t.Fatalf("downloadDocument() = %q, %v; want a size refusal", data, err)
		}
	})

	t.Run("streamed length", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/queryDentryId"):
				_, _ = w.Write([]byte(`{"dentryId":"1","spaceId":"2"}`))
			case strings.HasSuffix(r.URL.Path, "/downloadInfos/query"):
				_, _ = w.Write([]byte(`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":{
					"resourceUrls":["http://` + r.Host + `/oss/object"]}}`))
			default:
				// Flushing first drops Content-Length, so only the streaming
				// guard can stop an over-sized body.
				w.WriteHeader(http.StatusOK)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
			}
		}))
		defer server.Close()

		c := testClient(server)
		c.token = "cached"
		c.tokenExpiry = time.Now().Add(time.Hour)
		c.downloadLimit = 1024
		if _, err := c.downloadDocument(context.Background(), "doc"); err == nil ||
			!strings.Contains(err.Error(), "1024 byte download limit") {
			t.Fatalf("downloadDocument() error = %v, want a size refusal", err)
		}
	})
}

func TestReadLimitedRejectsOverlongBody(t *testing.T) {
	if data, err := readLimited(strings.NewReader("1234"), 4); err != nil || string(data) != "1234" {
		t.Fatalf("readLimited() = %q, %v", data, err)
	}
	if _, err := readLimited(strings.NewReader("12345"), 4); err == nil ||
		!strings.Contains(err.Error(), "download limit") {
		t.Fatalf("readLimited() error = %v", err)
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
