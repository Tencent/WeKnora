package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type streamPage struct {
	id      string
	title   string
	version int
}

type streamAPI struct {
	cloud         bool
	pages         []streamPage
	bodyCalls     int
	bodyStatus    map[string]int
	listCalls     int
	ancestorCalls int
	spaceCalls    int
}

func (a *streamAPI) jsonResponse(value any) (*http.Response, error) {
	body, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func (a *streamAPI) pageMaps() []any {
	pages := make([]any, 0, len(a.pages))
	for _, page := range a.pages {
		pages = append(pages, map[string]any{
			"id": page.id, "title": page.title,
			"version": map[string]any{"number": page.version},
			"space":   map[string]any{"key": "ENG", "name": "Engineering"},
			"_links":  map[string]any{"webui": "/wiki/pages/" + page.id},
		})
	}
	return pages
}

func (a *streamAPI) response(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	switch {
	case path == "/wiki/rest/api/space":
		a.spaceCalls++
		return a.jsonResponse(map[string]any{
			"results": []any{map[string]any{
				"id": 1, "key": "ENG", "name": "Engineering",
				"_links": map[string]any{"webui": "/wiki/spaces/ENG"},
			}},
		})
	case path == "/wiki/rest/api/space/ENG/content/page":
		a.listCalls++
		if req.URL.Query().Get("depth") == "root" {
			return a.jsonResponse(map[string]any{"results": a.pageMaps()})
		}
		return a.jsonResponse(map[string]any{
			"results": a.pageMaps(),
		})
	case strings.HasPrefix(path, "/wiki/rest/api/content/") && strings.HasSuffix(path, "/child/page"):
		return a.jsonResponse(map[string]any{"results": []any{}})
	case strings.HasPrefix(path, "/wiki/rest/api/content/"):
		if req.URL.Query().Get("expand") == "ancestors,space" {
			a.ancestorCalls++
			return a.jsonResponse(map[string]any{
				"id":        strings.TrimPrefix(path, "/wiki/rest/api/content/"),
				"space":     map[string]any{"key": "ENG", "name": "Engineering"},
				"ancestors": []any{},
			})
		}
		if req.URL.Query().Get("expand") == "version,space" {
			id := strings.TrimPrefix(path, "/wiki/rest/api/content/")
			for _, page := range a.pages {
				if page.id == id {
					return a.jsonResponse(map[string]any{"id": page.id, "title": page.title, "version": map[string]any{"number": page.version}, "space": map[string]any{"key": "ENG", "name": "Engineering"}})
				}
			}
			return nil, errors.New("missing page")
		}
		if req.URL.Query().Get("expand") != "body.view,version,space" {
			return nil, errors.New("page body did not request rendered body.view")
		}
		return a.pageBody(strings.TrimPrefix(path, "/wiki/rest/api/content/"))
	case path == "/wiki/api/v2/spaces":
		return a.jsonResponse(map[string]any{
			"results": []any{map[string]any{
				"id": "1", "key": "ENG", "name": "Engineering",
				"_links": map[string]any{"webui": "/spaces/ENG"},
			}},
		})
	case path == "/wiki/api/v2/spaces/1/pages":
		a.listCalls++
		if req.URL.Query().Get("depth") != "all" {
			return nil, errors.New("cloud page listing omitted depth=all")
		}
		pages := make([]any, 0, len(a.pages))
		for _, page := range a.pages {
			pages = append(pages, map[string]any{
				"id": page.id, "title": page.title, "status": "current",
				"version": map[string]any{"number": page.version, "createdAt": "2026-01-02T03:04:05Z"},
				"_links":  map[string]any{"webui": "/spaces/ENG/pages/" + page.id},
			})
		}
		return a.jsonResponse(map[string]any{"results": pages})
	case strings.HasPrefix(path, "/wiki/api/v2/pages/") && strings.HasSuffix(path, "/ancestors"):
		return a.jsonResponse(map[string]any{"results": []any{}})
	case strings.HasPrefix(path, "/wiki/api/v2/pages/") && strings.HasSuffix(path, "/descendants"):
		return a.jsonResponse(map[string]any{"results": []any{}})
	case strings.HasPrefix(path, "/wiki/api/v2/pages/") && strings.HasSuffix(path, "/direct-children"):
		return a.jsonResponse(map[string]any{"results": []any{}})
	case strings.HasPrefix(path, "/wiki/api/v2/pages/"):
		if req.URL.Query().Get("body-format") != "" && req.URL.Query().Get("body-format") != "view" {
			return nil, errors.New("cloud page body did not request body-format=view")
		}
		return a.pageBody(strings.TrimPrefix(path, "/wiki/api/v2/pages/"))
	default:
		return nil, errors.New("unexpected Confluence endpoint: " + req.URL.String())
	}
}

func (a *streamAPI) pageBody(id string) (*http.Response, error) {
	a.bodyCalls++
	if status := a.bodyStatus[id]; status != 0 && status != http.StatusOK {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("missing page")),
		}, nil
	}
	for _, page := range a.pages {
		if page.id == id {
			return a.jsonResponse(map[string]any{
				"id": page.id, "title": page.title,
				"spaceId": "1",
				"version": map[string]any{"number": page.version, "by": map[string]any{"displayName": "Ada"}},
				"space":   map[string]any{"key": "ENG", "name": "Engineering"},
				"_links":  map[string]any{"webui": "/wiki/pages/" + page.id},
				"body":    map[string]any{"view": map[string]any{"value": "<p>" + page.title + "</p>"}},
			})
		}
	}
	return nil, errors.New("missing Confluence page")
}

func newStreamConnector(api *streamAPI) *Connector {
	return &Connector{newClient: func(cfg config) (*client, error) {
		cfgCopy := cfg
		if api.cloud {
			cfgCopy.edition = editionCloud
		}
		return &client{cfg: cfgCopy, http: &http.Client{Transport: roundTripper(api.response)}}, nil
	}}
}

func streamConfig() *types.DataSourceConfig {
	return &types.DataSourceConfig{ResourceIDs: []string{"1"}, Credentials: map[string]interface{}{
		"base_url": "https://confluence.test/wiki", "username": "reader", "password": "secret",
	}}
}

func cloudStreamConfig() *types.DataSourceConfig {
	return &types.DataSourceConfig{ResourceIDs: []string{"1"}, Credentials: map[string]interface{}{
		"edition":   "cloud",
		"base_url":  "https://confluence.test/wiki",
		"username":  "reader@example.com",
		"api_token": "token",
	}}
}

type captureHandler struct {
	items        []types.FetchedItem
	checkpoints  []*types.SyncCursor
	emitErr      error
	succeedFirst int
}

func (h *captureHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		return h.emitErr
	}
	if h.succeedFirst > 0 && len(h.items) >= h.succeedFirst {
		return errors.New("ingest failed")
	}
	h.items = append(h.items, item)
	return nil
}

func (h *captureHandler) Checkpoint(_ context.Context, c *types.SyncCursor) error {
	raw, _ := json.Marshal(c)
	var stored types.SyncCursor
	_ = json.Unmarshal(raw, &stored)
	h.checkpoints = append(h.checkpoints, &stored)
	return nil
}

var _ datasource.StreamHandler = (*captureHandler)(nil)

func streamCursor(pages map[string]string) *types.SyncCursor {
	return (&cursor{SpacePages: map[string]map[string]string{"1": pages}}).syncCursor()
}

func TestFetchStreamCheckpointsOnlyAfterSuccessfulEmit(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{emitErr: errors.New("ingest failed")}
	_, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), nil, h)
	if err == nil || len(h.checkpoints) != 0 {
		t.Fatalf("FetchStream() err=%v checkpoints=%d; failed emit must not advance cursor", err, len(h.checkpoints))
	}
}

func TestFetchStreamSkipsUnchangedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "v:1"})
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, h); err != nil {
		t.Fatal(err)
	}
	if api.bodyCalls != 0 || len(h.items) != 0 {
		t.Fatalf("unchanged page was fetched=%d emitted=%d", api.bodyCalls, len(h.items))
	}
}

func TestFetchStreamUnknownVersionDoesNotSkipBody(t *testing.T) {
	// version.number=0 with no when/createdAt leaves the version unknown; the
	// legacy stable "t:" cursor value must not suppress the body refresh.
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 0}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "t:"})
	next, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, h)
	if err != nil {
		t.Fatal(err)
	}
	if api.bodyCalls != 1 || len(h.items) != 1 || h.items[0].IsDeleted {
		t.Fatalf("unknown-version page fetched=%d items=%#v", api.bodyCalls, h.items)
	}
	if decoded := decodeCursor(next); decoded.SpacePages["1"]["p1"] != "" {
		t.Fatalf("cursor stored a stable unknown version: %#v", decoded)
	}
}

func TestFetchStreamDetectsDeletedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	next, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || !h.items[0].IsDeleted || h.items[0].ExternalID != "p2" {
		t.Fatalf("deletion items = %#v", h.items)
	}
	if decoded := decodeCursor(next); decoded.SpacePages["1"]["p2"] != "" {
		t.Fatalf("deleted page stayed in cursor as a ghost: %#v", decoded)
	}
}

func TestFetchStreamRemovesDeletedPageFromCursor(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	next, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || !h.items[0].IsDeleted || h.items[0].ExternalID != "p2" {
		t.Fatalf("deletion items = %#v", h.items)
	}
	decoded := decodeCursor(next)
	if decoded.SpacePages["1"]["p1"] != "v:1" {
		t.Fatalf("surviving page missing from cursor: %#v", decoded)
	}
	for _, checkpoint := range h.checkpoints {
		if stored := decodeCursor(checkpoint); stored.SpacePages["1"]["p2"] != "" {
			t.Fatalf("checkpoint recorded a not-yet-deleted page as deleted: %#v", stored)
		}
	}
	// A follow-up run must not re-emit the already tombstoned page.
	followUp := &captureHandler{}
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), next, followUp); err != nil {
		t.Fatal(err)
	}
	for _, item := range followUp.items {
		if item.IsDeleted {
			t.Fatalf("ghost page re-tombstoned on the next run: %#v", followUp.items)
		}
	}
}

func TestFetchStreamTombstoneResumeSkipsCompletedDeletions(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1", "p3": "v:1"})
	first := &captureHandler{succeedFirst: 1}
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, first); err == nil {
		t.Fatal("interrupted tombstone run unexpectedly succeeded")
	}
	if len(first.items) != 1 || !first.items[0].IsDeleted || first.items[0].ExternalID != "p2" {
		t.Fatalf("first run items = %#v", first.items)
	}
	if len(first.checkpoints) != 1 {
		t.Fatalf("first run checkpoints = %d; want 1", len(first.checkpoints))
	}
	second := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), first.checkpoints[0], second)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.items) != 1 || !second.items[0].IsDeleted || second.items[0].ExternalID != "p3" {
		t.Fatalf("resume must tombstone only the remaining page: %#v", second.items)
	}
	if decoded := decodeCursor(next); decoded.SpacePages["1"]["p1"] != "v:1" || decoded.SpacePages["1"]["p3"] != "" {
		t.Fatalf("resume cursor = %#v", decoded)
	}
}

func TestFetchFullStreamTombstoneResumeSkipsCompletedDeletions(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1", "p3": "v:1"})
	// p1 re-fetch succeeds and the p2 tombstone is checkpointed before the
	// worker dies on the p3 tombstone.
	first := &captureHandler{succeedFirst: 2}
	if _, err := newStreamConnector(api).FetchFullStream(context.Background(), streamConfig(), old, first); err == nil {
		t.Fatal("interrupted full-sync tombstone run unexpectedly succeeded")
	}
	if len(first.items) != 2 || first.items[1].ExternalID != "p2" || !first.items[1].IsDeleted {
		t.Fatalf("first run items = %#v", first.items)
	}
	if len(first.checkpoints) != 2 {
		t.Fatalf("first run checkpoints = %d; want 2", len(first.checkpoints))
	}
	deletionCheckpoint := decodeCursor(first.checkpoints[1])
	if !deletionCheckpoint.FullSync {
		t.Fatalf("mid-full-sync checkpoint lost the resume flag: %#v", deletionCheckpoint)
	}
	if baseline := deletionCheckpoint.FullSyncBaseline["1"]; baseline["p1"] != "v:1" || baseline["p2"] != "" || baseline["p3"] != "v:1" {
		t.Fatalf("tombstone checkpoint did not advance the deletion baseline: %#v", deletionCheckpoint)
	}

	resumeAPI := &streamAPI{pages: api.pages}
	second := &captureHandler{}
	next, err := newStreamConnector(resumeAPI).FetchFullStream(context.Background(), streamConfig(), first.checkpoints[1], second)
	if err != nil {
		t.Fatal(err)
	}
	if resumeAPI.bodyCalls != 0 {
		t.Fatalf("resume re-fetched %d already synced page bodies", resumeAPI.bodyCalls)
	}
	if len(second.items) != 1 || !second.items[0].IsDeleted || second.items[0].ExternalID != "p3" {
		t.Fatalf("resume must tombstone only the remaining page: %#v", second.items)
	}
	decoded := decodeCursor(next)
	if decoded.FullSync || decoded.FullSyncBaseline != nil {
		t.Fatalf("completed full sync left resume fields set: %#v", decoded)
	}
	if decoded.SpacePages["1"]["p1"] != "v:1" || decoded.SpacePages["1"]["p2"] != "" || decoded.SpacePages["1"]["p3"] != "" {
		t.Fatalf("completed resume cursor = %#v", decoded)
	}
}

func TestFetchStreamRefusesMassDeletion(t *testing.T) {
	prior := make(map[string]string, 20)
	for i := 0; i < 20; i++ {
		prior["p"+string(rune('0'+i))] = "v:1"
	}
	api := &streamAPI{}
	h := &captureHandler{}
	_, err := newStreamConnector(api).FetchStream(
		context.Background(), streamConfig(), streamCursor(prior), h,
	)
	if err == nil || len(h.items) != 0 {
		t.Fatalf("mass deletion err=%v items=%#v", err, h.items)
	}
}

func TestFetchStreamRefusesMassDeletionInSurvivingScope(t *testing.T) {
	prior := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		prior[fmt.Sprintf("p%03d", i)] = "v:1"
	}
	// The selection is unchanged; a broken listing suddenly returns only five
	// of the hundred previously synced pages.
	listed := make([]streamPage, 0, 5)
	for i := 0; i < 5; i++ {
		listed = append(listed, streamPage{id: fmt.Sprintf("p%03d", i), title: "Page", version: 1})
	}
	api := &streamAPI{pages: listed}
	h := &captureHandler{}
	_, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), streamCursor(prior), h)
	if err == nil || len(h.items) != 0 {
		t.Fatalf("surviving-scope mass deletion err=%v items=%#v", err, h.items)
	}
}

func TestFetchStreamRefusesMassDeletionWhenSelectionShrinks(t *testing.T) {
	prior := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		prior[fmt.Sprintf("p%03d", i)] = "v:1"
	}
	// The user narrows a whole-space selection down to a single page subtree.
	// This is still a broad mirror deletion and requires an explicit release
	// policy rather than silently emitting one hundred tombstones.
	api := &streamAPI{pages: []streamPage{{id: "erp", title: "ERP", version: 2}}}
	ds := streamConfig()
	ds.ResourceIDs = []string{"page:1:erp"}
	h := &captureHandler{}
	_, err := newStreamConnector(api).FetchStream(context.Background(), ds, streamCursor(prior), h)
	if err == nil {
		t.Fatal("selection shrink unexpectedly bypassed the mass-deletion guard")
	}
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatalf("selection shrink emitted tombstone: %#v", h.items)
		}
	}
}

func TestFetchStreamRefusesEmptyListingWhenBaselineExists(t *testing.T) {
	prior := map[string]string{"p1": "v:1", "p2": "v:1"}
	api := &streamAPI{}
	h := &captureHandler{}

	_, err := newStreamConnector(api).FetchStream(
		context.Background(), streamConfig(), streamCursor(prior), h,
	)
	if err == nil || len(h.items) != 0 {
		t.Fatalf("empty listing err=%v items=%#v", err, h.items)
	}
}

func TestFetchFullStreamRefusesEmptyListingWhenBaselineExists(t *testing.T) {
	prior := map[string]string{"p1": "v:1", "p2": "v:1"}
	h := &captureHandler{}
	_, err := newStreamConnector(&streamAPI{}).FetchFullStream(
		context.Background(), streamConfig(), streamCursor(prior), h,
	)
	if err == nil || len(h.items) != 0 {
		t.Fatalf("full-sync empty listing err=%v items=%#v", err, h.items)
	}
}

func TestFetchFullStreamRefetchesUnchangedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "v:1"})
	next, err := newStreamConnector(api).FetchFullStream(context.Background(), streamConfig(), old, h)
	if err != nil {
		t.Fatal(err)
	}
	if api.bodyCalls != 1 || len(h.items) != 1 || h.items[0].IsDeleted {
		t.Fatalf("full sync fetched=%d items=%#v", api.bodyCalls, h.items)
	}
	if decoded := decodeCursor(next); decoded.FullSync || decoded.FullSyncBaseline != nil {
		t.Fatalf("completed full sync left resume fields set: %#v", decoded)
	}
}

func TestFetchFullStreamResumesFromCheckpoint(t *testing.T) {
	api := &streamAPI{pages: []streamPage{
		{id: "p1", title: "One", version: 1},
		{id: "p2", title: "Two", version: 1},
	}}
	first := &captureHandler{succeedFirst: 1}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	_, err := newStreamConnector(api).FetchFullStream(context.Background(), streamConfig(), old, first)
	if err == nil || len(first.checkpoints) != 1 {
		t.Fatalf("first full sync err=%v checkpoints=%d", err, len(first.checkpoints))
	}
	checkpoint := decodeCursor(first.checkpoints[0])
	if !checkpoint.FullSync || checkpoint.SpacePages["1"]["p1"] != "v:1" || checkpoint.SpacePages["1"]["p2"] != "" {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}

	resumeAPI := &streamAPI{pages: api.pages}
	second := &captureHandler{}
	conn := newStreamConnector(resumeAPI)
	next, err := conn.FetchFullStream(context.Background(), streamConfig(), first.checkpoints[0], second)
	if err != nil {
		t.Fatal(err)
	}
	if resumeAPI.bodyCalls != 1 {
		t.Fatalf("resume re-fetched %d page bodies, want 1", resumeAPI.bodyCalls)
	}
	if len(second.items) != 1 || second.items[0].ExternalID != "p2" {
		t.Fatalf("resume items = %#v", second.items)
	}
	if decoded := decodeCursor(next); decoded.FullSync || decoded.SpacePages["1"]["p2"] != "v:1" {
		t.Fatalf("completed resume cursor = %#v", decoded)
	}
}

func TestFetchStreamContinuesAfterPageBodyError(t *testing.T) {
	api := &streamAPI{
		pages: []streamPage{
			{id: "p1", title: "Broken", version: 1},
			{id: "p2", title: "Healthy", version: 1},
		},
		bodyStatus: map[string]int{"p1": http.StatusNotFound},
	}
	h := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 2 {
		t.Fatalf("items = %#v", h.items)
	}
	if h.items[0].Metadata["error_reason_code"] != "confluence_not_found" ||
		strings.Contains(h.items[0].Metadata["error_reason"], "missing page") ||
		len(h.items[0].Content) != 0 {
		t.Fatalf("broken page = %#v", h.items[0])
	}
	if string(h.items[1].Content) == "" || h.items[1].ExternalID != "p2" {
		t.Fatalf("healthy page = %#v", h.items[1])
	}
	decoded := decodeCursor(next)
	if _, ok := decoded.SpacePages["1"]["p1"]; ok {
		t.Fatal("failed page must not enter the cursor")
	}
	if decoded.SpacePages["1"]["p2"] != "v:1" {
		t.Fatalf("healthy page missing from cursor: %#v", decoded)
	}
}

func TestFetchStreamCloudUsesV2Body(t *testing.T) {
	api := &streamAPI{cloud: true, pages: []streamPage{{id: "p1", title: "Cloud", version: 3}}}
	h := &captureHandler{}
	if _, err := newStreamConnector(api).FetchStream(context.Background(), cloudStreamConfig(), nil, h); err != nil {
		t.Fatal(err)
	}
	if api.listCalls != 1 || api.bodyCalls != 1 || len(h.items) != 1 {
		t.Fatalf("cloud sync list=%d body=%d items=%d", api.listCalls, api.bodyCalls, len(h.items))
	}
	if !strings.Contains(string(h.items[0].Content), "Cloud") {
		t.Fatalf("cloud markdown = %q", h.items[0].Content)
	}
	if h.items[0].FileName != "Cloud-p1.md" {
		t.Fatalf("cloud filename = %q", h.items[0].FileName)
	}
}

func TestListResourcesKeepsInvalidWebUI(t *testing.T) {
	api := &streamAPI{}
	connector := newStreamConnector(api)
	resources, err := connector.ListResources(context.Background(), streamConfig(), "")
	if err != nil || len(resources) != 1 || resources[0].URL == "" {
		t.Fatalf("ListResources() = %#v, %v", resources, err)
	}

	foreign := &Connector{newClient: func(cfg config) (*client, error) {
		transport := roundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/wiki/rest/api/space" {
				return nil, errors.New(req.URL.String())
			}
			body, _ := json.Marshal(map[string]any{"results": []any{map[string]any{
				"id": 1, "key": "ENG", "name": "Engineering",
				"_links": map[string]any{"webui": "https://attacker.example/wiki"},
			}}})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		})
		return &client{cfg: cfg, http: &http.Client{Transport: transport}}, nil
	}}
	resources, err = foreign.ListResources(context.Background(), streamConfig(), "")
	if err != nil || len(resources) != 1 || resources[0].URL != "https://confluence.test/wiki" {
		t.Fatalf("ListResources() with hostile webui = %#v, %v", resources, err)
	}
}

func TestListResourcesLoadsConfluencePagesLazily(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Home", version: 1}}}
	connector := newStreamConnector(api)
	spaces, err := connector.ListResources(context.Background(), streamConfig(), "")
	if err != nil || len(spaces) != 1 || !spaces[0].HasChildren {
		t.Fatalf("spaces = %#v, %v", spaces, err)
	}
	pages, err := connector.ListResources(context.Background(), streamConfig(), "1")
	if err != nil || len(pages) != 1 {
		t.Fatalf("top pages = %#v, %v", pages, err)
	}
	if pages[0].ExternalID != "page:1:p1" || pages[0].ParentID != "1" || pages[0].Type != "page" || !pages[0].HasChildren {
		t.Fatalf("page resource = %#v", pages[0])
	}
	children, err := connector.ListResources(context.Background(), streamConfig(), "page:1:p1")
	if err != nil || len(children) != 0 {
		t.Fatalf("children = %#v, %v", children, err)
	}
	if api.listCalls != 1 {
		t.Fatalf("space expansion made %d whole-space list calls", api.listCalls)
	}
	if api.ancestorCalls != 0 {
		t.Fatalf("page expansion made %d unnecessary ancestor calls", api.ancestorCalls)
	}
}

func TestPickerSpaceCacheIsPartitionedByCredentialFingerprint(t *testing.T) {
	api := &streamAPI{}
	connector := newStreamConnector(api)
	client, cfg, err := connector.configured(streamConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.pickerSpaces(context.Background(), client, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.pickerSpaces(context.Background(), client, cfg); err != nil {
		t.Fatal(err)
	}
	rotated := cfg
	rotated.secret = "rotated-password"
	if _, err := connector.pickerSpaces(context.Background(), client, rotated); err != nil {
		t.Fatal(err)
	}
	if api.spaceCalls != 2 {
		t.Fatalf("space list calls = %d; want 2 for distinct credentials", api.spaceCalls)
	}
}

func TestPickerSpaceCachePrunesExpiredCredentialEntries(t *testing.T) {
	api := &streamAPI{}
	connector := newStreamConnector(api)
	client, cfg, err := connector.configured(streamConfig())
	if err != nil {
		t.Fatal(err)
	}
	expired := cfg
	expired.secret = "expired-password"
	connector.spaceCache.entries = map[string]cachedSpaces{
		pickerSpaceCacheKey(expired): {expires: time.Now().Add(-time.Second)},
	}
	if _, err := connector.pickerSpaces(context.Background(), client, cfg); err != nil {
		t.Fatal(err)
	}
	if len(connector.spaceCache.entries) != 1 {
		t.Fatalf("cache entries = %#v; expired credential entry was retained", connector.spaceCache.entries)
	}
}

func TestFetchStreamSyncsSelectedPageSubtree(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Selected", version: 2}}}
	ds := streamConfig()
	ds.ResourceIDs = []string{"page:1:p1"}
	h := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), ds, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || h.items[0].SourceResourceID != "page:1:p1" || h.items[0].ExternalID != "p1" {
		t.Fatalf("items = %#v", h.items)
	}
	if decoded := decodeCursor(next); decoded.SpacePages["page:1:p1"]["p1"] != "v:2" {
		t.Fatalf("cursor = %#v", decoded)
	}
}

func TestFetchStreamReconcilesCancelledSpaceScope(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Selected", version: 2}}}
	ds := streamConfig()
	ds.ResourceIDs = []string{"page:1:p1"}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	h := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), ds, old, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 2 || !h.items[1].IsDeleted || h.items[1].ExternalID != "p2" {
		t.Fatalf("items = %#v", h.items)
	}
	decoded := decodeCursor(next)
	if _, oldScopePresent := decoded.SpacePages["1"]; oldScopePresent || decoded.SpacePages["page:1:p1"]["p1"] != "v:2" {
		t.Fatalf("cursor = %#v", decoded)
	}
}

func TestFetchStreamPreservesMovedPageWhenBodyFails(t *testing.T) {
	api := &streamAPI{
		pages:      []streamPage{{id: "p1", title: "Selected", version: 2}},
		bodyStatus: map[string]int{"p1": http.StatusNotFound},
	}
	ds := streamConfig()
	ds.ResourceIDs = []string{"page:1:p1"}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	h := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), ds, old, h)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeCursor(next)
	if decoded.SpacePages["page:1:p1"]["p1"] != "v:1" {
		t.Fatalf("prior version was not preserved: %#v", decoded)
	}
	deletedP2 := false
	for _, item := range h.items {
		if item.IsDeleted && item.ExternalID == "p1" {
			t.Fatalf("failed page was deleted: %#v", h.items)
		}
		deletedP2 = deletedP2 || (item.IsDeleted && item.ExternalID == "p2")
	}
	if !deletedP2 {
		t.Fatalf("cancelled scope page was not deleted: %#v", h.items)
	}
}

func TestFetchStreamDoesNotDeleteWhenNewScopeCannotEnumerate(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Selected", version: 2}}}
	connector := newStreamConnector(api)
	originalFactory := connector.newClient
	connector.newClient = func(cfg config) (*client, error) {
		c, err := originalFactory(cfg)
		if err != nil {
			return nil, err
		}
		originalTransport := c.http.Transport
		c.http.Transport = roundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/wiki/rest/api/content/p1" && req.URL.Query().Get("expand") == "version,space" {
				return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing"))}, nil
			}
			return originalTransport.RoundTrip(req)
		})
		return c, nil
	}
	ds := streamConfig()
	ds.ResourceIDs = []string{"page:1:p1"}
	h := &captureHandler{}
	_, err := connector.FetchStream(context.Background(), ds, streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"}), h)
	if err == nil {
		t.Fatal("scope enumeration failure unexpectedly succeeded")
	}
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatalf("incomplete scope emitted a tombstone: %#v", h.items)
		}
	}
}

func TestBuildSyncPlanNormalizesOverlappingScopes(t *testing.T) {
	api := &streamAPI{}
	client := &client{cfg: config{baseURL: "https://confluence.test/wiki"}, http: &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/wiki/rest/api/space":
			return api.jsonResponse(map[string]any{"results": []any{map[string]any{"id": 1, "key": "ENG", "name": "Engineering"}}})
		case "/wiki/rest/api/content/parent":
			return api.jsonResponse(map[string]any{"id": "parent", "space": map[string]any{"key": "ENG"}, "ancestors": []any{}})
		case "/wiki/rest/api/content/child":
			return api.jsonResponse(map[string]any{"id": "child", "space": map[string]any{"key": "ENG"}, "ancestors": []any{map[string]any{"id": "parent"}}})
		default:
			return nil, errors.New("unexpected endpoint: " + req.URL.String())
		}
	})}}
	connector := NewConnector()
	plan, err := connector.buildSyncPlan(context.Background(), client, []string{"page:1:child", "page:1:parent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Roots) != 1 || plan.Roots[0].ResourceID != "page:1:parent" {
		t.Fatalf("page overlap plan = %#v", plan)
	}
	plan, err = connector.buildSyncPlan(context.Background(), client, []string{"1", "page:1:child"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Roots) != 1 || plan.Roots[0].Kind != syncWholeSpace || plan.Roots[0].ResourceID != "1" {
		t.Fatalf("space overlap plan = %#v", plan)
	}
}

func TestFetchStreamCloudSyncsSelectedPageSubtree(t *testing.T) {
	api := &streamAPI{cloud: true, pages: []streamPage{{id: "p1", title: "Cloud", version: 3}}}
	ds := cloudStreamConfig()
	ds.ResourceIDs = []string{"page:1:p1"}
	h := &captureHandler{}
	next, err := newStreamConnector(api).FetchStream(context.Background(), ds, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || h.items[0].ExternalID != "p1" || h.items[0].SourceResourceID != "page:1:p1" {
		t.Fatalf("items = %#v", h.items)
	}
	if decoded := decodeCursor(next); decoded.SpacePages["page:1:p1"]["p1"] != "v:3" {
		t.Fatalf("cursor = %#v", decoded)
	}
}

func TestListResourcesCloudStaysLazyWithContainerLimitation(t *testing.T) {
	api := &streamAPI{}
	var listDepths []string
	conn := &Connector{newClient: func(cfg config) (*client, error) {
		return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/wiki/api/v2/spaces":
				return api.jsonResponse(map[string]any{"results": []any{map[string]any{"id": "1", "key": "ENG", "name": "Engineering"}}})
			case "/wiki/api/v2/spaces/1/pages":
				listDepths = append(listDepths, req.URL.Query().Get("depth"))
				// depth=0 surfaces root pages only; the folder and the page nested
				// inside it are unreachable through this endpoint (CONFCLOUD-84275).
				return api.jsonResponse(map[string]any{"results": []any{
					map[string]any{"id": "top", "type": "page", "title": "Top"},
					map[string]any{"id": "folder", "type": "folder", "title": "Folder"},
				}})
			default:
				return nil, errors.New("unexpected endpoint: " + req.URL.String())
			}
		})}}, nil
	}}
	// Cloud spaces carry the limitation marker so the picker can explain why
	// pages under top-level containers are not selectable.
	spaces, err := conn.ListResources(context.Background(), cloudStreamConfig(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(spaces) != 1 || spaces[0].Metadata["hierarchy_limitation"] != "cloud_top_level_containers" {
		t.Fatalf("cloud space metadata = %#v", spaces)
	}
	resources, err := conn.ListResources(context.Background(), cloudStreamConfig(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].ExternalID != "page:1:top" {
		t.Fatalf("resources = %#v", resources)
	}
	// Expanding a space must stay a single shallow call, never a whole-space
	// scan, even though top-level folders hide some pages.
	if len(listDepths) != 1 || listDepths[0] != "0" {
		t.Fatalf("space expansion depths = %#v; want a single depth=0 call", listDepths)
	}
}

func TestResolveResourceAncestorsSkipsUnresolvableSelections(t *testing.T) {
	api := &streamAPI{}
	connector := &Connector{newClient: func(cfg config) (*client, error) {
		return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/wiki/rest/api/space":
				return api.jsonResponse(map[string]any{"results": []any{map[string]any{
					"id": 1, "key": "ENG", "name": "Engineering",
				}}})
			case "/wiki/rest/api/content/alive":
				return api.jsonResponse(map[string]any{
					"id": "alive", "space": map[string]any{"key": "ENG"},
					"ancestors": []any{map[string]any{"id": "parent"}},
				})
			case "/wiki/rest/api/content/gone":
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("missing page")),
				}, nil
			default:
				return nil, errors.New("unexpected endpoint: " + req.URL.String())
			}
		})}}, nil
	}}
	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(), streamConfig(),
		[]string{"page:1:alive", "page:1:gone", "page:9:elsewhere", "not-a-resource"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "page:1:parent"}
	if len(ancestors) != len(want) || ancestors[0] != want[0] || ancestors[1] != want[1] {
		t.Fatalf("ancestors = %#v; want %#v", ancestors, want)
	}
}

func TestVisibleCloudPageChildrenTraversesContainers(t *testing.T) {
	api := &streamAPI{}
	client := &client{cfg: config{edition: editionCloud, baseURL: "https://confluence.test/wiki"}, http: &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/wiki/api/v2/pages/a/direct-children":
			return api.jsonResponse(map[string]any{"results": []any{
				map[string]any{"id": "b", "type": "page", "title": "B"},
				map[string]any{"id": "folder", "type": "folder", "title": "Folder"},
			}})
		case "/wiki/api/v2/folders/folder/direct-children":
			return api.jsonResponse(map[string]any{"results": []any{
				map[string]any{"id": "c", "type": "page", "title": "C"},
				map[string]any{"id": "db", "type": "database", "title": "DB"},
			}})
		case "/wiki/api/v2/databases/db/direct-children":
			return api.jsonResponse(map[string]any{"results": []any{map[string]any{"id": "e", "type": "page", "title": "E"}}})
		default:
			return nil, errors.New("unexpected endpoint: " + req.URL.String())
		}
	})}}
	pages, err := client.visibleCloudPageChildren(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 || pages[0].ID != "b" || pages[1].ID != "c" || pages[2].ID != "e" {
		t.Fatalf("projected pages = %#v", pages)
	}
}
