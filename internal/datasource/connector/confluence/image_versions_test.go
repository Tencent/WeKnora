package confluence

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type imageRevisionAPI struct {
	streamAPI
	files           []attachment
	secondPage      []attachment
	attachmentCalls int
	failSecondList  bool
	failImage       bool
	imageBytes      string
}

func (a *imageRevisionAPI) response(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/child/attachment") || strings.HasSuffix(req.URL.Path, "/attachments") {
		a.attachmentCalls++
		if req.URL.Query().Get("cursor") == "second" {
			if a.failSecondList {
				return &http.Response{
					StatusCode: 403, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader("denied")),
				}, nil
			}
			return a.jsonResponse(map[string]any{"results": a.secondPage})
		}
		result := map[string]any{"results": a.files}
		if a.secondPage != nil {
			result["_links"] = map[string]any{"next": req.URL.Path + "?cursor=second"}
		}
		return a.jsonResponse(result)
	}
	if req.URL.Path == "/wiki/download/chart.png" {
		a.imageCalls++
		status := http.StatusOK
		if a.failImage {
			status = http.StatusForbidden
		}
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": []string{"image/png"}},
			Body: io.NopCloser(strings.NewReader(a.imageBytes)),
		}, nil
	}
	return a.streamAPI.response(req)
}

func revisionAttachment(id, name string, version int) attachment {
	a := attachment{ID: id, Title: name, MediaType: "image/png"}
	a.Version.Number = version
	return a
}

func imageRevisionFixture(cloud bool) (*imageRevisionAPI, *Connector, *types.DataSourceConfig) {
	a := &imageRevisionAPI{
		streamAPI: streamAPI{
			cloud: cloud, pages: []streamPage{{id: "p1", title: "Guide", version: 1}},
			bodyHTML: map[string]string{"p1": `<p>Guide</p><img src="/wiki/download/chart.png">`},
		},
		files: []attachment{revisionAttachment("a1", "chart.png", 1)}, imageBytes: "original-png-bytes",
	}
	c := &Connector{newClient: func(cfg config) (*client, error) {
		return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(a.response)}}, nil
	}}
	cfg := streamConfig()
	if cloud {
		cfg = cloudStreamConfig()
	}
	cfg.Settings = map[string]interface{}{"refresh_on_image_attachment_change": true}
	return a, c, cfg
}

func TestImageAttachmentRefreshUpdatesUnchangedPage(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprint(cloud), func(t *testing.T) {
			a, c, cfg := imageRevisionFixture(cloud)
			ctx := context.Background()
			first := &captureHandler{}
			cur, err := c.FetchStream(ctx, cfg, nil, first)
			require.NoError(t, err)
			require.Len(t, first.items, 1)
			require.Contains(t, string(first.items[0].Content), "data:image/png;base64,")
			quiet := &captureHandler{}
			cur, err = c.FetchStream(ctx, cfg, cur, quiet)
			require.NoError(t, err)
			require.Empty(t, quiet.items)
			require.Equal(t, 1, a.bodyCalls)

			// Only the image revision and its bytes change; the page stays at v1.
			a.files[0].Version.Number = 2
			a.imageBytes = "replacement-png-bytes"
			changed := &captureHandler{}
			cur, err = c.FetchStream(ctx, cfg, cur, changed)
			require.NoError(t, err)
			require.Len(t, changed.items, 1)
			require.Equal(t, "p1", changed.items[0].ExternalID)
			require.NotEqual(t, first.items[0].Content, changed.items[0].Content)
			require.Equal(t, "v:1", decodeCursor(cur).SpacePages["1"]["p1"])
			quiet = &captureHandler{}
			cur, err = c.FetchStream(ctx, cfg, cur, quiet)
			require.NoError(t, err)
			require.Empty(t, quiet.items)

			// Removing the last image also changes the fingerprint.
			a.files = []attachment{}
			a.bodyHTML["p1"] = "<p>Guide without image</p>"
			removed := &captureHandler{}
			_, err = c.FetchStream(ctx, cfg, cur, removed)
			require.NoError(t, err)
			require.Len(t, removed.items, 1)
			require.NotContains(t, string(removed.items[0].Content), "data:image")
		})
	}
}

func TestImageAttachmentRefreshRetriesUnacknowledgedChanges(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		for _, failure := range []string{"body", "image", "ingest", "listing"} {
			t.Run(fmt.Sprintf("cloud=%t/%s", cloud, failure), func(t *testing.T) {
				a, c, cfg := imageRevisionFixture(cloud)
				ctx := context.Background()
				cur, err := c.FetchStream(ctx, cfg, nil, &captureHandler{})
				require.NoError(t, err)
				baseline := decodeCursor(cur).PageImageVersions["p1"]
				a.files[0].Version.Number = 2
				a.imageBytes = "new-image"
				failed := &captureHandler{}
				switch failure {
				case "body":
					a.bodyStatus = map[string]int{"p1": http.StatusForbidden}
				case "image":
					a.failImage = true
				case "ingest":
					failed.emitErr = datasource.ErrItemIngestFailed
				case "listing":
					a.secondPage = []attachment{}
					a.failSecondList = true
				}
				retryCursor, err := c.FetchStream(ctx, cfg, cur, failed)
				if failure == "listing" {
					require.Error(t, err)
					require.Empty(t, failed.items, "never ingest from an incomplete attachment listing")
					retryCursor = cur
				} else {
					require.NoError(t, err)
					require.Equal(t, baseline, decodeCursor(retryCursor).PageImageVersions["p1"])
				}
				require.Equal(t, baseline, decodeCursor(cur).PageImageVersions["p1"],
					"do not mutate the caller's cursor")
				a.bodyStatus, a.failImage, a.failSecondList = nil, false, false
				recovered := &captureHandler{}
				_, err = c.FetchStream(ctx, cfg, retryCursor, recovered)
				require.NoError(t, err)
				require.Len(t, recovered.items, 1)
				require.Empty(t, recovered.items[0].Metadata["error"])
			})
		}
	}
}

func TestImageAttachmentRefreshUsesAllPagesAndSharesAttachmentListing(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprint(cloud), func(t *testing.T) {
			a, c, cfg := imageRevisionFixture(cloud)
			a.secondPage, a.files = a.files, []attachment{}
			cfg.Settings["sync_attachments"] = true
			cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
			require.NoError(t, err)
			require.Equal(t, 2, a.attachmentCalls, "one paginated listing for both features")
			a.secondPage[0].Version.Number++
			h := &captureHandler{}
			_, err = c.FetchStream(context.Background(), cfg, cur, h)
			require.NoError(t, err)
			require.Len(t, h.items, 1)
			require.Equal(t, 4, a.attachmentCalls)
		})
	}
}

func TestImageAttachmentRefreshIsOptIn(t *testing.T) {
	a, c, cfg := imageRevisionFixture(false)
	cfg.Settings = nil
	cur, err := c.FetchStream(context.Background(), cfg, nil, &captureHandler{})
	require.NoError(t, err)
	a.files[0].Version.Number++
	h := &captureHandler{}
	_, err = c.FetchStream(context.Background(), cfg, cur, h)
	require.NoError(t, err)
	require.Empty(t, h.items)
	require.Zero(t, a.attachmentCalls)
}

func TestPageImageVersion(t *testing.T) {
	a := revisionAttachment("a", "chart.png", 1)
	b := revisionAttachment("b", "photo.jpg", 2)
	v, known := pageImageVersion([]attachment{a, b})
	require.True(t, known)
	reordered, _ := pageImageVersion([]attachment{b, a})
	require.Equal(t, v, reordered)
	pdf := attachment{ID: "pdf", Title: "manual.pdf", MediaType: "application/pdf"}
	withPDF, _ := pageImageVersion([]attachment{a, b, pdf})
	require.Equal(t, v, withPDF, "non-image attachments do not refresh the body")
	renamed := a
	renamed.Title = "renamed.png"
	renamedVersion, _ := pageImageVersion([]attachment{renamed, b})
	require.NotEqual(t, v, renamedVersion)
	a.Version.Number = 0
	_, known = pageImageVersion([]attachment{a})
	require.False(t, known)
	a.Version.When = "2026-11-28T00:00:00Z"
	_, known = pageImageVersion([]attachment{a})
	require.True(t, known)
	a.MediaType, a.Metadata.MediaType = "", "image/png"
	_, known = pageImageVersion([]attachment{a})
	require.True(t, known)
	empty, known := pageImageVersion(nil)
	require.Empty(t, empty)
	require.True(t, known)
}

func TestImageAttachmentFullSyncResumesFromAcknowledgedPage(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprint(cloud), func(t *testing.T) {
			a, c, cfg := imageRevisionFixture(cloud)
			a.pages = append(a.pages, streamPage{id: "p2", title: "Second guide", version: 1})
			ctx := context.Background()
			old, err := c.FetchStream(ctx, cfg, nil, &captureHandler{})
			require.NoError(t, err)
			a.files[0].Version.Number++
			partial := &captureHandler{succeedFirst: 1}
			_, err = c.FetchFullStream(ctx, cfg, old, partial)
			require.ErrorContains(t, err, "ingest failed")
			require.Len(t, partial.checkpoints, 1)
			checkpoint := partial.checkpoints[0]
			require.True(t, decodeCursor(checkpoint).FullSync)
			resumed := &captureHandler{}
			_, err = c.FetchFullStream(ctx, cfg, checkpoint, resumed)
			require.NoError(t, err)
			require.Len(t, resumed.items, 1)
			require.Equal(t, "p2", resumed.items[0].ExternalID)
		})
	}
}

func TestUnknownImageRevisionNeverAcknowledgesUnchanged(t *testing.T) {
	a, c, cfg := imageRevisionFixture(false)
	a.files[0].Version.Number = 0
	ctx := context.Background()
	cur, err := c.FetchStream(ctx, cfg, nil, &captureHandler{})
	require.NoError(t, err)
	next := &captureHandler{}
	_, err = c.FetchStream(ctx, cfg, cur, next)
	require.NoError(t, err)
	require.Len(t, next.items, 1)
}
