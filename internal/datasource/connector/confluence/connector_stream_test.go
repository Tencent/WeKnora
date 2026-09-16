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
	pages     []streamPage
	bodyCalls int
}

func (a *streamAPI) response(req *http.Request) (*http.Response, error) {
	var value any
	switch {
	case req.URL.Path == "/wiki/rest/api/space":
		value = map[string]any{"results": []any{map[string]any{"id": 1, "key": "ENG", "name": "Engineering", "_links": map[string]any{"webui": "/wiki/spaces/ENG"}}}}
	case req.URL.Path == "/wiki/rest/api/content/search":
		pages := make([]any, 0, len(a.pages))
		for _, page := range a.pages {
			pages = append(pages, map[string]any{"id": page.id, "title": page.title, "version": map[string]any{"number": page.version}, "space": map[string]any{"key": "ENG", "name": "Engineering"}, "_links": map[string]any{"webui": "/wiki/pages/" + page.id}})
		}
		value = map[string]any{"results": pages}
	case strings.HasPrefix(req.URL.Path, "/wiki/rest/api/content/"):
		if req.URL.Query().Get("expand") != "body.view,version,space" {
			return nil, errors.New("page body did not request rendered body.view")
		}
		a.bodyCalls++
		id := strings.TrimPrefix(req.URL.Path, "/wiki/rest/api/content/")
		for _, page := range a.pages {
			if page.id == id {
				value = map[string]any{"id": page.id, "title": page.title, "version": map[string]any{"number": page.version, "by": map[string]any{"displayName": "Ada"}}, "space": map[string]any{"key": "ENG", "name": "Engineering"}, "_links": map[string]any{"webui": "/wiki/pages/" + page.id}, "body": map[string]any{"view": map[string]any{"value": "<p>" + page.title + "</p>"}}}
				break
			}
		}
	default:
		return nil, errors.New("unexpected Confluence endpoint: " + req.URL.String())
	}
	if value == nil {
		return nil, errors.New("missing Confluence page")
	}
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func newStreamConnector(api *streamAPI) *Connector {
	return &Connector{newClient: func(cfg config) (*client, error) {
		return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(api.response)}}, nil
	}}
}

func streamConfig() *types.DataSourceConfig {
	return &types.DataSourceConfig{ResourceIDs: []string{"1"}, Credentials: map[string]interface{}{
		"base_url": "https://confluence.test/wiki", "username": "reader", "password": "secret",
	}}
}

type captureHandler struct {
	items       []types.FetchedItem
	checkpoints []*types.SyncCursor
	emitErr     error
}

func (h *captureHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		return h.emitErr
	}
	h.items = append(h.items, item)
	return nil
}

func (h *captureHandler) Checkpoint(_ context.Context, c *types.SyncCursor) error {
	raw, _ := json.Marshal(c)
	var copy types.SyncCursor
	_ = json.Unmarshal(raw, &copy)
	h.checkpoints = append(h.checkpoints, &copy)
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
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), streamCursor(map[string]string{"p1": "v:1"}), h); err != nil {
		t.Fatal(err)
	}
	if api.bodyCalls != 0 || len(h.items) != 0 {
		t.Fatalf("unchanged page was fetched=%d emitted=%d", api.bodyCalls, len(h.items))
	}
}

func TestFetchStreamDetectsDeletedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"}), h); err != nil {
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
	_, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), streamCursor(prior), h)
	if err == nil || len(h.items) != 0 {
		t.Fatalf("mass deletion err=%v items=%#v", err, h.items)
	}
}

func TestFetchStreamDeletesSmallBaselineAfterEmptyListing(t *testing.T) {
	prior := map[string]string{"p1": "v:1", "p2": "v:1"}
	api := &streamAPI{}
	h := &captureHandler{}

	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), streamCursor(prior), h); err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 2 || !h.items[0].IsDeleted || !h.items[1].IsDeleted {
		t.Fatalf("empty listing items=%#v", h.items)
	}
}

func TestFetchFullStreamRefetchesUnchangedPages(t *testing.T) {
	api := &streamAPI{pages: []streamPage{{id: "p1", title: "Page", version: 1}}}
	h := &captureHandler{}
	if _, err := newStreamConnector(api).FetchFullStream(context.Background(), streamConfig(), streamCursor(map[string]string{"p1": "v:1"}), h); err != nil {
		t.Fatal(err)
	}
	if api.bodyCalls != 1 || len(h.items) != 1 || h.items[0].IsDeleted {
		t.Fatalf("full sync fetched=%d items=%#v", api.bodyCalls, h.items)
	}
}
