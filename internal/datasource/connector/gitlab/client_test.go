package gitlab

import (
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
	}}

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
