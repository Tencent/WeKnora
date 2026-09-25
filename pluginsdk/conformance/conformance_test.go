package conformance_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

type strictConnector struct{}

func (strictConnector) Validate(_ context.Context, _ *pluginsdk.Call, cfg pluginsdk.ConnectorConfig) error {
	if cfg.Settings["url"] == nil {
		return pluginapi.InvalidConfig("url is required", map[string]string{"settings.url": "required"})
	}
	return nil
}

func (s strictConnector) ListResources(
	ctx context.Context,
	call *pluginsdk.Call,
	cfg pluginsdk.ConnectorConfig,
	_ string,
) ([]pluginapi.Resource, error) {
	return nil, s.Validate(ctx, call, cfg)
}

func (s strictConnector) Fetch(
	ctx context.Context,
	call *pluginsdk.Call,
	cfg pluginsdk.ConnectorConfig,
	_ pluginapi.FetchInput,
	_ *pluginsdk.Stream,
) (*pluginapi.Cursor, error) {
	return nil, s.Validate(ctx, call, cfg)
}

func target(t *testing.T, h http.Handler) conformance.Target {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return conformance.Target{
		Client: client.New(srv.URL, nil, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, bytes.NewReader(body))
			return http.DefaultClient.Do(req)
		},
	}
}

func TestSDKPluginConforms(t *testing.T) {
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.strict", Version: "1.0.0"})
	p.Connector("strict", strictConnector{})
	p.WebSearch(
		"none",
		pluginsdk.WebSearchFunc(
			func(context.Context, *pluginsdk.Call, pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
				return nil, pluginapi.InvalidConfig("api key required", map[string]string{"api_key": "required"})
			},
		),
	)
	rep := conformance.Run(context.Background(), target(t, p.Handler()))
	for _, r := range rep.Results {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Detail)
		}
	}
	if !rep.Passed() || rep.Plugin != "acme.strict@1.0.0" || len(rep.Results) < 10 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestBrokenPluginFails(t *testing.T) {
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.broken", Version: "1.0.0"})
	p.Connector("broken", strictConnector{})
	good := p.Handler()
	// A fetch that returns plain text and a list that crashes.
	broken := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/fetch"):
			_, _ = w.Write([]byte("oops"))
		case strings.HasSuffix(r.URL.Path, "/list-resources"):
			panic("nil map")
		default:
			good.ServeHTTP(w, r)
		}
	})
	rep := conformance.Run(context.Background(), target(t, broken))
	failed := map[string]bool{}
	for _, r := range rep.Results {
		if !r.Passed {
			failed[r.Name] = true
		}
	}
	if rep.Passed() || !failed["connectors/broken fetch stream terminates"] ||
		!failed["connectors/broken list-resources answers"] {
		t.Fatalf("failed checks = %v", failed)
	}
}
