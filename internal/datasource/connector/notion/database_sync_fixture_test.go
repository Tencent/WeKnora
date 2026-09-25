package notion

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const databaseTestTime = "2026-01-15T10:00:00Z"

type databaseFixture struct {
	rows       map[string][]string
	times      map[string]string
	blockReads int
	failQuery  bool
	failBlocks bool
	searchRows bool
	server     *httptest.Server
}

func newDatabaseFixture(t *testing.T) *databaseFixture {
	t.Helper()
	allowNotionTestServer(t)
	f := &databaseFixture{
		rows:  map[string][]string{"db-a": {"row-a", "row-b"}, "db-b": {"row-c"}},
		times: make(map[string]string), searchRows: true,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *databaseFixture) object(id, parent string) map[string]interface{} {
	edited := f.times[id]
	if edited == "" {
		edited = databaseTestTime
	}
	obj := map[string]interface{}{
		"id": id, "object": "data_source", "last_edited_time": edited,
		"title":  []interface{}{map[string]interface{}{"plain_text": id}},
		"parent": map[string]interface{}{"type": "workspace", "workspace": true},
	}
	if parent != "" {
		obj["object"] = "page"
		obj["parent"] = map[string]interface{}{"type": "data_source_id", "data_source_id": parent}
		obj["properties"] = map[string]interface{}{
			"Name": map[string]interface{}{"type": "title", "title": obj["title"]},
		}
	}
	return obj
}

func (f *databaseFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	switch {
	case path == "search":
		var results []interface{}
		for id, rows := range f.rows {
			results = append(results, f.object(id, ""))
			if f.searchRows {
				for _, row := range rows {
					results = append(results, f.object(row, id))
				}
			}
		}
		writeDatabaseList(w, results, false)
	case strings.HasPrefix(path, "data_sources/"):
		id := strings.Split(path, "/")[1]
		if strings.HasSuffix(path, "/query") {
			f.query(w, r, id)
			return
		}
		_ = json.NewEncoder(w).Encode(f.object(id, ""))
	case strings.HasPrefix(path, "blocks/"):
		f.blockReads++
		if f.failBlocks {
			http.Error(w, "access revoked", http.StatusUnauthorized)
			return
		}
		writeDatabaseList(w, []interface{}{}, false)
	default:
		http.NotFound(w, r)
	}
}

func (f *databaseFixture) query(w http.ResponseWriter, r *http.Request, id string) {
	var request map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&request)
	if f.failQuery && request["start_cursor"] != nil {
		http.Error(w, "access revoked", http.StatusUnauthorized)
		return
	}
	var results []interface{}
	for _, row := range f.rows[id] {
		results = append(results, f.object(row, id))
	}
	writeDatabaseList(w, results, f.failQuery)
}

func writeDatabaseList(w http.ResponseWriter, results []interface{}, more bool) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results, "has_more": more, "next_cursor": "next-page",
	})
}

func (f *databaseFixture) config(ids ...string) *types.DataSourceConfig {
	return makeNotionConfig(&Config{APIKey: "test-token"}, f.server.URL, ids)
}

func databaseLegacyCursor() *types.SyncCursor {
	edited, _ := time.Parse(time.RFC3339, databaseTestTime)
	return buildCursor(map[string]time.Time{
		"db-a": edited, "db-b": edited, "row-a": edited, "row-b": edited, "row-c": edited,
	})
}
