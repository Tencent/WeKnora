package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
