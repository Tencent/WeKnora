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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/projects/1":
			_, _ = w.Write([]byte(`{"id":1,"name":"project-one","path_with_namespace":"group/project-one","web_url":"https://gitlab.test/group/project-one","default_branch":"master"}`))
		case "/projects/2":
			_, _ = w.Write([]byte(`{"id":2,"name":"project-two","path_with_namespace":"group/project-two","web_url":"https://gitlab.test/group/project-two","default_branch":"master"}`))
		case "/projects/1/repository/commits/master":
			_, _ = w.Write([]byte(`{"id":"commit-1"}`))
		case "/projects/2/repository/commits/master":
			_, _ = w.Write([]byte(`{"id":"commit-2"}`))
		case "/projects/1/repository/tree":
			_, _ = w.Write([]byte(`[{"name":"one.md","type":"blob","path":"one.md"}]`))
		case "/projects/2/repository/tree":
			_, _ = w.Write([]byte(`[{"name":"two.md","type":"blob","path":"two.md"}]`))
		default:
			if strings.HasPrefix(r.URL.EscapedPath(), "/projects/1/repository/files/one%2Emd/raw") {
				_, _ = w.Write([]byte("one"))
				return
			}
			if strings.HasPrefix(r.URL.EscapedPath(), "/projects/2/repository/files/two%2Emd/raw") {
				_, _ = w.Write([]byte("two"))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	connector := &Connector{
		client:        &client{baseURL: server.URL, http: server.Client()},
		canonicalBase: server.URL,
	}
	config := &types.DataSourceConfig{Settings: map[string]interface{}{
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

func TestDirectoryExistsListsTheTargetPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/18296/repository/tree" || r.URL.Query().Get("path") != "docs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"quickstart.mdx","type":"blob","path":"docs/quickstart.mdx"}]`))
	}))
	defer server.Close()

	connector := &Connector{client: &client{baseURL: server.URL, http: server.Client()}}
	exists, err := connector.directoryExists(context.Background(), "18296", "master", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected docs directory to exist")
	}
}

func TestMembersFallsBackWhenInheritedMembersEndpointIsUnavailable(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/18724/members/all":
			http.NotFound(w, r)
		case "/api/v4/projects/18724/members":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"username":"alice","state":"active"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	members, err := c.members(context.Background(), "18724")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Username != "alice" {
		t.Fatalf("members = %#v", members)
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
