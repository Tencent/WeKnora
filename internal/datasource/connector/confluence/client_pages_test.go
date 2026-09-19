package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagesUsesOfficialFlatListAndKeepsExpandOnNext(t *testing.T) {
	var expands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/space/ENG/content/page" {
			http.NotFound(w, r)
			return
		}
		expands = append(expands, r.URL.Query().Get("expand"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("start") == "100" {
			_, _ = w.Write([]byte(`{
				"results": [{"id": "2", "title": "Child", "version": {"number": 4}}]
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"_links": {"next": "/rest/api/space/ENG/content/page?limit=100&start=100"},
			"results": [{"id": "1", "title": "Root", "version": {"number": 3}}]
		}`))
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	pages, err := c.pages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].ID != "1" || pages[1].ID != "2" {
		t.Fatalf("pages = %#v", pages)
	}
	if pages[0].Version.Number != 3 || pages[1].Version.Number != 4 {
		t.Fatalf("versions = %#v", pages)
	}
	if pages[0].Space.Key != "ENG" || pages[1].Space.Key != "ENG" {
		t.Fatalf("space copied from parent = %#v", pages)
	}
	if len(expands) != 2 || expands[0] != serverPageExpand || expands[1] != serverPageExpand {
		t.Fatalf("expand query = %#v", expands)
	}
}

func TestPagesAcceptsNestedContentEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page": {
				"results": [{"id": "9", "title": "Nested", "version": {"number": 1}}]
			}
		}`))
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	pages, err := c.pages(context.Background(), space{Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].ID != "9" {
		t.Fatalf("pages = %#v", pages)
	}
}

func TestServerSpacePageListPrefersFlatResults(t *testing.T) {
	var listed serverSpacePageList
	if err := json.Unmarshal([]byte(`{
		"_links": {"next": "/rest/api/space/ENG/content/page?limit=5&start=5"},
		"results": [{"id": "98308", "title": "What is Confluence?"}]
	}`), &listed); err != nil {
		t.Fatal(err)
	}
	pages := listed.pages()
	if len(pages) != 1 || pages[0].ID != "98308" {
		t.Fatalf("pages() = %#v", pages)
	}
	if listed.nextLink() != "/rest/api/space/ENG/content/page?limit=5&start=5" {
		t.Fatalf("nextLink() = %q", listed.nextLink())
	}
}

func TestTopLevelPagesServerRestoresScopeOnNext(t *testing.T) {
	var depths, expands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/space/ENG/content/page" {
			http.NotFound(w, r)
			return
		}
		depths = append(depths, r.URL.Query().Get("depth"))
		expands = append(expands, r.URL.Query().Get("expand"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("start") == "100" {
			_, _ = w.Write([]byte(`{
				"results": [{"id": "2", "title": "Second", "version": {"number": 2}}]
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"_links": {"next": "/rest/api/space/ENG/content/page?limit=100&start=100"},
			"results": [{"id": "1", "title": "Root", "version": {"number": 1}}]
		}`))
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	pages, err := c.topLevelPages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].ID != "1" || pages[1].ID != "2" {
		t.Fatalf("pages = %#v", pages)
	}
	// The server's _links.next drops depth and expand; the connector must
	// rebuild the listing scope or later pages would look versionless.
	if len(depths) != 2 || depths[0] != "root" || depths[1] != "root" {
		t.Fatalf("depth query = %#v", depths)
	}
	if len(expands) != 2 || expands[0] != serverPageExpand || expands[1] != serverPageExpand {
		t.Fatalf("expand query = %#v", expands)
	}
}

func TestTopLevelPagesCloudSurfacesRawAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"space not found"}`))
	}))
	t.Cleanup(server.Close)

	cloud := &client{cfg: config{baseURL: server.URL + "/wiki", edition: editionCloud}, http: server.Client()}
	_, err := cloud.topLevelPages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
	if err == nil {
		t.Fatal("cloud topLevelPages unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "does not support") {
		t.Fatalf("cloud 404 reused the Server/DC navigation hint: %v", err)
	}

	srv := &client{cfg: config{baseURL: server.URL + "/wiki"}, http: server.Client()}
	_, err = srv.topLevelPages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
	if err == nil || !strings.Contains(err.Error(), "Confluence server does not support") {
		t.Fatalf("server error = %v; want the Server/DC navigation hint", err)
	}
}

func TestPagesCloudFollowsNextAndKeepsDepth(t *testing.T) {
	var depths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/spaces/1/pages" {
			http.NotFound(w, r)
			return
		}
		depths = append(depths, r.URL.Query().Get("depth"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "page-two" {
			_, _ = w.Write([]byte(`{"results":[{"id":"2","title":"Child","version":{"number":2}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"_links": {"next": "/wiki/api/v2/spaces/1/pages?limit=250&cursor=page-two"},
			"results": [{"id":"1","title":"Root","version":{"number":1}}]
		}`))
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL + "/wiki", edition: editionCloud}, http: server.Client()}
	pages, err := c.pages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].ID != "1" || pages[1].ID != "2" {
		t.Fatalf("pages = %#v", pages)
	}
	if len(depths) != 2 || depths[0] != "all" || depths[1] != "all" {
		t.Fatalf("depth query = %#v", depths)
	}
}
