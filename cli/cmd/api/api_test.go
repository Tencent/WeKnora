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

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
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
