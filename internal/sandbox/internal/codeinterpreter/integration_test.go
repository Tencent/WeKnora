package codeinterpreter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunCode_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertJupyterRequest(t, r, "POST", "/execute")
		assertExecuteBody(t, r, "x = 1; x", "", "")
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, ndjson(
			`{"type": "result", "text": "1", "is_main_result": true}`,
		))
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "x = 1; x", nil)
	if err != nil {
		t.Fatalf("run code: %v", err)
	}
	if got := exec.Text(); got != "1" {
		t.Errorf("text: got %q, want %q", got, "1")
	}
	if len(exec.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(exec.Results))
	}
}

func TestRunCode_SendsContextID(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		body := readJSON(t, r)
		if body["context_id"] != "ctx-42" {
			t.Errorf("expected context_id=ctx-42, got %v", body["context_id"])
		}
		if n == 1 {
			_, _ = io.WriteString(w, `{"type": "result", "text": "", "is_main_result": true}`)
		} else {
			_, _ = io.WriteString(w, `{"type": "result", "text": "2", "is_main_result": true}`)
		}
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	ctx := context.Background()
	ctxObj := &Context{ID: "ctx-42", Language: "python"}

	if _, err := sbx.RunCode(ctx, "test_stateful = 1", &RunCodeOpts{Context: ctxObj}); err != nil {
		t.Fatal(err)
	}
	exec, err := sbx.RunCode(ctx, "test_stateful+=1; test_stateful", &RunCodeOpts{Context: ctxObj})
	if err != nil {
		t.Fatal(err)
	}
	if exec.Text() != "2" {
		t.Errorf("expected 2, got %q", exec.Text())
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 /execute calls, got %d", calls)
	}
}

func TestRunCode_Callbacks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ndjson(
			`{"type": "stdout", "text": "Hello from e2b\n", "timestamp": 1}`,
			`{"type": "stderr", "text": "This is an error message\n", "timestamp": 2}`,
			`{"type": "result", "text": "1", "is_main_result": true}`,
		))
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)

	var stdout, stderr []OutputMessage
	var results []*Result
	exec, err := sbx.RunCode(context.Background(), "x = 1;x", &RunCodeOpts{
		OnStdout: func(o OutputMessage) { stdout = append(stdout, o) },
		OnStderr: func(o OutputMessage) { stderr = append(stderr, o) },
		OnResult: func(r *Result) { results = append(results, r) },
	})
	if err != nil {
		t.Fatalf("run code: %v", err)
	}
	if len(stdout) != 1 || stdout[0].Line != "Hello from e2b\n" {
		t.Errorf("stdout cb: %+v", stdout)
	}
	if len(stderr) != 1 || stderr[0].Line != "This is an error message\n" || !stderr[0].Error {
		t.Errorf("stderr cb: %+v", stderr)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result callback, got %d", len(results))
	}
	if exec.Logs.Stdout[0] != "Hello from e2b\n" {
		t.Errorf("logs stdout: %+v", exec.Logs.Stdout)
	}
	if exec.Logs.Stderr[0] != "This is an error message\n" {
		t.Errorf("logs stderr: %+v", exec.Logs.Stderr)
	}
}

func TestRunCode_ErrorCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type": "error", "name": "NameError", "value": "name 'xyz' is not defined", "traceback": "..."}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	var errs []*ExecutionError
	exec, err := sbx.RunCode(context.Background(), "xyz", &RunCodeOpts{
		OnError: func(e *ExecutionError) { errs = append(errs, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error callback, got %d", len(errs))
	}
	if exec.Error == nil || exec.Error.Name != "NameError" {
		t.Errorf("error not captured: %+v", exec.Error)
	}
}

func TestRunCode_StreamingResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ndjson(
			`{"type": "result", "png": "abc", "text": "<Figure>"}`,
			`{"type": "result", "text": "final", "is_main_result": true}`,
		))
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	var out []*Result
	exec, err := sbx.RunCode(context.Background(), "plot()", &RunCodeOpts{
		OnResult: func(r *Result) { out = append(out, r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Errorf("expected 2 streaming results, got %d", len(out))
	}
	if exec.Text() != "final" {
		t.Errorf("expected final text 'final', got %q", exec.Text())
	}
}

func TestRunCode_DisplayData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type": "result", "text": "<Figure>", "png": "base64data", "is_main_result": true}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "plt.show()", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(exec.Results))
	}
	r := exec.Results[0]
	if r.PNG == "" {
		t.Error("expected PNG data")
	}
	if r.Text == "" {
		t.Error("expected text")
	}
	if len(r.Extra) != 0 {
		t.Errorf("expected no extra keys, got %+v", r.Extra)
	}
}

func TestRunCode_DataRepresentation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type": "result", "data": {"a": [1, 2, 3]}, "text": "df", "is_main_result": true}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "df", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := exec.Results[0]
	if r.Data == nil {
		t.Fatal("expected Data to be set")
	}
	arr, ok := r.Data["a"].([]any)
	if !ok || len(arr) != 3 {
		t.Errorf("unexpected data: %+v", r.Data)
	}
}

func TestRunCode_CustomReprLatexOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type": "result", "latex": "\\text{X}", "is_main_result": true}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "display()", nil)
	if err != nil {
		t.Fatal(err)
	}
	formats := exec.Results[0].Formats()
	if len(formats) != 1 || formats[0] != "latex" {
		t.Errorf("expected ['latex'], got %+v", formats)
	}
}

func TestRunCode_ExecutionCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ndjson(
			`{"type": "result", "text": "/home/user", "is_main_result": true}`,
			`{"type": "number_of_executions", "execution_count": 2}`,
		))
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "!pwd", nil)
	if err != nil {
		t.Fatal(err)
	}
	if exec.ExecutionCount != 2 {
		t.Errorf("expected count 2, got %d", exec.ExecutionCount)
	}
}

func TestRunCode_PassContextAndLanguage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not have been called")
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	_, err := sbx.RunCode(context.Background(), "console.log('Hello')", &RunCodeOpts{
		Language: LanguageJavaScript,
		Context:  &Context{ID: "ctx"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*InvalidArgumentError); !ok {
		t.Errorf("expected *InvalidArgumentError, got %T", err)
	}
}

func TestRunCode_LanguageIsSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if body["language"] != "javascript" {
			t.Errorf("expected language=javascript, got %v", body["language"])
		}
		_, _ = io.WriteString(w, `{"type": "result", "text": "Hello, World!", "is_main_result": true}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	exec, err := sbx.RunCode(context.Background(), "console.log('Hello')", &RunCodeOpts{
		Language: LanguageJavaScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	if exec.Text() != "Hello, World!" {
		t.Errorf("text: %q", exec.Text())
	}
}

func TestRunCode_EnvVarsAreSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		env, ok := body["env_vars"].(map[string]any)
		if !ok {
			t.Fatalf("env_vars missing: %+v", body)
		}
		if env["FOO"] != "bar" {
			t.Errorf("env FOO: %v", env["FOO"])
		}
		_, _ = io.WriteString(w, `{"type": "result", "text": "ok", "is_main_result": true}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	_, err := sbx.RunCode(context.Background(), "x", &RunCodeOpts{
		Envs: map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunCode_BackendErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "server error")
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	_, err := sbx.RunCode(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*SandboxError); !ok {
		t.Errorf("expected *SandboxError, got %T: %v", err, err)
	}
}

func TestRunCode_502IsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	_, err := sbx.RunCode(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*TimeoutError); !ok {
		t.Errorf("expected *TimeoutError, got %T", err)
	}
}

func TestCreateCodeContext_DefaultOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertJupyterRequest(t, r, "POST", "/contexts")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id": "ctx-new", "language": "python", "cwd": "/home/user"}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	c, err := sbx.CreateCodeContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "ctx-new" || c.Language != "python" || c.Cwd != "/home/user" {
		t.Errorf("unexpected context: %+v", c)
	}
}

func TestCreateCodeContext_WithOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if body["language"] != "python" {
			t.Errorf("language: %v", body["language"])
		}
		if body["cwd"] != "/root" {
			t.Errorf("cwd: %v", body["cwd"])
		}
		_, _ = io.WriteString(w, `{"id": "ctx-x", "language": "python", "cwd": "/root"}`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	c, err := sbx.CreateCodeContext(context.Background(), &CreateCodeContextOpts{
		Language: LanguagePython,
		Cwd:      "/root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Cwd != "/root" {
		t.Errorf("cwd: %q", c.Cwd)
	}
}

func TestListCodeContexts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertJupyterRequest(t, r, "GET", "/contexts")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id": "ctx-1", "language": "python", "cwd": "/home/user"},
			{"id": "ctx-2", "language": "javascript", "cwd": "/home/user"}
		]`)
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	contexts, err := sbx.ListCodeContexts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected 2, got %d", len(contexts))
	}

	// default contexts should include python and javascript
	langs := map[string]bool{}
	for _, c := range contexts {
		langs[c.Language] = true
	}
	if !langs["python"] || !langs["javascript"] {
		t.Errorf("expected python + javascript, got %+v", langs)
	}
}

func TestRestartCodeContext(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	if err := sbx.RestartCodeContext(context.Background(), "ctx-xy"); err != nil {
		t.Fatal(err)
	}
	if path != "/contexts/ctx-xy/restart" {
		t.Errorf("path: %q", path)
	}

	if err := sbx.RestartCodeContext(context.Background(), &Context{ID: "ctx-zz"}); err != nil {
		t.Fatal(err)
	}
	if path != "/contexts/ctx-zz/restart" {
		t.Errorf("path with *Context: %q", path)
	}
}

func TestRemoveCodeContext(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != "DELETE" {
			t.Errorf("method: %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/contexts/ctx-1") {
			t.Errorf("path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	if err := sbx.RemoveCodeContext(context.Background(), "ctx-1"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected delete call")
	}
}

func TestCreateCodeContext_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "context gone")
	}))
	defer srv.Close()

	sbx := newMockSandbox(t, srv)
	_, err := sbx.CreateCodeContext(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected *NotFoundError, got %T", err)
	}
}

func TestRestartCodeContext_NilContext(t *testing.T) {
	sbx := newMockSandbox(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not have been called")
	})))

	err := sbx.RestartCodeContext(context.Background(), (*Context)(nil))
	if err == nil {
		t.Fatal("expected error on nil context")
	}
	if _, ok := err.(*InvalidArgumentError); !ok {
		t.Errorf("expected *InvalidArgumentError, got %T", err)
	}
}
func assertJupyterRequest(t *testing.T, r *http.Request, method, path string) {
	t.Helper()
	if r.Method != method {
		t.Errorf("method: got %s, want %s", r.Method, method)
	}
	if r.URL.Path != path {
		t.Errorf("path: got %s, want %s", r.URL.Path, path)
	}
}

func assertExecuteBody(t *testing.T, r *http.Request, wantCode, wantCtxID, wantLang string) {
	t.Helper()
	body := readJSON(t, r)
	if body["code"] != wantCode {
		t.Errorf("code: got %v, want %q", body["code"], wantCode)
	}
	if wantCtxID != "" && body["context_id"] != wantCtxID {
		t.Errorf("context_id: got %v, want %q", body["context_id"], wantCtxID)
	}
	if wantLang != "" && body["language"] != wantLang {
		t.Errorf("language: got %v, want %q", body["language"], wantLang)
	}
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse body: %v (raw=%s)", err, string(b))
	}
	return m
}
