package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

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
	cloud      bool
	pages      []streamPage
	bodyCalls  int
	bodyStatus map[string]int
	listCalls  int
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
		return a.jsonResponse(map[string]any{
			"results": []any{map[string]any{
				"id": 1, "key": "ENG", "name": "Engineering",
				"_links": map[string]any{"webui": "/wiki/spaces/ENG"},
			}},
		})
	case path == "/wiki/rest/api/space/ENG/content/page":
		a.listCalls++
		return a.jsonResponse(map[string]any{
			"results": a.pageMaps(),
		})
	case strings.HasPrefix(path, "/wiki/rest/api/content/"):
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
	case strings.HasPrefix(path, "/wiki/api/v2/pages/"):
		if req.URL.Query().Get("body-format") != "view" {
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

func TestFetchStreamDetectsDeletedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), old, h); err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || !h.items[0].IsDeleted || h.items[0].ExternalID != "p2" {
		t.Fatalf("deletion items = %#v", h.items)
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
