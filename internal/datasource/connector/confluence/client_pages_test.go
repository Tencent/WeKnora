package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	pages, _, err := c.pages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
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

func TestServerPagesStopAfterRepeatedEmptyPages(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		results := `[]`
		if r.URL.Query().Get("start") == "" {
			results = `[{"id": "1", "title": "Root", "version": {"number": 1}}]`
		}
		next := fmt.Sprintf("/rest/api/space/ENG/content/page?limit=100&start=%d", calls*100)
		_, _ = fmt.Fprintf(w, `{"results": %s, "_links": {"next": %q}}`, results, next)
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	pages, complete, err := c.pages(context.Background(), space{Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("a listing cut short by empty pages must not report complete")
	}
	if len(pages) != 1 || pages[0].ID != "1" {
		t.Fatalf("pages = %#v", pages)
	}
	if calls != 1+maxEmptyServerPages {
		t.Fatalf("calls = %d, want %d", calls, 1+maxEmptyServerPages)
	}
}

func TestServerPagesContinuePastOneEmptyPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("start") {
		case "":
			_, _ = w.Write([]byte(`{"results": [{"id": "1", "title": "A"}],
				"_links": {"next": "/rest/api/space/ENG/content/page?limit=100&start=100"}}`))
		case "100":
			// Every page in this window was filtered out by permissions.
			_, _ = w.Write([]byte(`{"results": [],
				"_links": {"next": "/rest/api/space/ENG/content/page?limit=100&start=200"}}`))
		default:
			_, _ = w.Write([]byte(`{"results": [{"id": "2", "title": "B"}]}`))
		}
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	pages, complete, err := c.pages(context.Background(), space{Key: "ENG", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if !complete || len(pages) != 2 {
		t.Fatalf("complete = %v, pages = %#v", complete, pages)
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
	pages, _, err := c.pages(context.Background(), space{Key: "ENG", Name: "Engineering"})
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
	pages, _, err := c.pages(context.Background(), space{ID: "1", Key: "ENG", Name: "Engineering"})
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

func TestPageInSpace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expand") != "space" {
			t.Errorf("expand = %q, want space", r.URL.Query().Get("expand"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/content/current":
			_, _ = w.Write([]byte(`{"id": "current", "status": "current", "space": {"key": "ENG"}}`))
		case "/rest/api/content/trashed":
			_, _ = w.Write([]byte(`{"id": "trashed", "status": "trashed", "space": {"key": "ENG"}}`))
		case "/rest/api/content/moved":
			_, _ = w.Write([]byte(`{"id": "moved", "status": "current", "space": {"key": "OPS"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
	for id, want := range map[string]bool{"current": true, "trashed": false, "moved": false, "deleted": false} {
		got, err := c.pageInSpace(context.Background(), id, "ENG")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != want {
			t.Errorf("pageInSpace(%s) = %v, want %v", id, got, want)
		}
	}
}
