package notion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/stretchr/testify/require"
)

// A single data source query returns at most 10,000 rows; the vendor's recipe for
// reading a larger data source is to sort by created_time ascending and to start a
// new window (filter created_time.on_or_after = the last row of the previous
// window) every time a query comes back with request_status.type="incomplete",
// de-duplicating rows by ID.
// See https://developers.notion.com/guides/data-apis/query-large-data-sources
//
// The fake server below answers queries that way, with the vendor's per-query
// limit, the page size and the window boundaries scaled down to test size, and it
// records every request body so tests can assert on the window filter and on the
// created_time sort.

type windowTestRow struct {
	id          string
	createdTime string // RFC3339; empty emulates a row without created_time
}

// createdTimeRows builds rows one minute apart, in created_time ascending order.
func createdTimeRows(ids ...string) []windowTestRow {
	rows := make([]windowTestRow, 0, len(ids))
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range ids {
		createdTime := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		rows = append(rows, windowTestRow{id: id, createdTime: createdTime})
	}
	return rows
}

type windowQueryFake struct {
	// rows is the whole data source, in created_time ascending order.
	rows []windowTestRow
	// queryLimit is how many rows one query returns before the vendor marks it
	// incomplete; 0 means every query returns all matching rows.
	queryLimit int
	// pageSize is how many rows one page of a query holds; 0 means 100.
	pageSize int
	// markerFirstPageOnly models the vendor caveat that the incomplete marker can
	// appear before the last page received for a capped query instead of on it.
	markerFirstPageOnly bool
	// alwaysIncomplete marks every query incomplete, even one that matched nothing.
	alwaysIncomplete bool
	// omitCompleteMarker answers a complete window without request_status at all.
	omitCompleteMarker bool

	// searchResults is served from POST /v1/search when set.
	searchResults []interface{}
	// blocks is served from GET /v1/blocks/{id}/children when set.
	blocks map[string][]interface{}
}

type windowQueryServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []map[string]interface{}
	calls    int32
}

func (s *windowQueryServer) queryCalls() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// queryRequests returns the decoded body of every data source query served so far.
func (s *windowQueryServer) queryRequests() []map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]interface{}(nil), s.requests...)
}

func (s *windowQueryServer) serveQuery(w http.ResponseWriter, r *http.Request, fake windowQueryFake) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read query request", http.StatusBadRequest)
		return
	}
	var request map[string]interface{}
	if err := json.Unmarshal(raw, &request); err != nil {
		http.Error(w, "unmarshal query request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.calls++
	s.requests = append(s.requests, request)
	s.mu.Unlock()

	response, errText := fake.answer(request)
	if errText != "" {
		http.Error(w, errText, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}

// answer builds the response the vendor would send for one query request. The
// second result is a non-empty error text when the request cannot be served.
func (f windowQueryFake) answer(request map[string]interface{}) (map[string]interface{}, string) {
	lower, errText := requestWindowStart(request)
	if errText != "" {
		return nil, errText
	}

	// The filter is applied before the per-query limit is.
	var matching []windowTestRow
	for _, row := range f.rows {
		if lower.IsZero() {
			matching = append(matching, row)
			continue
		}
		created, err := time.Parse(time.RFC3339, row.createdTime)
		if err != nil {
			return nil, "test row " + row.id + " has no parsable created_time"
		}
		if !created.Before(lower) {
			matching = append(matching, row)
		}
	}

	// A capped query ends the window with has_more=false even though the data
	// source holds more rows.
	window := matching
	capped := f.alwaysIncomplete
	if f.queryLimit > 0 && len(window) > f.queryLimit {
		window = window[:f.queryLimit]
		capped = true
	}

	offset, errText := requestCursorOffset(request)
	if errText != "" {
		return nil, errText
	}
	if offset > len(window) {
		return nil, "start_cursor points past the end of the window"
	}
	size := f.pageSize
	if requested, ok := request["page_size"].(float64); ok && int(requested) < size {
		size = int(requested)
	}
	end := offset + size
	if end > len(window) {
		end = len(window)
	}
	page := window[offset:end]

	results := make([]interface{}, 0, len(page))
	for _, row := range page {
		results = append(results, windowRecord(row))
	}

	hasMore := end < len(window)
	response := map[string]interface{}{
		"object": "list", "results": results, "has_more": hasMore, "next_cursor": nil,
	}
	if hasMore {
		response["next_cursor"] = strconv.Itoa(end)
	}
	switch {
	case capped && (!f.markerFirstPageOnly || offset == 0):
		response["request_status"] = truncationIncomplete()
	case !capped && !f.omitCompleteMarker:
		response["request_status"] = map[string]interface{}{"type": "complete"}
	}
	return response, ""
}

func requestWindowStart(request map[string]interface{}) (time.Time, string) {
	filter, _ := request["filter"].(map[string]interface{})
	if filter == nil {
		return time.Time{}, ""
	}
	created, _ := filter["created_time"].(map[string]interface{})
	if created == nil {
		return time.Time{}, "query filter is missing created_time"
	}
	raw, _ := created["on_or_after"].(string)
	if raw == "" {
		return time.Time{}, "query filter is missing created_time.on_or_after"
	}
	start, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, "unparsable created_time.on_or_after: " + raw
	}
	return start, ""
}

func requestCursorOffset(request map[string]interface{}) (int, string) {
	raw, _ := request["start_cursor"].(string)
	if raw == "" {
		return 0, ""
	}
	offset, err := strconv.Atoi(raw)
	if err != nil {
		return 0, "unparsable start_cursor: " + raw
	}
	return offset, ""
}

func windowRecord(row windowTestRow) map[string]interface{} {
	record := truncationRecord(row.id)
	if row.createdTime != "" {
		record["created_time"] = row.createdTime
	}
	return record
}

func windowDataSourceInfo() map[string]interface{} {
	return map[string]interface{}{
		"id": "ds-1", "object": "data_source",
		"title": []interface{}{map[string]interface{}{"plain_text": "Large Database"}},
	}
}

func newWindowQueryServer(t *testing.T, fake windowQueryFake) *windowQueryServer {
	t.Helper()
	if fake.pageSize <= 0 {
		fake.pageSize = 100
	}
	server := &windowQueryServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/search":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list", "results": fake.searchResults, "has_more": false, "next_cursor": nil,
			})
		case r.URL.Path == "/v1/data_sources/ds-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(windowDataSourceInfo())
		case r.URL.Path == "/v1/data_sources/ds-1/query":
			server.serveQuery(w, r, fake)
		case strings.HasPrefix(r.URL.Path, "/v1/blocks/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/blocks/"), "/children")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list", "results": fake.blocks[id], "has_more": false, "next_cursor": nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// --- Assertions on recorded requests ---

func recordIDs(records []notionPage) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}

// assertCreatedTimeSort pins the recipe's first step: every query sorts by
// created_time ascending, which is what gives each window a stable order.
func assertCreatedTimeSort(t *testing.T, request map[string]interface{}) {
	t.Helper()
	require.Equal(t, []interface{}{
		map[string]interface{}{"timestamp": "created_time", "direction": "ascending"},
	}, request["sorts"], "every query must sort by created_time ascending")
}

// windowFilterStart returns filter.created_time.on_or_after from a recorded
// request, or "" when the request carries no window filter (the first window).
func windowFilterStart(t *testing.T, request map[string]interface{}) string {
	t.Helper()
	filter, ok := request["filter"].(map[string]interface{})
	if !ok {
		return ""
	}
	require.Equal(t, "created_time", filter["timestamp"], "the window filter must be a created_time filter")
	created, ok := filter["created_time"].(map[string]interface{})
	require.True(t, ok, "the window filter must carry a created_time object")
	start, ok := created["on_or_after"].(string)
	require.True(t, ok, "the window filter must carry created_time.on_or_after")
	return start
}

// --- Log capture ---

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureNotionLogs redirects the logger into a buffer for the duration of the
// test so warnings about capped queries can be asserted on.
func captureNotionLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buffer := &syncBuffer{}
	logger.SetOutput(buffer)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	return buffer
}

// --- Tests ---

func TestQueryDatabaseAllReadsPastTheLimitInCreatedTimeWindows(t *testing.T) {
	logs := captureNotionLogs(t)
	server := newWindowQueryServer(t, windowQueryFake{
		rows:       createdTimeRows("r1", "r2", "r3", "r4", "r5"),
		queryLimit: 2,
		pageSize:   2,
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.NoError(t, err, "a data source larger than one query must still be read in full")
	require.Equal(t, []string{"r1", "r2", "r3", "r4", "r5"}, recordIDs(records),
		"boundary rows are returned by two windows and must be kept once, in created_time order")
	require.Equal(t, int32(4), server.queryCalls(), "every capped window must start a new query")

	requests := server.queryRequests()
	require.Len(t, requests, 4)
	for _, request := range requests {
		assertCreatedTimeSort(t, request)
	}
	require.Empty(t, windowFilterStart(t, requests[0]), "the first window is read without a filter")
	for i, want := range []string{"2026-01-01T10:01:00Z", "2026-01-01T10:02:00Z", "2026-01-01T10:03:00Z"} {
		require.Equal(t, want, windowFilterStart(t, requests[i+1]),
			"window %d must continue from the last row of the previous window", i+2)
	}
	require.Contains(t, logs.String(), "hit the vendor per-query result limit",
		"continuing past the limit must stay visible in the logs")
}

func TestQueryDatabaseAllDeduplicatesRowsByID(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		rows: []windowTestRow{
			{id: "r1", createdTime: "2026-01-01T10:00:00Z"},
			{id: "r1", createdTime: "2026-01-01T10:00:00Z"},
			{id: "r2", createdTime: "2026-01-01T10:01:00Z"},
		},
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r2"}, recordIDs(records), "a row returned twice must be kept once")
	require.Equal(t, int32(1), server.queryCalls())
}

func TestQueryDatabaseAllIncompleteMarkerBeforeLastPageStartsNextWindow(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		rows:                createdTimeRows("r1", "r2", "r3", "r4"),
		queryLimit:          2,
		pageSize:            1,
		markerFirstPageOnly: true,
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r2", "r3", "r4"}, recordIDs(records))
	require.Equal(t, int32(6), server.queryCalls(),
		"a marker seen on a non-final page makes the whole window incomplete, so the walk must continue")
}

func TestQueryDatabaseAllCompleteWindowUnchanged(t *testing.T) {
	cases := map[string]windowQueryFake{
		"request_status complete": {rows: createdTimeRows("r1", "r2", "r3")},
		"no request_status":       {rows: createdTimeRows("r1", "r2", "r3"), omitCompleteMarker: true},
	}
	for name, fake := range cases {
		t.Run(name, func(t *testing.T) {
			server := newWindowQueryServer(t, fake)
			client := mustTestClient(t, "test-token", server.URL)

			records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
			require.NoError(t, err, "a result set that fits in one query must not start a second window")
			require.Equal(t, []string{"r1", "r2", "r3"}, recordIDs(records))
			require.Equal(t, int32(1), server.queryCalls())

			requests := server.queryRequests()
			require.Len(t, requests, 1)
			require.Empty(t, windowFilterStart(t, requests[0]), "the first window is read without a filter")
			assertCreatedTimeSort(t, requests[0])
		})
	}
}
