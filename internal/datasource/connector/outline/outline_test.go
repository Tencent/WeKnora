package outline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/time/rate"
)

const collectionA = "11111111-1111-4111-8111-111111111111"
const collectionB = "22222222-2222-4222-8222-222222222222"

type fixture struct {
	docs      map[string]document
	listed    map[string][]string
	deleted   []string
	deny      map[string]int
	actor     string
	workspace string
}

func sample(id, col string) document {
	body := "hello"
	return document{ID: id, CollectionID: col, Title: id, Text: &body,
		URL: "/doc/" + id, PublishedAt: "2026-09-01T00:00:00Z",
		UpdatedAt: "2026-09-01T00:00:00Z", Revision: "1"}
}

func testFixture(t *testing.T) (*fixture, *types.DataSourceConfig) {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	f := &fixture{docs: map[string]document{}, listed: map[string][]string{}, deny: map[string]int{}, actor: "actor", workspace: "team"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-API-Version") != "2" {
			t.Error("invalid RPC authentication or method")
		}
		var body struct {
			ID           string `json:"id"`
			CollectionID string `json:"collectionId"`
			Offset       int    `json:"offset"`
			Limit        int    `json:"limit"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON request")
		}
		method := strings.TrimPrefix(r.URL.Path, "/api/")
		if status := f.deny[method+":"+body.ID]; status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"ok":false}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var data interface{}
		switch method {
		case "auth.info":
			data = map[string]interface{}{"user": map[string]string{"id": f.actor}, "team": map[string]string{"id": f.workspace}}
		case "collections.info":
			data = collection{ID: body.ID, Name: body.ID}
		case "collections.list":
			data = []collection{{ID: collectionA, Name: "A"}, {ID: collectionB, Name: "B"}}
		case "documents.list":
			rows := []document{}
			ids := f.listed[body.CollectionID]
			for i := body.Offset; i < min(len(ids), body.Offset+body.Limit); i++ {
				rows = append(rows, f.docs[ids[i]])
			}
			data = rows
		case "documents.info":
			d, ok := f.docs[body.ID]
			if !ok {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"ok":false,"error":"not_found"}`)
				return
			}
			data = map[string]interface{}{"document": d}
		case "documents.deleted":
			rows := []map[string]string{}
			for i := body.Offset; i < min(len(f.deleted), body.Offset+body.Limit); i++ {
				rows = append(rows, map[string]string{"id": f.deleted[i]})
			}
			data = rows
		default:
			t.Errorf("unexpected method %s", method)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "data": data})
	}))
	t.Cleanup(server.Close)
	digest := sha256.Sum256([]byte(server.URL + "\x00test-key"))
	limiters.Store(digest, rate.NewLimiter(rate.Inf, 1))
	t.Cleanup(func() { limiters.Delete(digest) })
	return f, &types.DataSourceConfig{Type: types.ConnectorTypeOutline,
		Credentials: map[string]interface{}{"base_url": server.URL, "api_key": "test-key"},
		ResourceIDs: []string{collectionA}, SyncDeletions: true}
}

type acknowledger struct {
	items         []types.FetchedItem
	fail          map[string]bool
	checkpoints   []*types.SyncCursor
	checkpointErr error
	stopAfter     int
}

func (h *acknowledger) Emit(context.Context, types.FetchedItem) error {
	panic("Outline must request acknowledgement")
}
func (h *acknowledger) EmitWithResult(_ context.Context, item types.FetchedItem) (datasource.ApplyResult, error) {
	h.items = append(h.items, item)
	outcome := datasource.ApplyApplied
	if h.fail[item.ExternalID] {
		outcome = datasource.ApplyFailed
	}
	return datasource.ApplyResult{Outcome: outcome}, nil
}
func (h *acknowledger) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	if h.checkpointErr != nil && len(h.checkpoints) >= h.stopAfter {
		return h.checkpointErr
	}
	raw, _ := json.Marshal(cursor)
	var copy types.SyncCursor
	_ = json.Unmarshal(raw, &copy)
	h.checkpoints = append(h.checkpoints, &copy)
	return nil
}

func TestAcknowledgementRetryAndIncremental(t *testing.T) {
	f, cfg := testFixture(t)
	f.docs["a"], f.docs["b"] = sample("a", collectionA), sample("b", collectionA)
	f.listed[collectionA] = []string{"a", "b"}
	c := NewConnector()
	h := &acknowledger{fail: map[string]bool{"a": true}}
	next, err := c.FetchStream(context.Background(), cfg, nil, h)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("expected partial, got %v", err)
	}
	s, _ := parseCursor(next)
	if _, ok := s.Documents["a"]; ok {
		t.Fatal("failed item advanced version")
	}
	if s.PendingUpserts["a"] == "" || s.Documents["b"].Fingerprint == "" {
		t.Fatal("missing pending/success state")
	}
	h = &acknowledger{}
	next, err = c.FetchStream(context.Background(), cfg, next, h)
	if err != nil || len(h.items) != 1 || h.items[0].ExternalID != "a" {
		t.Fatalf("retry: %v %#v", err, h.items)
	}
	h = &acknowledger{}
	_, err = c.FetchStream(context.Background(), cfg, next, h)
	if err != nil || len(h.items) != 0 {
		t.Fatalf("unchanged documents emitted: %v %#v", err, h.items)
	}
}

func TestDeletionFailureAndDisabledDeletionRetainIdentity(t *testing.T) {
	f, cfg := testFixture(t)
	f.docs["a"] = sample("a", collectionA)
	f.listed[collectionA] = []string{"a"}
	c := NewConnector()
	next, err := c.FetchStream(context.Background(), cfg, nil, &acknowledger{})
	if err != nil {
		t.Fatal(err)
	}
	delete(f.docs, "a")
	f.listed[collectionA] = nil
	f.deleted = []string{"a"}
	h := &acknowledger{fail: map[string]bool{"a": true}}
	next, _ = c.FetchStream(context.Background(), cfg, next, h)
	s, _ := parseCursor(next)
	if len(s.Documents) != 1 || s.PendingDeletions["a"] != "pending" {
		t.Fatal("failed deletion lost identity")
	}
	cfg.SyncDeletions = false
	h = &acknowledger{}
	next, _ = c.FetchStream(context.Background(), cfg, next, h)
	s, _ = parseCursor(next)
	if len(s.Documents) != 1 || len(h.items) != 0 {
		t.Fatal("disabled deletion removed identity/content")
	}
	cfg.SyncDeletions = true
	h = &acknowledger{}
	next, err = c.FetchStream(context.Background(), cfg, next, h)
	s, _ = parseCursor(next)
	if err != nil || len(s.Documents) != 0 || len(h.items) != 1 || !h.items[0].IsDeleted {
		t.Fatalf("delete retry failed: %v", err)
	}
}

func TestUnknownMissingAndIncompleteScanNeverDelete(t *testing.T) {
	f, cfg := testFixture(t)
	cfg.ResourceIDs = []string{collectionA, collectionB}
	f.docs["a"] = sample("a", collectionA)
	f.listed[collectionA] = []string{"a"}
	c := NewConnector()
	next, err := c.FetchStream(context.Background(), cfg, nil, &acknowledger{})
	if err != nil {
		t.Fatal(err)
	}
	delete(f.docs, "a")
	f.listed[collectionA] = nil
	for i := 0; i < 3; i++ {
		h := &acknowledger{}
		next, _ = c.FetchStream(context.Background(), cfg, next, h)
		if len(h.items) != 0 {
			t.Fatal("uncertified 404 deleted document")
		}
	}
	f.deleted = []string{"a"}
	f.deny["collections.info:"+collectionB] = 403
	h := &acknowledger{}
	next, _ = c.FetchStream(context.Background(), cfg, next, h)
	s, _ := parseCursor(next)
	if len(h.items) != 0 || len(s.Documents) != 1 {
		t.Fatal("incomplete scan deleted content")
	}
}

func TestMoveAcrossCollectionsAndDeselect(t *testing.T) {
	f, cfg := testFixture(t)
	cfg.ResourceIDs = []string{collectionA, collectionB}
	f.docs["a"] = sample("a", collectionA)
	f.listed[collectionA] = []string{"a"}
	c := NewConnector()
	next, err := c.FetchStream(context.Background(), cfg, nil, &acknowledger{})
	if err != nil {
		t.Fatal(err)
	}
	f.docs["a"] = sample("a", collectionB)
	f.listed[collectionA], f.listed[collectionB] = nil, []string{"a"}
	h := &acknowledger{fail: map[string]bool{"a": true}}
	next, _ = c.FetchStream(context.Background(), cfg, next, h)
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatal("cross-collection move deleted")
		}
	}
	h = &acknowledger{}
	next, err = c.FetchStream(context.Background(), cfg, next, h)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ResourceIDs = []string{collectionA}
	h = &acknowledger{}
	next, err = c.FetchStream(context.Background(), cfg, next, h)
	s, _ := parseCursor(next)
	if err != nil || len(h.items) != 0 || len(s.Documents) != 0 {
		t.Fatal("deselection did not preserve copies and remove baseline")
	}
}

func TestFullSyncPreservesBaselineAndResumes(t *testing.T) {
	f, cfg := testFixture(t)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("doc-%03d", i)
		f.docs[id] = sample(id, collectionA)
		f.listed[collectionA] = append(f.listed[collectionA], id)
	}
	c := NewConnector()
	next, err := c.FetchStream(context.Background(), cfg, nil, &acknowledger{})
	if err != nil {
		t.Fatal(err)
	}
	next, err = c.PrepareFullSyncCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := parseCursor(next)
	if len(s.Documents) != 60 {
		t.Fatal("full sync dropped baseline")
	}
	h := &acknowledger{checkpointErr: errors.New("disk failure"), stopAfter: 2}
	_, err = c.FetchStream(context.Background(), cfg, next, h)
	if err == nil || len(h.checkpoints) < 2 {
		t.Fatal("checkpoint error swallowed")
	}
	last := h.checkpoints[len(h.checkpoints)-1]
	s, _ = parseCursor(last)
	h = &acknowledger{}
	next, err = c.FetchStream(context.Background(), cfg, last, h)
	if err != nil || len(h.items) != 60-len(s.Run.Applied) {
		t.Fatalf("resume re-emitted confirmed versions: %v, %d", err, len(h.items))
	}
}

func TestConfigurationAndCursorValidation(t *testing.T) {
	_, cfg := testFixture(t)
	for _, base := range []string{"", "https://user:pass@example.com", "https://example.com/api", "https://example.com?q=1", "https://example.com/#x", "file:///tmp"} {
		copy := *cfg
		copy.Credentials = map[string]interface{}{"base_url": base, "api_key": "key"}
		if _, err := newClient(&copy); err == nil {
			t.Errorf("accepted invalid base %q", base)
		}
	}
	for _, values := range []map[string]interface{}{{}, {"version": 2}, {"version": 1, "documents": nil}} {
		if _, err := parseCursor(&types.SyncCursor{ConnectorCursor: values}); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}

func TestDocumentShapesAndMarkdown(t *testing.T) {
	for _, raw := range []string{`{"id":"a","text":""}`, `{"document":{"id":"a","text":""}}`} {
		d, err := decodeDocument(json.RawMessage(raw))
		if err != nil || d.Text == nil || *d.Text != "" {
			t.Fatalf("empty text must be valid: %v", err)
		}
	}
	body := "[relative](../other) ![image](/images/a.png)\n\n`[code](/stay)`\n\n```md\n[code](/stay)\n```\n\n[ref][r]\n\n[r]: /reference\n"
	got := absoluteLinks(body, "https://outline.example.com/doc/a")
	for _, want := range []string{"https://outline.example.com/other", "https://outline.example.com/images/a.png",
		"`[code](/stay)`", "```md\n[code](/stay)\n```", "https://outline.example.com/reference"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	name := fileName(strings.Repeat("中", 150) + "/bad")
	if !utf8.ValidString(name) || len(name) > 203 || strings.Contains(name, "/") {
		t.Fatal("unsafe file name")
	}
	a := sample("a", collectionA)
	a.Revision, a.UpdatedAt = "", ""
	fp := fingerprint(a, "/a")
	changed := "changed"
	a.Text = &changed
	if fingerprint(a, "/a") == fp {
		t.Fatal("missing-version content changes skipped")
	}
}

func TestCursorTenThousandSize(t *testing.T) {
	s, _ := parseCursor(nil)
	s.Run = newRun(true)
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)
		s.Documents[id] = version{CollectionID: collectionA, Fingerprint: strings.Repeat("a", 64)}
		s.Run.Seen[id], s.Run.Applied[id], s.PendingUpserts[id] = true, strings.Repeat("b", 64), collectionA
	}
	raw, _ := s.cursor().ToJSON()
	if len(raw) > 10<<20 {
		t.Fatalf("cursor exceeds 10 MiB: %d", len(raw))
	}
	t.Logf("10000-document cursor: %d bytes", len(raw))
}

func TestRetryAfter(t *testing.T) {
	if got := retryDelay("7", 0); got != 7*time.Second {
		t.Fatal(got)
	}
	if got := retryDelay("", 2); got != 8*time.Second {
		t.Fatal(got)
	}
}

func TestFullRunPreparationSurvivesAdmissionRetries(t *testing.T) {
	c := NewConnector()
	s, _ := parseCursor(nil)
	s.Documents["a"] = version{CollectionID: collectionA, Fingerprint: "old"}
	first, err := c.PrepareSyncRunCursor(s.cursor(), "sync-log-1", true)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = parseCursor(first)
	if s.Run == nil || !s.Run.ForceFull || len(s.Documents) != 1 {
		t.Fatal("new logical task must prepare full baseline")
	}
	s.Run.Applied["a"] = "new"
	retry, err := c.PrepareSyncRunCursor(s.cursor(), "sync-log-1", true)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = parseCursor(retry)
	if s.Run.Applied["a"] != "new" {
		t.Fatal("retry discarded applied progress")
	}
	fresh, err := c.PrepareSyncRunCursor(s.cursor(), "sync-log-2", true)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = parseCursor(fresh)
	if len(s.Run.Applied) != 0 || !s.Run.ForceFull {
		t.Fatal("new full request reused old progress")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPaginationRejectsUntrustedOrIncompletePages(t *testing.T) {
	_, cfg := testFixture(t)
	for _, tc := range []struct {
		name  string
		pages []string
	}{
		{"cross origin", []string{`{"data":[{"id":"a"}],"pagination":{"nextPath":"https://evil.example/api/documents.list?offset=1"}}`}},
		{"backwards", []string{`{"data":[{"id":"a"}],"pagination":{"nextPath":"/api/documents.list?offset=0"}}`}},
		{"gap", []string{`{"data":[{"id":"a"}],"pagination":{"nextPath":"/api/documents.list?offset=9"}}`}},
		{"repeated page", []string{`{"data":[{"id":"a"}],"pagination":{"total":3}}`, `{"data":[{"id":"a"}],"pagination":{"total":3}}`}},
		{"early empty", []string{`{"data":[],"pagination":{"total":5}}`}},
		{"malformed", []string{`{"data":{}}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := newClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				if calls >= len(tc.pages) {
					t.Fatal("unexpected extra pagination request")
				}
				data := tc.pages[calls]
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			if err := c.walk(context.Background(), "documents.list", map[string]interface{}{}, func([]json.RawMessage) error { return nil }); err == nil {
				t.Fatal("incomplete scan accepted")
			}
		})
	}
}

func TestHTTPRetryBoundAndCancellation(t *testing.T) {
	_, cfg := testFixture(t)
	c, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	status := 429
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"0"}},
			Body: io.NopCloser(strings.NewReader(`{"ok":false}`))}, nil
	})
	_, err = c.call(context.Background(), "documents.list", map[string]interface{}{})
	if err == nil || calls != 4 {
		t.Fatalf("retry bound: %d, %v", calls, err)
	}
	status, calls = 401, 0
	_, err = c.call(context.Background(), "documents.list", map[string]interface{}{})
	if !fatal(err) || calls != 1 {
		t.Fatalf("authentication should abort: %d, %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInstanceBindingRejectsWorkspaceChanges(t *testing.T) {
	f, cfg := testFixture(t)
	c := NewConnector()
	bound, err := c.BindIdentity(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = "other-team"
	if _, err := c.BindIdentity(context.Background(), cfg, bound); err == nil {
		t.Fatal("workspace migration accepted")
	}
	f.workspace, f.actor = "team", "new-actor"
	s, _ := parseCursor(bound)
	s.Documents["a"] = version{CollectionID: collectionA, Fingerprint: "fp"}
	s.Run = newRun(false)
	next, err := c.BindIdentity(context.Background(), cfg, s.cursor())
	if err != nil {
		t.Fatal(err)
	}
	s, _ = parseCursor(next)
	if s.Run != nil || len(s.Documents) != 1 || s.Instance.ActorID != f.actor {
		t.Fatal("actor rotation lost baseline or reused old run")
	}
}
