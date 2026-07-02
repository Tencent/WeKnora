package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeAPISvc is a test double for Service that delegates each call to a
// caller-supplied do function, giving full control over per-call responses.
type fakeAPISvc struct {
	do func(method, path string, body any) (*http.Response, error)
}

func (f *fakeAPISvc) Raw(_ context.Context, method, path string, body any) (*http.Response, error) {
	return f.do(method, path, body)
}

// newTestClient stands up an httptest server with the supplied handler and
// returns an *sdk.Client targeting it plus a teardown closure. The real SDK is
// used so we exercise the same Raw() code path as production (header
// injection, JSON marshalling, etc.).
func newTestClient(t *testing.T, h http.HandlerFunc) (*sdk.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	return sdk.NewClient(srv.URL), srv.Close
}

func TestAPI_GetSuccess(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/foo" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	})
	defer stop()

	if err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, "GET", "/api/v1/foo", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"hello":"world"`) {
		t.Errorf("expected raw JSON body in stdout, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline appended, got %q", got)
	}
}

func TestAPI_GetSuccess_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-123")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"value":42}`))
	})
	defer stop()

	if err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, cli, "GET", "/api/v1/foo", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	// The server response is passed through directly under envelope.data — no
	// {status, headers, body} wrapper — so consumers project at the server's
	// own depth (e.g. .data.value here).
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope JSON: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Errorf("envelope ok: want true, got false")
	}
	if v, ok := env.Data["value"]; !ok || v.(float64) != 42 {
		t.Errorf("data.value: want 42 directly under .data (no .body nesting), got %v", env.Data)
	}
}

// TestAPI_InlineDataBody verifies -d/--data sends an inline JSON body and
// auto-promotes the method to POST.
func TestAPI_InlineDataBody(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	var seenBody []byte
	var seenMethod string
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"new"}`))
	})
	defer stop()

	opts := &Options{Data: `{"name":"foo"}`}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, resolveMethod(opts), "/api/v1/things", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if seenMethod != http.MethodPost {
		t.Errorf("inline -d should auto-promote to POST, got %s", seenMethod)
	}
	if string(seenBody) != `{"name":"foo"}` {
		t.Errorf("server received body %q, want the inline --data JSON", seenBody)
	}
}

// TestAPI_InlineDataMalformed_RejectedAsInput pins that a malformed -d body is
// rejected as input.invalid_argument before any request.
func TestAPI_InlineDataMalformed_RejectedAsInput(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	called := false
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{}`))
	})
	defer stop()

	opts := &Options{Data: `{bad`}
	err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, cli, "POST", "/api/v1/things", false)
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("want input.invalid_argument for malformed -d, got %v", err)
	}
	if called {
		t.Error("server must not be called with a malformed inline body")
	}
}

func TestAPI_PostWithStdinInput(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	var seenBody []byte
	var seenMethod, seenPath string
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"new"}`))
	})
	defer stop()

	opts := &Options{Input: "-", StdinReader: strings.NewReader(`{"name":"foo"}`)}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, "POST", "/api/v1/things", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if seenMethod != http.MethodPost || seenPath != "/api/v1/things" {
		t.Errorf("server saw %s %s, want POST /api/v1/things", seenMethod, seenPath)
	}
	if string(seenBody) != `{"name":"foo"}` {
		t.Errorf("server received body %q, want %q", seenBody, `{"name":"foo"}`)
	}
}

// TestAPI_MalformedInputJSON_RejectedAsInput pins that a non-JSON --input body
// is rejected with input.invalid_argument (exit 5) at the boundary, not the
// confusing network.error (exit 7, "retryable") the SDK's marshal step
// produced. The server must never be called.
func TestAPI_MalformedInputJSON_RejectedAsInput(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	called := false
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{}`))
	})
	defer stop()

	opts := &Options{Input: "-", StdinReader: strings.NewReader(`{bad json`)}
	err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, cli, "POST", "/api/v1/things", false)
	if err == nil {
		t.Fatal("expected an error for malformed --input JSON")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("want input.invalid_argument, got %v", err)
	}
	if called {
		t.Error("server must not be called with a malformed body")
	}
}

// TestAPI_InputFile verifies --input <file> reads the request body from disk.
func TestAPI_InputFile(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	tmp := filepath.Join(t.TempDir(), "body.json")
	payload := `{"k":"from-file"}`
	if err := os.WriteFile(tmp, []byte(payload), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	var seenBody []byte
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	})
	defer stop()

	opts := &Options{Input: tmp}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, "POST", "/api/v1/x", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if string(seenBody) != payload {
		t.Errorf("body from --input: got %q, want %q", seenBody, payload)
	}
}

// TestAPI_InputDash_Stdin verifies the "--input -" form: the payload comes
// from opts.StdinReader (production-default iostreams.IO.In).
func TestAPI_InputDash_Stdin(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	var seenBody []byte
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	})
	defer stop()

	payload := `{"k":"from-stdin"}`
	opts := &Options{Input: "-", StdinReader: strings.NewReader(payload)}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, "POST", "/api/v1/x", false); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if string(seenBody) != payload {
		t.Errorf("body from --input -: got %q, want %q", seenBody, payload)
	}
}

func TestAPI_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing"}`))
	})
	defer stop()

	err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, "GET", "/api/v1/missing", false)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}

func TestAPI_AcceptsArbitraryMethod(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	var seenMethod string
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		w.WriteHeader(http.StatusOK)
	})
	defer stop()
	for _, m := range []string{"OPTIONS", "PATCH", "TRACE", "CUSTOM"} {
		t.Run(m, func(t *testing.T) {
			seenMethod = ""
			err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, cli, m, "/api/v1/things", false)
			if err != nil {
				t.Fatalf("expected method %q to be accepted, got %v", m, err)
			}
			if seenMethod != m {
				t.Errorf("server saw method %q, want %q", seenMethod, m)
			}
		})
	}
}

func TestAPI_EmptyMethodRejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, nil, "", "/api/v1/things", false)
	if err == nil {
		t.Fatal("expected error for empty method")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("expected FlagError, got %T %v", err, err)
	}
}

func TestAPI_PathWithoutSlash(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, nil, "GET", "api/v1/things", false)
	if err == nil {
		t.Fatal("expected error for missing leading slash")
	}
	var ce *cmdutil.Error
	if !asTypedError(err, &ce) || ce.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

// withRootHarness wraps `weknora api ...` under a synthetic root cmd that
// registers the global persistent flags (mirrors addGlobalFlags in
// cmd/root.go). Required because api's NewCmd doesn't register --yes /
// --format / --jq itself — it inherits them from root in production.
func withRootHarness(api *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	root.AddCommand(api)
	root.SetArgs(append([]string{"api"}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

// TestAPI_DELETE_RequiresConfirmation pins the exit-10 protocol on the
// escape-hatch DELETE path: agent invokes `weknora api DELETE /...` without
// -y/--yes, must get input.confirmation_required + exit 10. Confirmation is
// enforced in NewCmd.RunE (not runAPI), so the test drives the cobra cmd.
func TestAPI_DELETE_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t) // non-TTY
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return nil, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarness(NewCmd(f), "/api/v1/knowledge-bases/kb_xxx", "-X", "DELETE")
	err := root.Execute()
	if err == nil {
		t.Fatal("expected confirmation_required error for DELETE without -y")
	}
	var ce *cmdutil.Error
	if !asTypedError(err, &ce) || ce.Code != cmdutil.CodeInputConfirmationRequired {
		t.Errorf("want input.confirmation_required, got %v", err)
	}
	if got := cmdutil.ExitCode(err); got != 10 {
		t.Errorf("exit code = %d, want 10", got)
	}
}

// TestAPI_DELETE_WithYes_Proceeds: -y/--yes opt-in skips confirmation and
// dispatches to the SDK. Server returns 200 to verify the happy-path lands
// on the response body emit.
func TestAPI_DELETE_WithYes_Proceeds(t *testing.T) {
	iostreams.SetForTest(t)
	called := false
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer stop()
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return cli, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarness(NewCmd(f), "/api/v1/knowledge-bases/kb_xxx", "-X", "DELETE", "-y")
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("DELETE handler not called - confirmation may have blocked")
	}
}

// asTypedError is a tiny wrapper around errors.As that keeps the call sites
// concise. Returns true on success, populating dst.
func asTypedError(err error, dst **cmdutil.Error) bool {
	for e := err; e != nil; {
		if t, ok := e.(*cmdutil.Error); ok {
			*dst = t
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

func TestAPI_PaginateMergesPages(t *testing.T) {
	pages := [][]byte{
		[]byte(`{"success":true,"data":[{"id":"1"},{"id":"2"}],"total":5,"page":1,"page_size":2}`),
		[]byte(`{"success":true,"data":[{"id":"3"},{"id":"4"}],"total":5,"page":2,"page_size":2}`),
		[]byte(`{"success":true,"data":[{"id":"5"}],"total":5,"page":3,"page_size":2}`),
	}
	idx := 0
	svc := &fakeAPISvc{do: func(method, path string, _ any) (*http.Response, error) {
		if idx >= len(pages) {
			return nil, fmt.Errorf("too many calls; idx=%d", idx)
		}
		body := pages[idx]
		idx++
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}}

	out, _ := iostreams.SetForTest(t)

	opts := &Options{}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "GET", "/api/v1/knowledge-base?page=1&page_size=2", true); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Data  []map[string]string `json:"data"`
			Total int                 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	got := env.Data
	if len(got.Data) != 5 || got.Total != 5 {
		t.Errorf("got %d records (total %d), want 5/5", len(got.Data), got.Total)
	}
	if idx != 3 {
		t.Errorf("called %d times, want 3", idx)
	}
}

func TestAPI_PaginateIgnoredForPOST(t *testing.T) {
	// --paginate should be a no-op for non-GET methods (no pagination
	// semantic for POST/PUT/DELETE). Single call expected.
	called := 0
	svc := &fakeAPISvc{do: func(method, path string, _ any) (*http.Response, error) {
		called++
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"success":true,"data":[],"total":5,"page":1,"page_size":2}`))),
			Header:     make(http.Header),
		}, nil
	}}

	_, _ = iostreams.SetForTest(t)

	opts := &Options{Input: "-", StdinReader: strings.NewReader(`{"name":"foo"}`)}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "POST", "/api/v1/knowledge-base", true); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if called != 1 {
		t.Errorf("called %d times, want 1 (POST should not paginate)", called)
	}
}

func TestAPI_PaginateNoMetadataPassesThrough(t *testing.T) {
	// If response doesn't look paginated (no total/page/page_size), --paginate
	// should fall back to single-call envelope behavior (same shape as api without --paginate).
	called := 0
	svc := &fakeAPISvc{do: func(method, path string, _ any) (*http.Response, error) {
		called++
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"hello":"world"}`))),
			Header:     hdr,
		}, nil
	}}

	out, _ := iostreams.SetForTest(t)

	opts := &Options{}
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "GET", "/api/v1/whoami", true); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if called != 1 {
		t.Errorf("called %d times, want 1 (non-paginated response)", called)
	}
	// The non-paginated fallback must emit the SAME flat shape as a single
	// call: the server response directly under .data (no {status,headers,body}
	// wrapper), so --paginate and single-call project identically.
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("non-paginated fallback must emit envelope JSON, unmarshal failed: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Errorf("envelope ok: want true, got false")
	}
	if v, ok := env.Data["hello"]; !ok || v.(string) != "world" {
		t.Errorf("fallback must pass the body through flat under .data (.data.hello), got %v", env.Data)
	}
}

// TestAPI_PaginateServerCapsPageSize covers the case where the user
// requests --page_size=50 but the server caps page_size at a smaller
// value (e.g. 2). Termination must count actually-collected records
// (len(allData)) not requested-page-count (page*pageSize) — otherwise
// we'd break early and silently truncate results.
func TestAPI_PaginateServerCapsPageSize(t *testing.T) {
	// User asks page_size=10; server only ever returns 2 per page (cap).
	// Total = 5 records; should make 3 calls (2+2+1) and return all 5.
	pages := [][]byte{
		[]byte(`{"success":true,"data":[{"id":"1"},{"id":"2"}],"total":5,"page":1,"page_size":2}`),
		[]byte(`{"success":true,"data":[{"id":"3"},{"id":"4"}],"total":5,"page":2,"page_size":2}`),
		[]byte(`{"success":true,"data":[{"id":"5"}],"total":5,"page":3,"page_size":2}`),
	}
	idx := 0
	svc := &fakeAPISvc{do: func(_, _ string, _ any) (*http.Response, error) {
		if idx >= len(pages) {
			return nil, fmt.Errorf("too many calls; idx=%d", idx)
		}
		body := pages[idx]
		idx++
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}}
	var stdout bytes.Buffer
	iostreams.IO.Out = &stdout
	defer func() { iostreams.IO.Out = os.Stdout }()

	opts := &Options{}
	// User requests page_size=10; server caps at 2 each response.
	if err := runAPI(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "GET", "/api/v1/items?page=1&page_size=10", true); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Data  []map[string]string `json:"data"`
			Total int                 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	got := env.Data
	if len(got.Data) != 5 {
		t.Errorf("got %d records, want 5 (server-capped page_size should not cause truncation)", len(got.Data))
	}
}

// TestAPI_PUT_RequiresConfirmation pins the exit-10 write gate on the
// escape-hatch PUT path: `weknora api -X PUT /...` mutates server state the
// same way a typed `kb update` does, so it must require -y (exit 10) rather
// than silently writing. Regression: only DELETE was gated, letting an agent
// bypass the write-confirmation protocol via raw PUT/PATCH.
func TestAPI_PUT_RequiresConfirmation(t *testing.T) {
	for _, method := range []string{"PUT", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			iostreams.SetForTest(t) // non-TTY
			f := &cmdutil.Factory{
				Client:   func() (*sdk.Client, error) { return nil, nil },
				Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
			}
			root := withRootHarness(NewCmd(f), "/api/v1/knowledge-bases/kb_xxx", "-X", method, "-F", "name=x")
			err := root.Execute()
			if err == nil {
				t.Fatalf("expected confirmation_required for %s without -y", method)
			}
			var ce *cmdutil.Error
			if !asTypedError(err, &ce) || ce.Code != cmdutil.CodeInputConfirmationRequired {
				t.Errorf("want input.confirmation_required, got %v", err)
			}
			if got := cmdutil.ExitCode(err); got != 10 {
				t.Errorf("exit code = %d, want 10", got)
			}
		})
	}
}

// TestAPI_POST_NotGated: POST is create-shaped and, like typed `kb create`,
// intentionally ungated — it must reach the SDK without a confirmation gate.
func TestAPI_POST_NotGated(t *testing.T) {
	iostreams.SetForTest(t)
	called := false
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer stop()
	f := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return cli, nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := withRootHarness(NewCmd(f), "/api/v1/knowledge-bases", "-X", "POST", "-F", "name=x")
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("POST handler not called - POST must not be gated")
	}
}
