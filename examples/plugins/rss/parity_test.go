package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/datasource/connector/rss"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// feedSite serves two feeds and the article pages they link to. Changing an
// entry's text simulates a feed update between syncs.
type feedSite struct {
	*httptest.Server
	mu      sync.Mutex
	secondA string
}

func newFeedSite(t *testing.T) *feedSite {
	s := &feedSite{secondA: "Second post, first version."}
	mux := http.NewServeMux()
	mux.HandleFunc("/a.xml", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		second := s.secondA
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Feed A</title><link>%[1]s</link>
<description>First feed</description>
<item><title>Hello: world</title><link>%[1]s/articles/1</link><guid>a-1</guid>
<pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate><description>Teaser one</description></item>
<item><title>Second</title><guid>a-2</guid><pubDate>Tue, 03 Jan 2006 15:04:05 GMT</pubDate>
<description><![CDATA[<p>%[2]s</p>]]></description></item>
</channel></rss>`, "http://"+r.Host, second)
	})
	mux.HandleFunc("/b.atom", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><feed xmlns="http://www.w3.org/2005/Atom">
<title>Feed B</title><id>urn:b</id><updated>2006-01-04T00:00:00Z</updated>
<entry><title>Atom entry</title><id>urn:b:1</id><updated>2006-01-04T00:00:00Z</updated>
<author><name>Ada</name></author><content type="html">&lt;h2&gt;Atom&lt;/h2&gt;&lt;p&gt;body&lt;/p&gt;</content></entry>
</feed>`)
	})
	mux.HandleFunc("/articles/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><head><title>Article one</title></head><body><nav>menu</nav><article>
<h1>Hello world</h1><p>The full text of the first article is long enough for a readability extractor to keep it.
It carries several sentences, so the page is recognised as the main content and not as chrome.</p>
<p>A second paragraph makes the article clearly the densest block on the page.</p></article></body></html>`)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// syncBoth runs the builtin connector and the plugin over the same config.
type harness struct {
	t       *testing.T
	builtin *builtin.Connector
	plugin  *client.Client
	cfg     *types.DataSourceConfig
}

func newHarness(t *testing.T) (*harness, *feedSite) {
	t.Helper()
	site := newFeedSite(t)
	// The builtin connector fetches through the SSRF-safe client.
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.rss", Version: Version})
	p.Connector("rss", &connector{})
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return &harness{
		t: t, builtin: builtin.NewConnector(), plugin: client.New(srv.URL, nil, nil),
		cfg: &types.DataSourceConfig{
			Type:     types.ConnectorTypeRSS,
			Settings: map[string]any{"feed_urls": site.URL + "/a.xml\n" + site.URL + "/b.atom"},
		},
	}, site
}

func (h *harness) pluginFetch(mode string, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
	h.t.Helper()
	instance := map[string]any{"settings": h.cfg.Settings, "credentials": map[string]any{}}
	in := pluginapi.FetchInput{Mode: mode}
	if cursor != nil {
		in.Cursor = &pluginapi.Cursor{State: cursor.ConnectorCursor}
	}
	var items []types.FetchedItem
	end, err := h.plugin.Stream(context.Background(), pluginapi.ConnectorFetchPath("rss"),
		pluginapi.Envelope{Config: pluginapi.Config{Instance: instance}}, in, func(ev pluginapi.Event) error {
			if ev.Type != pluginapi.EventItem {
				return nil
			}
			var it pluginapi.FetchedItem
			if err := json.Unmarshal(ev.Data, &it); err != nil {
				return err
			}
			items = append(items, types.FetchedItem{
				ExternalID: it.ExternalID, Title: it.Title, Content: it.Content, ContentType: it.ContentType,
				FileName: it.FileName, URL: it.URL, UpdatedAt: *it.UpdatedAt, Metadata: it.Metadata,
				SourceResourceID: it.SourceResourceID,
			})
			return nil
		})
	if err != nil {
		h.t.Fatalf("plugin fetch: %v", err)
	}
	var c pluginapi.Cursor
	if err := json.Unmarshal(end, &c); err != nil {
		h.t.Fatal(err)
	}
	return items, &types.SyncCursor{ConnectorCursor: c.State}
}

// comparable renders items in a stable form; UpdatedAt of an undated item is
// "now" on both sides, so it is compared only when the feed dated the item.
func comparable(items []types.FetchedItem) []string {
	var out []string
	for _, it := range items {
		meta, _ := json.Marshal(it.Metadata)
		updated := it.UpdatedAt.UTC().Format(time.RFC3339)
		if time.Since(it.UpdatedAt) < time.Hour {
			updated = "now"
		}
		out = append(out, strings.Join([]string{
			it.ExternalID, it.Title, it.ContentType, it.FileName, it.URL, updated, it.SourceResourceID,
			string(meta), string(it.Content),
		}, " | "))
	}
	sort.Strings(out)
	return out
}

func sameItems(t *testing.T, label string, want, got []types.FetchedItem) {
	t.Helper()
	w, g := comparable(want), comparable(got)
	if strings.Join(w, "\n") != strings.Join(g, "\n") {
		t.Fatalf("%s differ\nbuiltin:\n%s\n\nplugin:\n%s", label, strings.Join(w, "\n"), strings.Join(g, "\n"))
	}
}

func TestPluginMatchesBuiltinConnector(t *testing.T) {
	ctx := context.Background()
	h, site := newHarness(t)

	want, err := h.builtin.FetchAll(ctx, h.cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, pluginCursor := h.pluginFetch(pluginapi.FetchFull, nil)
	if len(want) != 3 {
		t.Fatalf("builtin fetched %d items, want 3", len(want))
	}
	sameItems(t, "full sync", want, got)
	if !strings.Contains(
		string(got[0].Content)+string(got[1].Content)+string(got[2].Content),
		"full text of the first article",
	) {
		t.Fatal("the article page should replace the feed teaser")
	}

	// An incremental run with nothing changed emits nothing on either side.
	_, builtinCursor, err := h.builtin.FetchIncremental(ctx, h.cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _, err := h.builtin.FetchIncremental(ctx, h.cfg, builtinCursor)
	if err != nil || len(unchanged) != 0 {
		t.Fatalf("builtin incremental without changes = %d items, %v", len(unchanged), err)
	}
	if again, _ := h.pluginFetch(pluginapi.FetchIncremental, pluginCursor); len(again) != 0 {
		t.Fatalf("plugin incremental without changes = %d items", len(again))
	}

	// One entry changes: both emit exactly that entry.
	site.mu.Lock()
	site.secondA = "Second post, edited."
	site.mu.Unlock()
	changedWant, _, err := h.builtin.FetchIncremental(ctx, h.cfg, builtinCursor)
	if err != nil {
		t.Fatal(err)
	}
	changedGot, _ := h.pluginFetch(pluginapi.FetchIncremental, pluginCursor)
	if len(changedWant) != 1 {
		t.Fatalf("builtin incremental after an edit = %d items", len(changedWant))
	}
	sameItems(t, "incremental sync", changedWant, changedGot)
}

func TestPluginValidatesLikeTheBuiltin(t *testing.T) {
	h, site := newHarness(t)
	ctx := context.Background()
	call := func(settings map[string]any) error {
		return h.plugin.Call(ctx, pluginapi.ConnectorValidatePath("rss"), pluginapi.Envelope{Config: pluginapi.Config{
			Instance: map[string]any{"settings": settings, "credentials": map[string]any{}},
		}}, nil, nil)
	}
	if err := call(h.cfg.Settings); err != nil {
		t.Fatalf("valid feeds rejected: %v", err)
	}
	for name, settings := range map[string]map[string]any{
		"no feeds":     {},
		"missing feed": {"feed_urls": site.URL + "/nope.xml"},
	} {
		err := call(settings)
		e, ok := pluginapi.AsError(err)
		if !ok || e.Code != pluginapi.CodeInvalidConfig {
			t.Errorf("%s: want invalid_config, got %v", name, err)
		}
		cfg := &types.DataSourceConfig{Settings: settings}
		if h.builtin.Validate(ctx, cfg) == nil {
			t.Errorf("%s: the builtin accepts what the plugin rejects", name)
		}
	}
}

// The example must stay an external plugin: the SDK and ordinary libraries,
// nothing from WeKnora's internals.
func TestPluginImportsNoInternals(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.Contains(imp.Path.Value, "github.com/Tencent/WeKnora/internal") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}

func TestPluginPassesConformance(t *testing.T) {
	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.rss", Version: Version})
	p.Connector("rss", &connector{})
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	rep := conformance.Run(context.Background(), conformance.Target{
		Client: client.New(srv.URL, nil, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, bytes.NewReader(body))
			return http.DefaultClient.Do(req)
		},
	})
	for _, r := range rep.Results {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Detail)
		}
	}
}
