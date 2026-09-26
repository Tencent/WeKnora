package activate

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const ocrManifest = `schemaVersion: 1
id: acme.ocr
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME OCR }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/ocr }
contributes:
  parsers:
    - id: ocr
      name: { en-US: ACME OCR, zh-CN: ACME 识别 }
      fileTypes: [tiff, pdf]
`

func TestPluginParser(t *testing.T) {
	ctx := context.Background()
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.ocr", Version: "1.0.0"})
	plugin.Parser("ocr", pluginsdk.ParserFunc(
		func(_ context.Context, _ *pluginsdk.Call, in pluginapi.ParseInput) (*pluginapi.ParseOutput, error) {
			switch string(in.Content) {
			case "busy":
				return nil, pluginapi.Errorf(pluginapi.CodeRateLimited, "slow down")
			case "broken":
				return nil, pluginapi.Errorf(pluginapi.CodeInvalidConfig, "not a scanned document")
			}
			return &pluginapi.ParseOutput{
				Markdown: "# " + in.FileName + "\n\n![scan](page-1.png)",
				Images: []pluginapi.ParsedImage{
					{OriginalRef: "page-1.png", MimeType: "image/png", Data: []byte{1, 2}},
				},
				Metadata: map[string]string{"pages": "1"},
			}, nil
		}))
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()

	p, err := pkg.Open(plugintest.Zip(t, map[string]string{"plugin.yaml": ocrManifest, "bin/ocr": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	a := NewParsers(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}))
	if err := a.Activate(ctx, &reconcile.Loaded{Manifest: p.Manifest, Package: p}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	defer func() { _ = a.Deactivate(ctx, "acme.ocr") }()

	var listed *types.ParserEngineInfo
	for _, e := range docparser.ListAllEngines(false, nil, nil) {
		if e.Name == "acme.ocr/ocr" {
			listed = &e
		}
	}
	if listed == nil || !listed.Available || listed.PluginID != "acme.ocr" ||
		listed.DisplayNames["zh-CN"] != "ACME 识别" ||
		!slices.Equal(listed.FileTypes, []string{"tiff", "pdf"}) {
		t.Fatalf("listed = %+v", listed)
	}
	if !slices.Contains(docparser.PluginFileTypes(ctx), "tiff") {
		t.Fatal("plugin file types must be importable")
	}
	// Once the switches are known, only workspaces with the plugin on
	// accept its file types.
	a.Bind(tenantSwitch{on: 7})
	on := context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	off := context.WithValue(ctx, types.TenantIDContextKey, uint64(8))
	if !slices.Contains(docparser.PluginFileTypes(on), "tiff") ||
		slices.Contains(docparser.PluginFileTypes(off), "tiff") {
		t.Fatal("a plugin's file types must be importable exactly where the plugin is on")
	}
	a.gate.Store(nil)

	reader, err := docparser.NewReader(ctx, "acme.ocr/ocr", "tiff", false, docparser.ReaderDeps{
		Overrides: map[string]string{"mineru_api_key": "must-not-leak"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := reader.Read(
		ctx,
		&types.ReadRequest{FileName: "scan.tiff", FileType: ".TIFF", FileContent: []byte("img")},
	)
	if err != nil || !strings.HasPrefix(res.MarkdownContent, "# scan.tiff") || res.Metadata["pages"] != "1" ||
		len(
			res.ImageRefs,
		) != 1 || res.ImageRefs[0].OriginalRef != "page-1.png" || res.ImageRefs[0].Filename != "page-1.png" {
		t.Fatalf("Read = %+v, %v", res, err)
	}

	if _, err := reader.Read(ctx, &types.ReadRequest{FileContent: []byte("busy")}); err == nil {
		t.Fatal("a retryable plugin error must come back as an error, so the task retries")
	}
	res, err = reader.Read(ctx, &types.ReadRequest{FileContent: []byte("broken")})
	if err != nil || res.Error != "not a scanned document" {
		t.Fatalf("a permanent plugin error must fail the document: %+v, %v", res, err)
	}

	if err := a.Deactivate(ctx, "acme.ocr"); err != nil {
		t.Fatal(err)
	}
	if _, err := docparser.NewReader(ctx, "acme.ocr/ocr", "tiff", false, docparser.ReaderDeps{}); err == nil ||
		!strings.Contains(err.Error(), "not installed or not running") {
		t.Fatalf("a removed plugin engine must not fall through to the docreader: %v", err)
	}
}

func TestPluginParserCannotShadowABuiltin(t *testing.T) {
	e := &pluginEngine{name: "simple"}
	if err := docparser.RegisterPluginEngine(e); err == nil {
		t.Fatal("a plugin engine must not take a builtin engine's name")
	}
}

// tenantSwitch has plugins on in one workspace only.
type tenantSwitch struct{ on uint64 }

func (s tenantSwitch) PluginEnabled(_ context.Context, tenantID uint64, _ string) (bool, error) {
	return tenantID == s.on, nil
}
