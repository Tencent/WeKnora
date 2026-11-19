package confluence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type attachmentAPI struct {
	streamAPI
	version        int
	filename       string
	removed        bool
	failDownload   bool
	failList       bool
	listNext       bool
	missingResults bool
	cancelDownload context.CancelFunc
	downloads      int
}

func (a *attachmentAPI) response(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/wiki/download/attachments/") {
		a.downloads++
		if a.cancelDownload != nil {
			a.cancelDownload()
			return nil, req.Context().Err()
		}
		if a.failDownload {
			return &http.Response{
				StatusCode: 403, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/pdf"}},
			Body: io.NopCloser(strings.NewReader("%PDF-1.7 fixture")),
		}, nil
	}
	if strings.HasSuffix(req.URL.Path, "/child/attachment") || strings.HasSuffix(req.URL.Path, "/attachments") {
		if a.failList || req.URL.Query().Get("cursor") == "second" {
			return &http.Response{
				StatusCode: 403, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		}
		items := []any{}
		filename := a.filename
		if filename == "" {
			filename = "checklist.pdf"
		}
		if !a.removed && !strings.Contains(req.URL.Path, "p2") {
			items = append(items, map[string]any{
				"id": "a1", "title": filename, "mediaType": "application/pdf",
				"version":      map[string]any{"number": a.version},
				"metadata":     map[string]any{"mediaType": "application/pdf"},
				"_links":       map[string]any{"download": "/wiki/download/attachments/p1/checklist.pdf"},
				"downloadLink": "/wiki/download/attachments/p1/checklist.pdf",
			})
		}
		result := map[string]any{"results": items}
		if a.missingResults {
			delete(result, "results")
		}
		if a.listNext {
			result["_links"] = map[string]any{"next": req.URL.Path + "?cursor=second"}
		}
		return a.jsonResponse(result)
	}
	return a.streamAPI.response(req)
}

func attachmentConnector(a *attachmentAPI) *Connector {
	return &Connector{newClient: func(cfg config) (*client, error) {
		if a.cloud {
			cfg.edition = editionCloud
		}
		return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(a.response)}}, nil
	}}
}

func TestAttachmentsIndependentVersionAndRetry(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		name := "server"
		if cloud {
			name = "cloud"
		}
		t.Run(name, func(t *testing.T) {
			a := &attachmentAPI{
				streamAPI: streamAPI{
					cloud: cloud,
					pages: []streamPage{{id: "p1", title: "Guide", version: 1}},
				},
				version: 1,
			}
			cfg := streamConfig()
			if cloud {
				cfg = cloudStreamConfig()
			}
			cfg.Settings = map[string]interface{}{"sync_attachments": true}
			c := attachmentConnector(a)
			h := &captureHandler{}
			cur, err := c.FetchStream(context.Background(), cfg, nil, h)
			require.NoError(t, err)
			require.Len(t, h.items, 2)
			child := h.items[0]
			if child.ContentType != "application/pdf" {
				child = h.items[1]
			}
			require.Equal(t, "application/pdf", child.ContentType)
			require.Equal(t, types.SubtreeChildID("p1", "file", "a1"), child.ExternalID)
			require.Contains(t, string(child.Content), "%PDF")
			h = &captureHandler{}
			next, err := c.FetchStream(context.Background(), cfg, cur, h)
			require.NoError(t, err)
			require.Empty(t, h.items)
			require.Equal(t, 1, a.downloads)
			// A new attachment revision does not change the page version.
			a.version = 2
			a.failDownload = true
			h = &captureHandler{}
			_, err = c.FetchStream(context.Background(), cfg, next, h)
			require.Error(t, err)
			require.NotEmpty(t, h.items)
			require.NotEmpty(t, h.items[0].Metadata["error"])
			a.failDownload = false
			h = &captureHandler{}
			cur, err = c.FetchStream(context.Background(), cfg, next, h)
			require.NoError(t, err)
			require.Len(t, h.items, 1)
			require.Equal(t, child.ExternalID, h.items[0].ExternalID)
			// An incomplete listing cannot turn existing attachments into deletions.
			a.removed = true
			a.failList = true
			h = &captureHandler{}
			_, err = c.FetchStream(context.Background(), cfg, cur, h)
			require.Error(t, err)
			require.Empty(t, h.items)
			a.failList = false
			h = &captureHandler{emitErr: errors.New("delete failed")}
			_, err = c.FetchStream(context.Background(), cfg, cur, h)
			require.EqualError(t, err, "delete failed")
			h = &captureHandler{}
			cur, err = c.FetchStream(context.Background(), cfg, cur, h)
			require.NoError(t, err)
			require.Len(t, h.items, 1)
			require.True(t, h.items[0].IsDeleted)
			require.Equal(t, child.ExternalID, h.items[0].ExternalID)
			h = &captureHandler{}
			_, err = c.FetchStream(context.Background(), cfg, cur, h)
			require.NoError(t, err)
			require.Empty(t, h.items)
		})
	}
}

func TestAttachmentsDisabledByDefault(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	h := &captureHandler{}
	_, err := attachmentConnector(a).FetchStream(context.Background(), streamConfig(), nil, h)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.Zero(t, a.downloads)
}

func TestAttachmentsFullRetryResumesConfirmedChildren(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	old, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	h := &captureHandler{succeedFirst: 1}
	_, err = c.FetchFullStream(context.Background(), cfg, old, h)
	require.Error(t, err)
	require.Len(t, h.checkpoints, 1)
	require.Equal(t, 2, a.downloads)
	h2 := &captureHandler{}
	_, err = c.FetchFullStream(context.Background(), cfg, h.checkpoints[0], h2)
	require.NoError(t, err)
	require.Equal(t, 2, a.downloads, "full retry must not replay an acknowledged child")
	require.Len(t, h2.items, 1)
	require.Equal(t, "text/markdown", h2.items[0].ContentType)
}

func TestAttachmentsBodyFailureStillTracksDeletedOwner(t *testing.T) {
	a := &attachmentAPI{
		streamAPI: streamAPI{
			pages:      []streamPage{{id: "p1", title: "Guide", version: 1}, {id: "p2", title: "Control", version: 1}},
			bodyStatus: map[string]int{"p1": 403},
		},
		version: 1,
	}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	h := &captureHandler{}
	cur, err := c.FetchStream(context.Background(), cfg, nil, h)
	require.NoError(t, err)
	require.NotEmpty(t, h.checkpoints)
	a.pages = a.pages[1:]
	h = &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.NoError(t, err)
	found := false
	for _, item := range h.items {
		if item.ExternalID == types.SubtreeChildID("p1", "file", "a1") && item.IsDeleted {
			found = true
		}
	}
	require.True(t, found, "attachment owner must be tracked even if its body was never accepted")
}

func TestAttachmentsIncompletePaginationCannotDelete(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.removed = true
	a.listNext = true
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.Error(t, err)
	require.Empty(t, h.items)
	require.Empty(t, h.checkpoints)
}

func TestAttachmentsCancellationDoesNotAcknowledge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &attachmentAPI{
		streamAPI: streamAPI{
			pages: []streamPage{{id: "p1", title: "Guide", version: 1}},
		},
		version: 1, cancelDownload: cancel,
	}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	h := &captureHandler{}
	_, err := attachmentConnector(a).FetchStream(ctx, cfg, nil, h)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, h.items)
	require.Empty(t, h.checkpoints)
	a.cancelDownload = nil
	_, err = attachmentConnector(a).FetchStream(context.Background(), cfg, nil, h)
	require.NoError(t, err)
	require.Len(t, h.items, 2)
}

func TestAttachmentsDisabledFullSyncRetainsDeletionBaseline(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	cfg.Settings["sync_attachments"] = false
	cur, err = c.FetchFullStream(context.Background(), cfg, cur, &captureHandler{})
	require.NoError(t, err)
	cfg.Settings["sync_attachments"] = true
	a.removed = true
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.True(t, h.items[0].IsDeleted)
}

func TestAttachmentDownloadBoundsAndHTML(t *testing.T) {
	for _, scenario := range []string{"declared size", "stream size", "html", "empty", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "stream size":
					_, _ = io.Copy(w, io.LimitReader(&attachmentZeroReader{}, maxAttachmentBytes+1))
				case "html":
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "<html>Login</html>")
				case "redirect":
					http.Redirect(w, r, "https://other.test/private", http.StatusFound)
				}
			}))
			defer server.Close()
			c := &client{cfg: config{baseURL: server.URL}, http: server.Client()}
			a := attachment{DownloadLink: "/file", Title: "file.pdf"}
			if scenario == "declared size" {
				a.FileSize = maxAttachmentBytes + 1
			}
			_, err := c.downloadAttachment(context.Background(), a)
			require.Error(t, err)
			switch scenario {
			case "declared size", "stream size":
				require.ErrorContains(t, err, "exceeds")
			case "html", "empty":
				require.ErrorContains(t, err, "HTML")
			case "redirect":
				require.ErrorIs(t, err, errImageRedirectOffOrigin)
			}
		})
	}
}

type attachmentZeroReader struct{}

func (*attachmentZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestAttachmentsUnsupportedRenamePreservesPriorFile(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.filename = "checklist.bin"
	a.version = 2
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.Error(t, err)
	for _, item := range h.items {
		require.False(t, item.IsDeleted)
	}
	require.Empty(t, h.checkpoints)
}

func TestAttachmentsMalformedListingCannotDelete(t *testing.T) {
	a := &attachmentAPI{streamAPI: streamAPI{pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}, version: 1}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.missingResults = true
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.Error(t, err)
	require.Empty(t, h.items)
}

func TestAttachmentsBodyFailureEmptyRootUsesDeletionGuard(t *testing.T) {
	a := &attachmentAPI{
		streamAPI: streamAPI{
			pages:      []streamPage{{id: "p1", title: "Guide", version: 1}},
			bodyStatus: map[string]int{"p1": 403},
		},
		version: 1,
	}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.pages = nil
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.Error(t, err)
	require.Empty(t, h.items)
}

func TestAttachmentsFullPartialListingRetainsVerifiedPages(t *testing.T) {
	a := &attachmentAPI{
		streamAPI: streamAPI{
			pages: []streamPage{{id: "p1", title: "Guide", version: 1}, {id: "p2", title: "Control", version: 1}},
		},
		version: 1,
	}
	cfg := streamConfig()
	cfg.Settings = map[string]interface{}{"sync_attachments": true}
	c := attachmentConnector(a)
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.pages = a.pages[1:]
	a.endlessNext = true
	a.listCalls = 0
	a.probe = map[string]probeReply{"p1": {status: 200, spaceKey: "ENG"}}
	cur, err = c.FetchFullStream(context.Background(), cfg, cur, &captureHandler{})
	require.NoError(t, err)
	a.endlessNext = false
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.NoError(t, err)
	found := false
	for _, item := range h.items {
		if item.ExternalID == types.SubtreeChildID("p1", "file", "a1") && item.IsDeleted {
			found = true
		}
	}
	require.True(t, found, "a verified but unlisted page must keep its attachment deletion baseline")
}
