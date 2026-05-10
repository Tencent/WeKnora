package api

import (
	"context"
	"encoding/json"
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

	if err := runAPI(context.Background(), &Options{}, cli, "GET", "/api/v1/foo"); err != nil {
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

	if err := runAPI(context.Background(), &Options{JSONOut: true}, cli, "GET", "/api/v1/foo"); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    map[string]any    `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Errorf("expected ok:true, got %s", out.String())
	}
	if env.Data.Status != 200 {
		t.Errorf("status: want 200, got %d", env.Data.Status)
	}
	if env.Data.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header missing: %v", env.Data.Headers)
	}
	if got, ok := env.Data.Body["value"]; !ok || got.(float64) != 42 {
		t.Errorf("body.value: want 42, got %v", env.Data.Body)
	}
}

func TestAPI_PostWithData(t *testing.T) {
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

	opts := &Options{Data: `{"name":"foo"}`}
	if err := runAPI(context.Background(), opts, cli, "POST", "/api/v1/things"); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if seenMethod != http.MethodPost || seenPath != "/api/v1/things" {
		t.Errorf("server saw %s %s, want POST /api/v1/things", seenMethod, seenPath)
	}
	// SDK marshals body via json.Marshal; json.RawMessage round-trips
	// verbatim so the bytes server-side equal the --data argument.
	if string(seenBody) != `{"name":"foo"}` {
		t.Errorf("server received body %q, want %q", seenBody, `{"name":"foo"}`)
	}
}

func TestAPI_DataFile(t *testing.T) {
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

	opts := &Options{DataFile: tmp}
	if err := runAPI(context.Background(), opts, cli, "POST", "/api/v1/x"); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if string(seenBody) != payload {
		t.Errorf("body from --data-file: got %q, want %q", seenBody, payload)
	}
}

func TestAPI_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing"}`))
	})
	defer stop()

	err := runAPI(context.Background(), &Options{}, cli, "GET", "/api/v1/missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("expected resource.not_found, got %v", err)
	}
}

func TestAPI_InvalidMethod(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	// No server needed: validation should fail before dispatch.
	err := runAPI(context.Background(), &Options{}, nil, "FOO", "/api/v1/things")
	if err == nil {
		t.Fatal("expected error for unsupported method")
	}
	var ce *cmdutil.Error
	if !asTypedError(err, &ce) || ce.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

func TestAPI_PathWithoutSlash(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runAPI(context.Background(), &Options{}, nil, "GET", "api/v1/things")
	if err == nil {
		t.Fatal("expected error for missing leading slash")
	}
	var ce *cmdutil.Error
	if !asTypedError(err, &ce) || ce.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

// withRootHarness wraps `weknora api ...` under a synthetic root cmd that
// registers the global `-y/--yes` persistent flag (mirrors addGlobalFlags in
// cmd/root.go). Required because api's NewCmd doesn't register --yes itself
// — it inherits from root in production.
func withRootHarness(api *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	root.PersistentFlags().BoolP("yes", "y", false, "")
	root.PersistentFlags().Bool("dry-run", false, "")
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
	root := withRootHarness(NewCmd(f), "DELETE", "/api/v1/knowledge-bases/kb_xxx")
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
	root := withRootHarness(NewCmd(f), "DELETE", "/api/v1/knowledge-bases/kb_xxx", "-y")
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("DELETE handler not called — confirmation may have blocked")
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
