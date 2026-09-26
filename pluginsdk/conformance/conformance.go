// Package conformance checks that a plugin speaks the extension protocol:
// it answers the discovery endpoints, rejects what it must reject, and
// answers every endpoint it claims with either a well-formed output or a
// well-formed protocol error. It does not judge the answers themselves; a
// plugin's own tests do that.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Result is one check.
type Result struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Report is every check against one plugin.
type Report struct {
	Plugin  string   `json:"plugin"`
	Results []Result `json:"results"`
}

// Passed reports whether every check passed.
func (r Report) Passed() bool {
	for _, res := range r.Results {
		if !res.Passed {
			return false
		}
	}
	return len(r.Results) > 0
}

// Target is the plugin under test.
type Target struct {
	// Client reaches the plugin with valid credentials.
	Client *client.Client
	// Unauthenticated reaches it without credentials; nil skips the check
	// (a plugin served without authentication in a test harness).
	Unauthenticated *client.Client
	// Raw posts arbitrary bytes, for malformed-request checks; nil skips.
	Raw func(ctx context.Context, path string, body []byte) (*http.Response, error)
	// Timeout bounds each call (default 30s).
	Timeout time.Duration
}

var semver = regexp.MustCompile(`^\d+\.\d+\.\d+([-+].*)?$`)

// Run checks a plugin.
func Run(ctx context.Context, t Target) Report {
	if t.Timeout <= 0 {
		t.Timeout = 30 * time.Second
	}
	var rep Report
	check := func(name string, fn func(ctx context.Context) error) {
		cctx, cancel := context.WithTimeout(ctx, t.Timeout)
		defer cancel()
		res := Result{Name: name, Passed: true}
		if err := fn(cctx); err != nil {
			res.Passed, res.Detail = false, err.Error()
		}
		rep.Results = append(rep.Results, res)
	}

	var m *pluginapi.Manifest
	check("manifest", func(ctx context.Context) error {
		var err error
		if m, err = t.Client.Manifest(ctx); err != nil {
			return err
		}
		rep.Plugin = m.ID + "@" + m.Version
		switch {
		case m.ID == "":
			return fmt.Errorf("manifest has no id")
		case !semver.MatchString(m.Version):
			return fmt.Errorf("version %q is not semantic", m.Version)
		case m.APIVersion != pluginapi.APIVersion:
			return fmt.Errorf("apiVersion %q, want %q", m.APIVersion, pluginapi.APIVersion)
		case len(m.Contributes) == 0:
			return fmt.Errorf("manifest lists no contributions")
		}
		return nil
	})
	check("health", func(ctx context.Context) error { return t.Client.Health(ctx) })
	if t.Unauthenticated != nil {
		check("rejects unauthenticated calls", func(ctx context.Context) error {
			return wantCode(t.Unauthenticated.Health(ctx), pluginapi.CodeUnauthorized)
		})
	}
	check("unknown endpoint answers not_found", func(ctx context.Context) error {
		return wantCode(
			t.Client.Call(ctx, "/v1/no-such-endpoint", pluginapi.Envelope{}, nil, nil),
			pluginapi.CodeNotFound,
		)
	})
	check("unknown contribution answers not_found", func(ctx context.Context) error {
		return wantCode(t.Client.Call(ctx, pluginapi.SearchPath("no-such-provider"), pluginapi.Envelope{},
			pluginapi.SearchInput{Query: "x"}, nil), pluginapi.CodeNotFound)
	})
	if t.Raw != nil {
		check("malformed envelope answers bad_request", func(ctx context.Context) error {
			resp, err := t.Raw(ctx, "/v1/config/validate", []byte("{not json"))
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			return wantErrorBody(resp, pluginapi.CodeBadRequest)
		})
	}
	check("config/validate answers", func(ctx context.Context) error {
		return protocolAnswer(t.Client.Call(ctx, "/v1/config/validate", envelope(), nil, nil))
	})
	if m == nil {
		return rep
	}
	for _, id := range m.Contributes["webSearch"] {
		check("webSearch/"+id+" search answers", func(ctx context.Context) error {
			var out pluginapi.SearchOutput
			err := t.Client.Call(
				ctx,
				pluginapi.SearchPath(id),
				envelope(),
				pluginapi.SearchInput{Query: "weknora", MaxResults: 3},
				&out,
			)
			if err == nil && out.Results == nil {
				return fmt.Errorf("output has no results array")
			}
			return protocolAnswer(err)
		})
	}
	for _, id := range m.Contributes["parsers"] {
		check("parsers/"+id+" parse answers", func(ctx context.Context) error {
			var out pluginapi.ParseOutput
			err := t.Client.Call(ctx, pluginapi.ParsePath(id), envelope(), pluginapi.ParseInput{
				FileName: "conformance.txt", FileType: "txt", Content: []byte("WeKnora conformance check\n"),
			}, &out)
			return protocolAnswer(err)
		})
	}
	for _, id := range m.Contributes["connectors"] {
		check("connectors/"+id+" validate answers", func(ctx context.Context) error {
			return protocolAnswer(t.Client.Call(ctx, pluginapi.ConnectorValidatePath(id), envelope(), nil, nil))
		})
		check("connectors/"+id+" list-resources answers", func(ctx context.Context) error {
			var out pluginapi.ListResourcesOutput
			err := t.Client.Call(
				ctx,
				pluginapi.ConnectorListResourcesPath(id),
				envelope(),
				pluginapi.ListResourcesInput{},
				&out,
			)
			if err == nil && out.Resources == nil {
				return fmt.Errorf("output has no resources array")
			}
			return protocolAnswer(err)
		})
		check("connectors/"+id+" resolve-ancestors answers", func(ctx context.Context) error {
			var out pluginapi.ResolveAncestorsOutput
			err := t.Client.Call(ctx, pluginapi.ConnectorResolveAncestorsPath(id), envelope(),
				pluginapi.ResolveAncestorsInput{ResourceIDs: []string{}}, &out)
			if err == nil && out.Ancestors == nil {
				return fmt.Errorf("output has no ancestors array")
			}
			return protocolAnswer(err)
		})
		check("connectors/"+id+" fetch stream terminates", func(ctx context.Context) error {
			_, err := t.Client.Stream(ctx, pluginapi.ConnectorFetchPath(id), envelope(),
				pluginapi.FetchInput{Mode: pluginapi.FetchFull}, func(ev pluginapi.Event) error {
					switch ev.Type {
					case pluginapi.EventItem, pluginapi.EventCheckpoint, pluginapi.EventProgress, pluginapi.EventLog:
						return nil
					}
					return fmt.Errorf("unknown event type %q", ev.Type)
				})
			return protocolAnswer(err)
		})
	}
	return rep
}

// envelope is a call with empty configuration: plugins must answer it with
// an output or a protocol error, never a crash or garbage.
func envelope() pluginapi.Envelope {
	return pluginapi.Envelope{Context: pluginapi.Context{TenantID: 1, RequestID: "conformance", Locale: "en-US"}}
}

// protocolAnswer accepts success or a protocol error the plugin chose, and
// fails transport failures and internal errors: a plugin must answer an
// empty configuration with invalid_config, not by crashing.
func protocolAnswer(err error) error {
	if err == nil {
		return nil
	}
	var te *client.TransportError
	if errors.As(err, &te) {
		return fmt.Errorf("no protocol answer: %v", te)
	}
	e, ok := pluginapi.AsError(err)
	if !ok {
		return err
	}
	if e.Code == pluginapi.CodeInternal {
		return fmt.Errorf("answered internal: %s", e.Message)
	}
	return nil
}

func wantCode(err error, code pluginapi.ErrorCode) error {
	e, ok := pluginapi.AsError(err)
	if !ok {
		return fmt.Errorf("want a %s protocol error, got %v", code, err)
	}
	if e.Code != code {
		return fmt.Errorf("want %s, got %s: %s", code, e.Code, e.Message)
	}
	return nil
}

func wantErrorBody(resp *http.Response, code pluginapi.ErrorCode) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body pluginapi.ErrorBody
	if err := json.Unmarshal(b, &body); err != nil {
		return fmt.Errorf("HTTP %d without a protocol error body: %s", resp.StatusCode, b)
	}
	if resp.StatusCode != code.HTTPStatus() || body.Error.Code != code {
		return fmt.Errorf(
			"want HTTP %d %s, got HTTP %d %s",
			code.HTTPStatus(),
			code,
			resp.StatusCode,
			body.Error.Code,
		)
	}
	return nil
}
