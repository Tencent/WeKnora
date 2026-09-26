package main

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const srt = "\ufeff1\r\n00:00:01,000 --> 00:00:03,500\r\nHello <i>world</i>.\r\n\r\n" +
	"2\r\n00:01:02,250 --> 00:01:04,000\r\nSecond line\r\ncontinues here.\r\n"

const vtt = `WEBVTT

NOTE a comment

01:02.500 --> 01:04.000
<v Ada>Plugins run out of process.

1:00:00.000 --> 1:00:02.000 align:start
Bye.
`

func TestParseSRTAndVTT(t *testing.T) {
	ctx := context.Background()
	out, err := parse(
		ctx,
		&pluginsdk.Call{},
		pluginapi.ParseInput{FileName: "talk.srt", FileType: "srt", Content: []byte(srt)},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "# talk\n\n**[00:01]** Hello world.\n\n**[01:02]** Second line continues here.\n"
	if out.Markdown != want || out.Metadata["cues"] != "2" {
		t.Fatalf("srt markdown = %q", out.Markdown)
	}
	out, err = parse(
		ctx,
		&pluginsdk.Call{},
		pluginapi.ParseInput{FileName: "t.vtt", FileType: "vtt", Title: "Talk", Content: []byte(vtt)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Markdown, "# Talk") ||
		!strings.Contains(out.Markdown, "**[01:02]** Plugins run out of process.") ||
		!strings.Contains(out.Markdown, "**[1:00:00]** Bye.") {
		t.Fatalf("vtt markdown = %q", out.Markdown)
	}
	for _, bad := range []string{"", "just some text\nwith no cues\n"} {
		_, err := parse(
			ctx,
			&pluginsdk.Call{},
			pluginapi.ParseInput{FileName: "x.srt", FileType: "srt", Content: []byte(bad)},
		)
		if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeInvalidConfig || e.Retryable {
			t.Errorf("%q: want a permanent invalid_config error, got %v", bad, err)
		}
	}
}

func TestParseCountsThroughTheHostAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.PluginKV{}); err != nil {
		t.Fatal(err)
	}
	iss := hostapi.NewIssuer([]byte("k"))
	r := gin.New()
	hostapi.NewHandler(iss, hostapi.NewKV(repository.NewPluginKVRepository(db))).Register(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	tok, _, _ := iss.Issue("weknora-examples.subtitles", Version, 3, []string{"kv"})
	call := &pluginsdk.Call{
		Context: pluginapi.Context{TenantID: 3, Host: &pluginapi.HostAccess{URL: srv.URL, Token: tok}},
	}
	in := pluginapi.ParseInput{FileName: "a.srt", FileType: "srt", Content: []byte(srt)}
	for range 2 {
		if _, err := parse(context.Background(), call, in); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if ok, err := call.Host().KVGet(context.Background(), "stats/parsed", &n); !ok || err != nil || n != 2 {
		t.Fatalf("parsed count = %d (%v, %v)", n, ok, err)
	}
}

func TestConformsAndStaysExternal(t *testing.T) {
	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.subtitles", Version: Version})
	p.Parser("subtitles", pluginsdk.ParserFunc(parse))
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	rep := conformance.Run(context.Background(), conformance.Target{
		Client: client.New(srv.URL, nil, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, bytes.NewReader(body))
			return http.DefaultClient.Do(req)
		},
	})
	for _, res := range rep.Results {
		if !res.Passed {
			t.Errorf("%s: %s", res.Name, res.Detail)
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		parsed, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.Contains(imp.Path.Value, "WeKnora/internal") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}
