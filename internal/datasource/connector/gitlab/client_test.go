package gitlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func allowLocalGitLabServer(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestConnectorValidateUsesDataSourceCredentials(t *testing.T) {
	allowLocalGitLabServer(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "per-source-token" {
			t.Fatalf("PRIVATE-TOKEN = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 42}`))
	}))
	defer server.Close()

	ds := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     server.URL,
			"access_token": "per-source-token",
		},
	}

	if err := NewConnector().Validate(context.Background(), ds); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConnectorValidateReturnsGitLabAPIError(t *testing.T) {
	allowLocalGitLabServer(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "invalid token", http.StatusUnauthorized)
	}))
	defer server.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     server.URL,
			"access_token": "invalid-token",
		},
	})
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Validate() error = %v, want GitLab API error", err)
	}
	if apiErr.endpoint != "/user" || apiErr.status != http.StatusUnauthorized {
		t.Fatalf("apiError = %#v", apiErr)
	}
}

func TestConnectorValidateRejectsMissingCredentials(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": "https://gitlab.example.com"},
	})
	if err == nil || err.Error() != "GitLab platform configuration is missing" {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConnectorValidateRejectsMissingProjectsOnSave(t *testing.T) {
	allowLocalGitLabServer(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 42}`))
	}))
	defer server.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     server.URL,
			"access_token": "token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{},
		},
	})
	if err == nil {
		t.Fatal("expected missing projects validation error")
	}
}

func TestConnectorConfiguredDoesNotReuseRegistryClient(t *testing.T) {
	allowLocalGitLabServer(t)

	var requestedTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		requestedTokens = append(requestedTokens, r.Header.Get("PRIVATE-TOKEN"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 42}`))
	}))
	defer server.Close()

	connector := NewConnector()
	first := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token-a"},
	}
	second := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token-b"},
	}
	if err := connector.Validate(context.Background(), first); err != nil {
		t.Fatalf("first validate: %v", err)
	}
	if err := connector.Validate(context.Background(), second); err != nil {
		t.Fatalf("second validate: %v", err)
	}
	if len(requestedTokens) != 2 || requestedTokens[0] != "token-a" || requestedTokens[1] != "token-b" {
		t.Fatalf("requested tokens = %#v", requestedTokens)
	}
}

func TestNewClientNormalizesAPIBaseURL(t *testing.T) {
	allowLocalGitLabServer(t)

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	c, err := newClient(server.URL+"/", "token")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.baseURL, server.URL+"/api/v4"; got != want {
		t.Fatalf("baseURL = %q, want %q", got, want)
	}
}

func TestFetchIncrementalSyncsMultipleProjects(t *testing.T) {
	allowLocalGitLabServer(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/1":
			_, _ = w.Write([]byte(`{"id":1,"name":"project-one","path_with_namespace":"group/project-one","web_url":"https://gitlab.test/group/project-one","default_branch":"master"}`))
		case "/api/v4/projects/2":
			_, _ = w.Write([]byte(`{"id":2,"name":"project-two","path_with_namespace":"group/project-two","web_url":"https://gitlab.test/group/project-two","default_branch":"master"}`))
		case "/api/v4/projects/1/repository/commits/master":
			_, _ = w.Write([]byte(`{"id":"commit-1"}`))
		case "/api/v4/projects/2/repository/commits/master":
			_, _ = w.Write([]byte(`{"id":"commit-2"}`))
		case "/api/v4/projects/1/repository/tree":
			_, _ = w.Write([]byte(`[{"name":"one.md","type":"blob","path":"one.md"}]`))
		case "/api/v4/projects/2/repository/tree":
			_, _ = w.Write([]byte(`[{"name":"two.md","type":"blob","path":"two.md"}]`))
		default:
			if strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/projects/1/repository/files/one%2Emd/raw") {
				_, _ = w.Write([]byte("one"))
				return
			}
			if strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/projects/2/repository/files/two%2Emd/raw") {
				_, _ = w.Write([]byte("two"))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	connector := NewConnector()
	config := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     server.URL,
			"access_token": "token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{
				map[string]interface{}{"project_id": "1", "ref": "master", "paths": []interface{}{}},
				map[string]interface{}{"project_id": "2", "ref": "master", "paths": []interface{}{}},
			},
		},
	}

	items, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("first sync items = %d, want 2", len(items))
	}
	if cursor == nil || fmt.Sprint(cursor.ConnectorCursor["projects"]) == "" {
		t.Fatal("first sync did not return per-project cursor state")
	}

	items, _, err = connector.FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("second sync items = %d, want 0", len(items))
	}
}

type gitLabStreamRecorder struct {
	items       []types.FetchedItem
	checkpoints []*types.SyncCursor
}

func (h *gitLabStreamRecorder) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (h *gitLabStreamRecorder) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	h.checkpoints = append(h.checkpoints, cursor)
	return nil
}

func TestFetchStreamFiltersUnsupportedFilesAndCheckpointsProjects(t *testing.T) {
	allowLocalGitLabServer(t)

	var rawRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/1":
			_, _ = w.Write([]byte(`{"id":1,"name":"docs","path_with_namespace":"group/docs","web_url":"https://gitlab.test/group/docs","default_branch":"main"}`))
		case "/api/v4/projects/1/repository/commits/main":
			_, _ = w.Write([]byte(`{"id":"commit-1"}`))
		case "/api/v4/projects/1/repository/tree":
			_, _ = w.Write([]byte(`[
				{"name":"README.md","type":"blob","path":"README.md"},
				{"name":"server.go","type":"blob","path":"server.go"},
				{"name":"payload.exe","type":"blob","path":"payload.exe"}
			]`))
		default:
			if strings.Contains(r.URL.EscapedPath(), "/repository/files/") {
				rawRequests = append(rawRequests, r.URL.EscapedPath())
				_, _ = w.Write([]byte("# readme"))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	connector := NewConnector()
	config := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":     server.URL,
			"access_token": "token",
		},
		Settings: map[string]interface{}{
			"projects": []interface{}{map[string]interface{}{"project_id": "1", "paths": []interface{}{}}},
		},
	}
	handler := &gitLabStreamRecorder{}
	next, err := connector.FetchStream(context.Background(), config, nil, handler)
	if err != nil {
		t.Fatalf("FetchStream() error = %v", err)
	}
	if len(handler.items) != 1 || handler.items[0].FileName != "docs-main/README.md" {
		t.Fatalf("emitted items = %#v", handler.items)
	}
	if len(rawRequests) != 1 || !strings.Contains(rawRequests[0], "README%2Emd") {
		t.Fatalf("raw requests = %v, want only README.md", rawRequests)
	}
	if len(handler.checkpoints) != 1 || next == nil {
		t.Fatalf("checkpoints = %d, next = %#v", len(handler.checkpoints), next)
	}
	projects, _ := next.ConnectorCursor["projects"].(map[string]string)
	if projects["1"] != "commit-1" {
		t.Fatalf("cursor projects = %#v", next.ConnectorCursor["projects"])
	}
}

func TestIsSupportedFile(t *testing.T) {
	for _, tc := range []struct {
		file string
		want bool
	}{
		{file: "docs/guide.MD", want: true},
		{file: "docs/guide.mdx", want: true},
		{file: "report.pdf", want: true},
		{file: "src/main.go", want: false},
		{file: "archive.tar.gz", want: false},
		{file: "LICENSE", want: false},
	} {
		if got := isSupportedFile(tc.file); got != tc.want {
			t.Errorf("isSupportedFile(%q) = %v, want %v", tc.file, got, tc.want)
		}
	}
}

func TestTreeFollowsGitLabPagination(t *testing.T) {
	allowLocalGitLabServer(t)
	var requestedPages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/repository/tree" {
			http.NotFound(w, r)
			return
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"name":"one.md","type":"blob","path":"one.md"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"name":"two.md","type":"blob","path":"two.md"}]`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	c, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := c.tree(context.Background(), "1", "main", "")
	if err != nil {
		t.Fatalf("tree() error = %v", err)
	}
	if len(entries) != 2 || entries[1].Path != "two.md" {
		t.Fatalf("entries = %#v", entries)
	}
	if got, want := strings.Join(requestedPages, ","), "1,2"; got != want {
		t.Fatalf("requested pages = %q, want %q", got, want)
	}
}

func TestProjectPathEncodesNamespaceWithoutDoubleEscaping(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{in: "12345", want: "12345"},
		{in: "group/project", want: "group%2Fproject"},
		{in: "group%2Fproject", want: "group%2Fproject"},
		{in: "my group/my project", want: "my%20group%2Fmy%20project"},
	} {
		if got := projectPath(tc.in); got != tc.want {
			t.Errorf("projectPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRawFallsBackToBase64FileDetail(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/18724/repository/files/docs%2Finternal%2Freadme%2Emd/raw":
			http.NotFound(w, r)
		case "/api/v4/projects/18724/repository/files/docs%2Finternal%2Freadme%2Emd":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"encoding":"base64","content":"SGVsbG8sIEdpdExhYiE="}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	content, err := c.raw(context.Background(), "18724", "master", "docs/internal/readme.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "Hello, GitLab!" {
		t.Fatalf("content = %q", content)
	}
}

func TestGitlabFilePathEscape(t *testing.T) {
	got := gitlabFilePathEscape("docs/internal/中文-file.md")
	want := "docs%2Finternal%2F%E4%B8%AD%E6%96%87-file%2Emd"
	if got != want {
		t.Fatalf("gitlabFilePathEscape() = %q, want %q", got, want)
	}
}

// newCappedTestClient builds a client whose JSON and raw-file caps are small
// enough to exercise them without materialising the production limits.
// compareLimit stays at the production value unless the test assigns it.
func newCappedTestClient(t *testing.T, serverURL string, jsonLimit, rawLimit int64) *client {
	t.Helper()
	c, err := newClient(serverURL, "token")
	if err != nil {
		t.Fatal(err)
	}
	c.jsonLimit = jsonLimit
	c.rawLimit = rawLimit
	return c
}

func TestGetRawRejectsBlobLargerThanCap(t *testing.T) {
	allowLocalGitLabServer(t)
	const rawLimit = 1 << 20

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), rawLimit+1))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, maxJSONResponseBytes, rawLimit)
	content, err := c.getRaw(context.Background(), "/projects/1/repository/files/big.bin/raw")
	if err == nil {
		t.Fatalf("getRaw() accepted a %d-byte blob under a %d-byte cap", len(content), rawLimit)
	}
	if content != nil {
		t.Fatalf("getRaw() returned %d bytes alongside the error, want no partial body", len(content))
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(rawLimit)) {
		t.Fatalf("error = %v, want it to name the %d-byte limit", err, rawLimit)
	}
}

func TestGetRawAcceptsBlobAtCap(t *testing.T) {
	allowLocalGitLabServer(t)
	const rawLimit = 1 << 20

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), rawLimit))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, maxJSONResponseBytes, rawLimit)
	content, err := c.getRaw(context.Background(), "/projects/1/repository/files/fit.bin/raw")
	if err != nil {
		t.Fatalf("getRaw() rejected a blob exactly at the cap: %v", err)
	}
	if len(content) != rawLimit {
		t.Fatalf("content = %d bytes, want the full %d", len(content), rawLimit)
	}
}

// TestRawSurfacesOversizedBlobInsteadOfFallingBack checks that hitting the size
// cap fails the fetch rather than being mistaken for a missing /raw route and
// retried through the base64 detail endpoint.
func TestRawSurfacesOversizedBlobInsteadOfFallingBack(t *testing.T) {
	allowLocalGitLabServer(t)
	const rawLimit = 1 << 20
	detailHits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.EscapedPath(), "/raw") {
			_, _ = w.Write(bytes.Repeat([]byte("a"), rawLimit+1))
			return
		}
		detailHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"encoding":"base64","content":"SGk="}`))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, maxJSONResponseBytes, rawLimit)
	const file = "docs/guide.mp3"
	content, err := c.raw(context.Background(), "1", "main", file)
	if err == nil {
		t.Fatalf("raw() accepted a %d-byte blob under a %d-byte cap", len(content), rawLimit)
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
	if !strings.Contains(err.Error(), file) {
		t.Fatalf("error = %v, want the repository path %s", err, file)
	}
	if detailHits != 0 {
		t.Fatalf("base64 detail endpoint hit %d times, want 0", detailHits)
	}
}

func TestBase64FallbackRejectsOversizedDetail(t *testing.T) {
	allowLocalGitLabServer(t)
	const rawLimit = 3 << 20
	limit := base64FileBytes(rawLimit)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.EscapedPath(), "/raw") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), int(limit)+1))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, maxJSONResponseBytes, rawLimit)
	const file = "docs/guide.mp3"
	if _, err := c.raw(context.Background(), "1", "main", file); err == nil {
		t.Fatal("raw() accepted an oversized base64 detail response")
	} else if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	} else if !strings.Contains(err.Error(), file) {
		t.Fatalf("error = %v, want the repository path %s", err, file)
	}
}

func TestGetRejectsOversizedJSONResponse(t *testing.T) {
	allowLocalGitLabServer(t)
	const jsonLimit = 512 << 10

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), jsonLimit+1))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, jsonLimit, maxRawFileBytes)
	var out struct{}
	err := c.get(context.Background(), "/projects/1", &out)
	if err == nil {
		t.Fatal("get() accepted an oversized JSON response")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
}

func TestGetPageRejectsOversizedJSONResponse(t *testing.T) {
	allowLocalGitLabServer(t)
	const jsonLimit = 512 << 10

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), jsonLimit+1))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, jsonLimit, maxRawFileBytes)
	if _, err := c.tree(context.Background(), "1", "main", ""); err == nil {
		t.Fatal("tree() accepted an oversized JSON response")
	} else if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
}

// TestOversizedErrorBodyKeepsStatusError pins that an oversized body on a
// non-2xx response is still reported as the HTTP status, so a large error page
// cannot mask the real failure.
func TestOversizedErrorBodyKeepsStatusError(t *testing.T) {
	allowLocalGitLabServer(t)
	const jsonLimit = 512 << 10

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(bytes.Repeat([]byte("a"), jsonLimit+1))
	}))
	defer server.Close()

	for name, call := range map[string]func(*client) error{
		"get": func(c *client) error {
			var out struct{}
			return c.get(context.Background(), "/projects/1", &out)
		},
		"getPage": func(c *client) error {
			_, err := c.tree(context.Background(), "1", "main", "")
			return err
		},
	} {
		c := newCappedTestClient(t, server.URL, jsonLimit, maxRawFileBytes)
		err := call(c)
		var apiErr *apiError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: error = %v, want an *apiError", name, err)
		}
		if apiErr.status != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want %d", name, apiErr.status, http.StatusInternalServerError)
		}
	}
}

func TestNewClientSetsResponseCaps(t *testing.T) {
	allowLocalGitLabServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	c, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if c.jsonLimit != maxJSONResponseBytes ||
		c.compareLimit != maxCompareResponseBytes ||
		c.rawLimit != maxRawFileBytes {
		t.Fatalf("caps = json %d compare %d raw %d", c.jsonLimit, c.compareLimit, c.rawLimit)
	}
	if c.compareLimit <= c.jsonLimit {
		t.Fatalf("compare cap %d must sit above the metadata cap %d", c.compareLimit, c.jsonLimit)
	}
}

// TestCompareAllowsPayloadAboveMetadataCap pins that a straight compare larger
// than the metadata JSON cap still succeeds. That body carries commits and
// patches the sync does not store; rejecting it would stall the project cursor.
func TestCompareAllowsPayloadAboveMetadataCap(t *testing.T) {
	allowLocalGitLabServer(t)
	const (
		jsonLimit    = 512
		compareLimit = 8 << 10
	)
	padding := strings.Repeat("p", jsonLimit)
	payload := `{"diffs":[{"old_path":"docs/old.md","new_path":"docs/new.md","renamed_file":true}],` +
		`"compare_timeout":false,"padding":"` + padding + `"}`
	body := []byte(payload)
	if int64(len(body)) <= jsonLimit || int64(len(body)) > compareLimit {
		t.Fatalf("fixture is %d bytes, want it between %d and %d", len(body), jsonLimit, compareLimit)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, jsonLimit, maxRawFileBytes)
	c.compareLimit = compareLimit

	var project struct{}
	if err := c.get(context.Background(), "/projects/1", &project); err == nil {
		t.Fatal("get() accepted a body above the metadata cap")
	} else if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("get() error = %v, want an explicit over-limit error", err)
	}

	diff, err := c.compare(context.Background(), "1", "abc", "def")
	if err != nil {
		t.Fatalf("compare() rejected a body above the metadata cap and under the compare cap: %v", err)
	}
	if diff.CompareTimeout {
		t.Fatal("compare_timeout = true, want false")
	}
	if len(diff.Diffs) != 1 ||
		diff.Diffs[0].OldPath != "docs/old.md" ||
		diff.Diffs[0].NewPath != "docs/new.md" ||
		!diff.Diffs[0].RenamedFile {
		t.Fatalf("diffs = %+v", diff.Diffs)
	}
}

func TestCompareRejectsPayloadAboveCompareCap(t *testing.T) {
	allowLocalGitLabServer(t)
	const compareLimit = 1024

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), compareLimit+1))
	}))
	defer server.Close()

	c := newCappedTestClient(t, server.URL, maxJSONResponseBytes, maxRawFileBytes)
	c.compareLimit = compareLimit
	_, err := c.compare(context.Background(), "1", "abc", "def")
	if err == nil {
		t.Fatal("compare() accepted a body above the compare cap")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") ||
		!strings.Contains(err.Error(), fmt.Sprint(compareLimit)) {
		t.Fatalf("error = %v, want an explicit over-limit error naming %d", err, compareLimit)
	}
}
