package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestListResourcesIncludesProjectsAfterFirstPage(t *testing.T) {
	const pageSize = 100
	const projectCount = 2*pageSize + 1
	server, requests := projectListServer(t, map[string]projectListPage{
		"1": {body: projectListJSON(t, 1, pageSize), next: "2"},
		"2": {body: projectListJSON(t, pageSize+1, pageSize), next: "3"},
		"3": {body: projectListJSON(t, projectCount, 1)},
	})
	ds := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "test-token"},
		Settings: map[string]interface{}{"projects": []interface{}{
			map[string]interface{}{"project_id": "1"},
		}},
	}

	resources, err := NewConnector().ListResources(context.Background(), ds, "")
	require.NoError(t, err)
	require.Equal(t, projectCount, len(resources), "every GitLab project must be available for selection")
	for i, resource := range resources {
		require.Equal(t, fmt.Sprint(i+1), resource.ExternalID)
		require.Equal(t, fmt.Sprintf("group/project-%03d", i+1), resource.Name)
		require.Equal(t, fmt.Sprintf("https://gitlab.example.com/group/project-%03d", i+1), resource.URL)
		require.Equal(t, "project", resource.Type)
		require.True(t, resource.HasChildren)
	}
	require.Equal(t, []string{"1", "2", "3"}, recordedProjectPages(requests))
}

func TestProjectsStopsAtFinalPage(t *testing.T) {
	const pageSize = 100
	for _, count := range []int{0, pageSize} {
		t.Run(fmt.Sprintf("%d projects", count), func(t *testing.T) {
			server, requests := projectListServer(t, map[string]projectListPage{
				"1": {body: projectListJSON(t, 1, count)},
			})
			c, err := newClient(server.URL, "test-token")
			require.NoError(t, err)
			projects, err := c.projects(context.Background())
			require.NoError(t, err)
			require.Len(t, projects, count)
			require.Equal(t, []string{"1"}, recordedProjectPages(requests))
		})
	}
}

func TestProjectsRejectsIncompleteList(t *testing.T) {
	for _, tc := range []struct {
		name string
		page projectListPage
	}{
		{name: "HTTP error", page: projectListPage{status: http.StatusServiceUnavailable}},
		{name: "malformed JSON", page: projectListPage{body: "not JSON"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := projectListServer(t, map[string]projectListPage{
				"1": {body: projectListJSON(t, 1, 1), next: "2"},
				"2": tc.page,
			})
			c, err := newClient(server.URL, "test-token")
			require.NoError(t, err)
			projects, err := c.projects(context.Background())
			require.Error(t, err)
			require.Nil(t, projects, "do not return a partial list as a successful result")
			require.Equal(t, []string{"1", "2"}, recordedProjectPages(requests))
		})
	}
}

type projectListPage struct {
	body   string
	next   string
	status int
}

func projectListServer(t *testing.T, pages map[string]projectListPage) (*httptest.Server, <-chan string) {
	t.Helper()
	allowLocalGitLabServer(t)
	requests := make(chan string, len(pages)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/api/v4/projects" ||
			r.Header.Get("PRIVATE-TOKEN") != "test-token" || query.Get("membership") != "true" ||
			query.Get("per_page") != "100" || query.Get("order_by") != "path_with_namespace" ||
			query.Get("sort") != "asc" {
			http.Error(w, "unexpected project-list request", http.StatusBadRequest)
			return
		}
		page := query.Get("page")
		if page == "" {
			page = "1" // GitLab defaults to its first page when the parameter is omitted.
		}
		response, ok := pages[page]
		if !ok {
			http.Error(w, "unexpected page", http.StatusBadRequest)
			return
		}
		select {
		case requests <- page:
		default:
			http.Error(w, "too many page requests", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Next-Page", response.next)
		if response.status != 0 {
			w.WriteHeader(response.status)
		}
		_, _ = fmt.Fprint(w, response.body)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func projectListJSON(t *testing.T, start, count int) string {
	t.Helper()
	projects := make([]project, 0, count)
	for id := start; id < start+count; id++ {
		projects = append(projects, project{
			ID: int64(id), PathWithNamespace: fmt.Sprintf("group/project-%03d", id),
			WebURL: fmt.Sprintf("https://gitlab.example.com/group/project-%03d", id),
		})
	}
	data, err := json.Marshal(projects)
	require.NoError(t, err)
	return string(data)
}

func recordedProjectPages(requests <-chan string) []string {
	var pages []string
	for len(requests) > 0 {
		pages = append(pages, <-requests)
	}
	return pages
}
