package pluginsdk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

type feedConnector struct{ items int }

func (f *feedConnector) Validate(_ context.Context, _ *Call, cfg ConnectorConfig) error {
	if cfg.Settings["url"] == nil {
		return pluginapi.InvalidConfig("feed URL is required", map[string]string{"settings.url": "required"})
	}
	return nil
}

func (f *feedConnector) ListResources(
	_ context.Context,
	_ *Call,
	cfg ConnectorConfig,
	parentID string,
) ([]pluginapi.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	return []pluginapi.Resource{{ExternalID: fmt.Sprint(cfg.Settings["url"]), Name: "Feed"}}, nil
}

func (f *feedConnector) Fetch(
	_ context.Context, _ *Call, _ ConnectorConfig, in pluginapi.FetchInput, s *Stream,
) (*pluginapi.Cursor, error) {
	start := 0
	if in.Cursor != nil {
		start = int(in.Cursor.State["next"].(float64))
	}
	for i := start; i < f.items; i++ {
		item := pluginapi.FetchedItem{ExternalID: fmt.Sprint(i), Title: fmt.Sprint("item ", i), Content: []byte("body")}
		if err := s.Item(item); err != nil {
			return nil, err
		}
		if err := s.Checkpoint(pluginapi.Cursor{State: map[string]any{"next": i + 1}}); err != nil {
			return nil, err
		}
		if i == 2 && in.Mode == "fail-midway" {
			return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "upstream went away")
		}
	}
	return &pluginapi.Cursor{State: map[string]any{"next": f.items}}, nil
}

func testPlugin(t *testing.T) (*Plugin, *client.Client) {
	t.Helper()
	p := New(Info{ID: "acme.feed", Version: "1.0.0"})
	p.Connector("feed", &feedConnector{items: 5})
	p.WebSearch(
		"echo",
		WebSearchFunc(func(ctx context.Context, call *Call, in pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
			switch in.Query {
			case "panic":
				panic("boom")
			case "deadline":
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{
				Title: in.Query,
				URL: fmt.Sprint("https://example.com/?tenant=", call.TenantID,
					"&key=", call.Config.Instance["api_key"]),
			}}}, nil
		}),
	)
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return p, client.New(srv.URL, nil, nil)
}

func TestUnaryCallsAndErrors(t *testing.T) {
	ctx := context.Background()
	_, c := testPlugin(t)

	m, err := c.Manifest(ctx)
	if err != nil || m.ID != "acme.feed" || m.Contributes["connectors"][0] != "feed" ||
		m.APIVersion != pluginapi.APIVersion {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if err := c.Health(ctx); err != nil {
		t.Fatal(err)
	}

	env := pluginapi.Envelope{
		Context: pluginapi.Context{TenantID: 7},
		Config:  pluginapi.Config{Instance: map[string]any{"api_key": "k1"}},
	}
	var out pluginapi.SearchOutput
	if err := c.Call(ctx, pluginapi.SearchPath("echo"), env, pluginapi.SearchInput{Query: "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].URL != "https://example.com/?tenant=7&key=k1" {
		t.Fatalf("search = %+v", out)
	}

	err = c.Call(ctx, pluginapi.ConnectorValidatePath("feed"), pluginapi.Envelope{}, nil, nil)
	if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeInvalidConfig ||
		e.Details.Fields["settings.url"] != "required" {
		t.Fatalf("validate = %v", err)
	}
	err = c.Call(ctx, pluginapi.SearchPath("nope"), env, pluginapi.SearchInput{}, nil)
	if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeNotFound {
		t.Fatalf("unknown provider = %v", err)
	}
	err = c.Call(ctx, pluginapi.SearchPath("echo"), env, pluginapi.SearchInput{Query: "panic"}, nil)
	if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeInternal ||
		!strings.Contains(e.Message, "boom") {
		t.Fatalf("panic = %v", err)
	}
	past := time.Now().Add(-time.Second)
	env.Context.Deadline = &past
	err = c.Call(ctx, pluginapi.SearchPath("echo"), env, pluginapi.SearchInput{Query: "deadline"}, nil)
	if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeUnavailable {
		t.Fatalf("the envelope deadline must reach the handler's context, got %v", err)
	}
}

func TestStreamingFetch(t *testing.T) {
	ctx := context.Background()
	_, c := testPlugin(t)
	env := pluginapi.Envelope{
		Config: pluginapi.Config{Instance: map[string]any{"settings": map[string]any{"url": "u"}}},
	}

	var items []string
	var last *pluginapi.Cursor
	end, err := c.Stream(ctx, pluginapi.ConnectorFetchPath("feed"), env,
		pluginapi.FetchInput{Mode: pluginapi.FetchFull, Cursor: &pluginapi.Cursor{State: map[string]any{"next": 3}}},
		func(ev pluginapi.Event) error {
			switch ev.Type {
			case pluginapi.EventItem:
				var it pluginapi.FetchedItem
				if err := remarshal(ev.Data, &it); err != nil {
					return err
				}
				items = append(items, it.ExternalID+":"+string(it.Content))
			case pluginapi.EventCheckpoint:
				last = &pluginapi.Cursor{}
				return remarshal(ev.Data, last)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(items, ",") != "3:body,4:body" || last.State["next"].(float64) != 5 ||
		!strings.Contains(string(end), `"next":5`) {
		t.Fatalf("items=%v last=%+v end=%s", items, last, end)
	}

	_, err = c.Stream(ctx, pluginapi.ConnectorFetchPath("feed"), env, pluginapi.FetchInput{Mode: "fail-midway"},
		func(pluginapi.Event) error { return nil })
	if e, ok := pluginapi.AsError(err); !ok || e.Code != pluginapi.CodeUnavailable || !e.Retryable {
		t.Fatalf("mid-stream failure = %v", err)
	}

	stop := errors.New("ingest failed")
	_, err = c.Stream(ctx, pluginapi.ConnectorFetchPath("feed"), env, pluginapi.FetchInput{},
		func(pluginapi.Event) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("a handler error must abort the stream, got %v", err)
	}
}

func TestAuthModes(t *testing.T) {
	ctx := context.Background()
	p, _ := testPlugin(t)

	bearer := httptest.NewServer(bearerAuth("tok")(p.Handler()))
	defer bearer.Close()
	if err := client.New(bearer.URL, nil, client.Bearer("wrong")).Health(ctx); !isCode(
		err,
		pluginapi.CodeUnauthorized,
	) {
		t.Fatalf("wrong token = %v", err)
	}
	if err := client.New(bearer.URL, nil, client.Bearer("tok")).Health(ctx); err != nil {
		t.Fatal(err)
	}

	signed := httptest.NewServer(signatureAuth([]byte("s3cret"))(p.Handler()))
	defer signed.Close()
	if err := client.New(signed.URL, nil, client.Signed("other")).Health(ctx); !isCode(
		err,
		pluginapi.CodeUnauthorized,
	) {
		t.Fatalf("wrong secret = %v", err)
	}
	var out pluginapi.SearchOutput
	err := client.New(signed.URL, nil, client.Signed("s3cret")).
		Call(ctx, pluginapi.SearchPath("echo"), pluginapi.Envelope{}, pluginapi.SearchInput{Query: "q"}, &out)
	if err != nil || len(out.Results) != 1 {
		t.Fatalf("signed call = %+v, %v", out, err)
	}
}

func isCode(err error, code pluginapi.ErrorCode) bool {
	e, ok := pluginapi.AsError(err)
	return ok && e.Code == code
}

// ServeContext in host mode listens on the socket from the environment,
// prints the handshake and requires the token.
func TestServeHostHandshake(t *testing.T) {
	dir, err := os.MkdirTemp("", "wkp")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "p.sock")
	t.Setenv(pluginapi.EnvSocket, sock)
	t.Setenv(pluginapi.EnvToken, "tok")

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	p, _ := testPlugin(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.ServeContext(ctx) }()

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdout
	hs, ok, err := pluginapi.ParseHandshake(line)
	if !ok || err != nil || hs.Network != "unix" || hs.Address != sock {
		t.Fatalf("handshake %q = %+v %v %v", line, hs, ok, err)
	}
	c := client.ForHandshake(hs, client.Bearer("tok"))
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestUIRequests(t *testing.T) {
	ctx := context.Background()
	p := New(Info{ID: "acme.ui", Version: "1.0.0"})
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	c := client.New(srv.URL, nil, nil)
	req := pluginapi.UIRequest{Mount: "pages/links", Method: "GET", Path: "/links", Role: "admin"}
	err := c.Call(ctx, pluginapi.UIRequestPath, pluginapi.Envelope{}, req, nil)
	if !isCode(err, pluginapi.CodeNotFound) {
		t.Fatalf("no handler = %v", err)
	}

	p.UI(func(_ context.Context, call *Call, in pluginapi.UIRequest) (*pluginapi.UIResponse, error) {
		if in.Path == "/missing" {
			return &pluginapi.UIResponse{Status: 404}, nil
		}
		return UIJSON(0, map[string]any{"mount": in.Mount, "path": in.Path, "role": in.Role, "tenant": call.TenantID})
	})
	if m := p.Manifest(); len(m.Contributes["ui"]) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	var out pluginapi.UIResponse
	env := pluginapi.Envelope{Context: pluginapi.Context{TenantID: 3}}
	if err := c.Call(ctx, pluginapi.UIRequestPath, env, req, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != 200 || string(out.Body) != `{"mount":"pages/links","path":"/links","role":"admin","tenant":3}` {
		t.Fatalf("out = %d %s", out.Status, out.Body)
	}
	req.Path = "/missing"
	if err := c.Call(ctx, pluginapi.UIRequestPath, env, req, &out); err != nil || out.Status != 404 {
		t.Fatalf("status = %d, %v", out.Status, err)
	}
}
