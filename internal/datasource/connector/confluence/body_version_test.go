package confluence

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestSyncAcknowledgesFetchedBodyVersion(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	for _, cloud := range []bool{false, true} {
		for _, full := range []bool{false, true} {
			t.Run(fmt.Sprintf("cloud=%t/full=%t", cloud, full), func(t *testing.T) {
				fixture := &streamAPI{
					cloud:    cloud,
					pages:    []streamPage{{id: "p1", title: "Edited page", version: 1}},
					bodyHTML: map[string]string{"p1": "<p>Current version two body</p>"},
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					// The author publishes v2 after enumeration, before the rendered-body read.
					if req.URL.Query().Get("expand") == "body.view,version,space" ||
						req.URL.Query().Get("body-format") == "view" {
						fixture.pages[0].version = 2
					}
					response, err := fixture.response(req)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					defer func() { _ = response.Body.Close() }()
					for k, vals := range response.Header {
						for _, value := range vals {
							w.Header().Add(k, value)
						}
					}
					w.WriteHeader(response.StatusCode)
					_, _ = io.Copy(w, response.Body)
				}))
				defer server.Close()
				cfg := streamConfig()
				if cloud {
					cfg = cloudStreamConfig()
				}
				cfg.Credentials["base_url"] = server.URL + "/wiki"
				connector := NewConnector()
				first := &captureHandler{}
				var next *types.SyncCursor
				var err error
				if full {
					next, err = connector.FetchFullStream(context.Background(), cfg, nil, first)
				} else {
					next, err = connector.FetchStream(context.Background(), cfg, nil, first)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(first.items) != 1 || string(first.items[0].Content) != "Current version two body" {
					t.Fatalf("first sync did not ingest v2: %#v", first.items)
				}
				if len(first.checkpoints) != 1 {
					t.Fatalf("checkpoints=%d", len(first.checkpoints))
				}
				if got := decodeCursor(first.checkpoints[0]).SpacePages["1"]["p1"]; got != "v:2" {
					t.Errorf("acknowledged %q although accepted body is v2", got)
				}
				if full {
					resumed := &captureHandler{}
					_, resumeErr := connector.FetchFullStream(context.Background(), cfg, first.checkpoints[0], resumed)
					if resumeErr != nil {
						t.Fatal(resumeErr)
					}
					if len(resumed.items) != 0 {
						t.Errorf("full-sync retry repeated an acknowledged body: %d emits", len(resumed.items))
					}
				}
				second := &captureHandler{}
				if _, err := connector.FetchStream(context.Background(), cfg, next, second); err != nil {
					t.Fatal(err)
				}
				if len(second.items) != 0 || fixture.bodyCalls != 1 {
					t.Fatalf("unchanged v2 was downloaded/re-ingested again: body GETs=%d extra emits=%d",
						fixture.bodyCalls, len(second.items))
				}
			})
		}
	}
}
