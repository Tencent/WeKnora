package codeinterpreter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConnectionConfig_DefaultsFromEnv(t *testing.T) {
	t.Setenv("E2B_API_KEY", "env-key")
	t.Setenv("E2B_DOMAIN", "example.dev")
	t.Setenv("E2B_ACCESS_TOKEN", "env-token")
	t.Setenv("E2B_DEBUG", "true")

	c := &ConnectionConfig{}
	c.init()

	if c.APIKey != "env-key" {
		t.Errorf("api key: %q", c.APIKey)
	}
	if c.Domain != "example.dev" {
		t.Errorf("domain: %q", c.Domain)
	}
	if c.AccessToken != "env-token" {
		t.Errorf("token: %q", c.AccessToken)
	}
	if !c.Debug {
		t.Error("expected debug to be true")
	}
	if c.RequestTimeout == 0 {
		t.Error("expected default request timeout")
	}
	if c.HTTPClient == nil {
		t.Error("http client should have been created")
	}
}

func TestConnectionConfig_ExplicitOverrides(t *testing.T) {
	// Clear env to make sure the explicit values win.
	_ = os.Unsetenv("E2B_API_KEY")
	_ = os.Unsetenv("E2B_DOMAIN")
	_ = os.Unsetenv("E2B_ACCESS_TOKEN")
	_ = os.Unsetenv("E2B_DEBUG")

	c := &ConnectionConfig{
		APIKey:         "my-key",
		Domain:         "my.dev",
		AccessToken:    "tok",
		RequestTimeout: 10 * time.Second,
	}
	c.init()

	if c.APIKey != "my-key" || c.Domain != "my.dev" || c.AccessToken != "tok" {
		t.Errorf("explicit values not kept: %+v", c)
	}
	if c.RequestTimeout != 10*time.Second {
		t.Errorf("timeout overwritten: %v", c.RequestTimeout)
	}
}

func TestConnectionConfig_APIBase(t *testing.T) {
	c := &ConnectionConfig{Domain: "e2b.app"}
	if got := c.APIBase(); got != "https://api.e2b.app" {
		t.Errorf("api base: %s", got)
	}
	c.Debug = true
	if got := c.APIBase(); got != "http://api.e2b.app" {
		t.Errorf("debug api base: %s", got)
	}
}

func TestSandbox_GetHost(t *testing.T) {
	sbx := &Sandbox{
		id:         "abc",
		clientID:   "xyz",
		connection: &ConnectionConfig{Domain: "e2b.app"},
	}
	got := sbx.GetHost(3000)
	if got != "3000-abc-xyz.e2b.app" {
		t.Errorf("host: %q", got)
	}

	sbx.clientID = ""
	got = sbx.GetHost(8080)
	if got != "8080-abc.e2b.app" {
		t.Errorf("host w/o client: %q", got)
	}
}

func TestSandbox_JupyterURL(t *testing.T) {
	sbx := &Sandbox{
		id:         "sid",
		clientID:   "cid",
		connection: &ConnectionConfig{Domain: "e2b.app"},
	}
	if got := sbx.jupyterURL(); got != "https://49999-sid-cid.e2b.app" {
		t.Errorf("jupyter url: %q", got)
	}
	sbx.connection.Debug = true
	if got := sbx.jupyterURL(); got != "http://49999-sid-cid.e2b.app" {
		t.Errorf("debug jupyter url: %q", got)
	}
}

func TestSandbox_AddAuthHeaders(t *testing.T) {
	sbx := &Sandbox{
		connection: &ConnectionConfig{
			AccessToken:        "tok",
			TrafficAccessToken: "traf",
		},
	}
	h := http.Header{}
	sbx.addAuthHeaders(h)
	if h.Get("Content-Type") != "application/json" {
		t.Error("content-type missing")
	}
	if h.Get("X-Access-Token") != "tok" {
		t.Errorf("access token: %q", h.Get("X-Access-Token"))
	}
	if h.Get("E2B-Traffic-Access-Token") != "traf" {
		t.Errorf("traffic token: %q", h.Get("E2B-Traffic-Access-Token"))
	}
}

func TestCreate_MissingAPIKey(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")

	_, err := Create(context.Background(), &SandboxOpts{})
	if err == nil {
		t.Fatal("expected error when API key missing")
	}
	if _, ok := err.(*AuthenticationError); !ok {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestErrorTypes_Error(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&NotFoundError{Message: "ctx"}, "not found: ctx"},
		{&TimeoutError{Message: "slow"}, "timeout: slow"},
		{&InvalidArgumentError{Message: "bad"}, "invalid argument: bad"},
		{&AuthenticationError{Message: "key"}, "authentication error: key"},
		{&RateLimitError{Message: "rl"}, "rate limit: rl"},
		{&SandboxError{Message: "boom"}, "sandbox error: boom"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error(): got %q, want %q", got, c.want)
		}
	}

	se := &SandboxError{Message: "oops", StatusCode: 500}
	if !strings.Contains(se.Error(), "500") {
		t.Errorf("sandbox error with status should contain code: %s", se.Error())
	}
}

func TestCreate_ThroughMockAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sandboxes":
			if r.Method != "POST" {
				t.Errorf("unexpected method: %s", r.Method)
			}
			if r.Header.Get("X-API-Key") != "my-key" {
				t.Errorf("api key header missing: %q", r.Header.Get("X-API-Key"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"sandboxID": "sbx-1", "clientID": "c-1", "templateID": "code-interpreter-v1", "envdPort": 49999}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}}

	sbx, err := Create(context.Background(), &SandboxOpts{
		APIKey:     "my-key",
		Domain:     "e2b.test",
		Debug:      true,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sbx.SandboxID() != "sbx-1" || sbx.ClientID() != "c-1" {
		t.Errorf("unexpected sandbox: id=%s client=%s", sbx.SandboxID(), sbx.ClientID())
	}
	if sbx.template != "code-interpreter-v1" {
		t.Errorf("template: %s", sbx.template)
	}
}

func TestKill_ThroughMockAPI(t *testing.T) {
	var killed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/sandboxes/") {
			killed = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	if err := sbx.Kill(context.Background()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !killed {
		t.Error("expected handler to have been called")
	}
}

func TestSetTimeout_ThroughMockAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/sandboxes/abc/timeout" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	if err := sbx.SetTimeout(context.Background(), 30*time.Second); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
}

func TestIsRunning_ReturnsFalseOnNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	running, err := sbx.IsRunning(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected sandbox to be reported as not running")
	}
}

func TestIsRunning_ReturnsTrueOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxID":"abc"}`))
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	running, err := sbx.IsRunning(context.Background())
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !running {
		t.Error("expected running=true")
	}
}

func TestList_ThroughMockAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes" || r.Method != "GET" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"sandboxID":"sbx-1","clientID":"c-1","templateID":"tpl","state":"running"},
			{"sandboxID":"sbx-2","clientID":"c-2","templateID":"tpl","state":"running"}
		]`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}}
	list, err := List(context.Background(), &SandboxOpts{
		APIKey:     "k",
		Domain:     "e2b.test",
		Debug:      true,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if list[0].SandboxID != "sbx-1" || list[1].SandboxID != "sbx-2" {
		t.Errorf("unexpected list: %+v", list)
	}
}

type rewriteToServerTransport struct {
	target string
}

func (r *rewriteToServerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	// parse target
	idx := strings.Index(r.target, "://")
	if idx == -1 {
		return nil, &SandboxError{Message: "invalid target"}
	}
	scheme := r.target[:idx]
	host := r.target[idx+3:]
	req.URL.Scheme = scheme
	req.URL.Host = host
	req.Host = host
	return http.DefaultTransport.RoundTrip(req)
}

func TestConnect_ThroughMockAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sandboxes/abc" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxID":"abc","clientID":"cid","templateID":"tpl"}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}}
	sbx, err := Connect(context.Background(), "abc", &SandboxOpts{
		APIKey:     "k",
		Domain:     "e2b.test",
		Debug:      true,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if sbx.SandboxID() != "abc" || sbx.ClientID() != "cid" {
		t.Errorf("unexpected: id=%s clientID=%s", sbx.SandboxID(), sbx.ClientID())
	}
}

func TestGetInfo_ThroughMockAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes/abc" || r.Method != "GET" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxID":"abc","clientID":"cid","state":"running","metadata":{"k":"v"}}`))
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	info, err := sbx.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("get info: %v", err)
	}
	if info.SandboxID != "abc" || info.State != "running" {
		t.Errorf("unexpected info: %+v", info)
	}
	if info.Metadata["k"] != "v" {
		t.Errorf("metadata: %+v", info.Metadata)
	}
}

func TestIsRunning_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sbx := &Sandbox{
		id: "abc",
		connection: &ConnectionConfig{
			APIKey:     "k",
			Domain:     "e2b.test",
			Debug:      true,
			HTTPClient: &http.Client{Transport: &rewriteToServerTransport{target: srv.URL}},
		},
	}
	running, err := sbx.IsRunning(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if running {
		t.Error("should not be running on error")
	}
}

func TestConnect_MissingAPIKey(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")
	_, err := Connect(context.Background(), "abc", nil)
	if err == nil {
		t.Fatal("expected error (no API key, no server)")
	}
}
