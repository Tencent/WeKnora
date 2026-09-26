package activate

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const clausesManifest = `schemaVersion: 1
id: acme.legal
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Legal }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/legal }
contributes:
  chunkers:
    - id: clauses
      name: { en-US: By clause }
`

type enabledFor map[uint64]bool

func (e enabledFor) PluginEnabled(_ context.Context, tenantID uint64, _ string) (bool, error) {
	return e[tenantID], nil
}

func TestPluginChunker(t *testing.T) {
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.legal", Version: "1.0.0"})
	plugin.Chunker("clauses", pluginsdk.ChunkerFunc(
		func(_ context.Context, _ *pluginsdk.Call, in pluginapi.ChunkInput) ([]pluginapi.ChunkSpan, error) {
			// One chunk per "Clause", in runes.
			var spans []pluginapi.ChunkSpan
			runes := []rune(in.Text)
			start := 0
			for i := 1; i <= len(runes); i++ {
				if i == len(runes) || strings.HasPrefix(string(runes[i:]), "Clause") {
					spans = append(spans, pluginapi.ChunkSpan{Start: start, End: i, ContextHeader: "Contract"})
					start = i
				}
			}
			return spans, nil
		}))
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()

	p, err := pkg.Open(plugintest.Zip(t, map[string]string{"plugin.yaml": clausesManifest, "bin/legal": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(p.Manifest); err != nil {
		t.Fatal(err)
	}
	split := PluginChunker(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}), reg, enabledFor{7: true})

	text := "Clause 1: 条款一。" + strings.Repeat("x", 300) + "Clause 2: 条款二。" + strings.Repeat("y", 300)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	spans, err := split(ctx, "acme.legal/clauses", text, chunker.SplitterConfig{ChunkSize: 512})
	if err != nil || len(spans) != 2 || spans[1].ContextHeader != "Contract" {
		t.Fatalf("spans = %+v, %v", spans, err)
	}
	if got := string([]rune(text)[spans[1].Start:spans[1].End]); !strings.HasPrefix(got, "Clause 2: 条款二") {
		t.Fatalf("second chunk = %q", got)
	}

	off := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(8))
	if _, err := split(off, "acme.legal/clauses", text, chunker.SplitterConfig{}); err == nil {
		t.Fatal("a workspace with the plugin off used its chunker")
	}
	if _, err := split(ctx, "acme.legal/missing", text, chunker.SplitterConfig{}); err == nil {
		t.Fatal("an unknown chunker answered")
	}
}
