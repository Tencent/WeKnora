package yuque

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
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

// fakeYuque sets up an httptest server emulating Yuque API endpoints.
// Handlers can be overridden per test.
type fakeYuque struct {
	server *httptest.Server
	mux    *http.ServeMux
	calls  []string // methodPath history
}

func newFakeYuque() *fakeYuque {
	f := &fakeYuque{mux: http.NewServeMux()}
	f.server = httptest.NewServer(f.mux)
	// Default /api/v2/user handler (most tests hit it at least once).
	f.handleJSON("/api/v2/user", 200, v2UserResponse{Data: v2User{ID: 1, Login: "me", Name: "Me"}})
	return f
}

func (f *fakeYuque) Close() { f.server.Close() }

func (f *fakeYuque) cfg() *Config {
	return &Config{APIToken: "tok-super-secret-value-1234", BaseURL: f.server.URL}
}

func (f *fakeYuque) handleJSON(path string, status int, body interface{}) {
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
}

func TestClient_Ping_Success(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()

	c := newClient(f.cfg())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping error: %v", err)
	}
}

func TestClient_Ping_SendsAuthHeader(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()

	var gotToken string
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Auth-Token")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v2UserResponse{Data: v2User{ID: 1, Login: "me"}})
	})
	f.server.Config.Handler = f.mux

	c := newClient(f.cfg())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping error: %v", err)
	}
	if gotToken != "tok-super-secret-value-1234" {
		t.Errorf("X-Auth-Token = %q, want the token", gotToken)
	}
}

func TestClient_Ping_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"message":"Unauthorized"}`)
	}))
	defer srv.Close()

	c := newClient(&Config{APIToken: "bad", BaseURL: srv.URL})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Errorf("401 should wrap ErrInvalidCredentials, got: %v", err)
	}
}

func TestClient_Ping_403WrapsInvalidCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"message":"Forbidden"}`)
	}))
	defer srv.Close()

	c := newClient(&Config{APIToken: "insufficient", BaseURL: srv.URL})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Errorf("403 should wrap ErrInvalidCredentials, got: %v", err)
	}
}

func TestClient_TokenNeverLoggedInFull(t *testing.T) {
	// Redirect the project's internal logger to an in-memory buffer so we can
	// assert the raw token never appears in log output. Using stdlib `log`
	// would be vacuous — the real logger is a private logrus instance.
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	f := newFakeYuque()
	defer f.Close()

	rawToken := "tok-super-secret-value-1234"
	cfg := &Config{APIToken: rawToken, BaseURL: f.server.URL}
	c := newClient(cfg)
	_ = c.Ping(context.Background())

	out := buf.String()
	if out == "" {
		t.Fatal("expected logger output (sanity check — SetOutput wiring may be broken)")
	}
	if strings.Contains(out, rawToken) {
		t.Errorf("raw token leaked into logs:\n%s", out)
	}
	// Verify the redacted form was emitted (positive assertion, not just absence).
	if !strings.Contains(out, redactToken(rawToken)) {
		t.Errorf("expected redacted token %q in logs, got:\n%s", redactToken(rawToken), out)
	}
}

func TestRedactToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{"short", "***"},
		{"abcdef1234567890", "abcdef...7890"},
	}
	for _, tt := range tests {
		if got := redactToken(tt.in); got != tt.want {
			t.Errorf("redactToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestClient_429WithRetryAfter_Retries(t *testing.T) {
	attempt := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			// "0" is coerced to 100ms inside the client so the test stays fast.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"message":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v2UserResponse{Data: v2User{ID: 1, Login: "me"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newClient(&Config{APIToken: "t", BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if attempt < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempt)
	}
}

func TestClient_429ExhaustsRetries(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"message":"rate limited"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newClient(&Config{APIToken: "t", BaseURL: srv.URL})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error when 429s exceed retry budget")
	}
	if attempts != 4 { // initial + 3 retries
		t.Errorf("attempts = %d, want 4 (1 + 3 retries)", attempts)
	}
}

func TestClient_5xxRetriesOnce(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"message":"internal error"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Make the fixed 2-second 5xx sleep negligible by using a context whose
	// deadline fires after just one retry cycle. We still expect exactly 2
	// attempts because the implementation should retry 5xx once and then stop.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := newClient(&Config{APIToken: "t", BaseURL: srv.URL})
	if err := c.Ping(ctx); err == nil {
		t.Fatal("expected error after 5xx exhaustion")
	}
	if attempts != 2 { // initial + 1 retry
		t.Errorf("attempts = %d, want 2 (5xx retries exactly once)", attempts)
	}
}

func TestClient_4xxNotRetried(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"message":"bad request"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newClient(&Config{APIToken: "t", BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (non-429/5xx 4xx must not retry)", attempts)
	}
}

func TestParseRetryAfter(t *testing.T) {
	fallback := 5 * time.Second
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", fallback},
		{"0", 100 * time.Millisecond},
		{"-1", 100 * time.Millisecond}, // negative coerced
		{"3", 3 * time.Second},
		{"abc", fallback}, // unparseable
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.header, fallback); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestClient_GetCurrentUser(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	// Override the default /api/v2/user handler via a fresh mux.
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v2UserResponse{Data: v2User{ID: 42, Login: "alice", Name: "Alice"}})
	})
	f.server.Config.Handler = f.mux

	c := newClient(f.cfg())
	u, err := c.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if u.ID != 42 || u.Login != "alice" {
		t.Errorf("got %+v, want {42 alice}", u)
	}
}

func TestClient_ListUserGroups(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/users/42/groups", 200, v2GroupListResponse{Data: []v2Group{
		{ID: 100, Login: "team-a", Name: "Team A"},
		{ID: 101, Login: "team-b", Name: "Team B"},
	}})

	c := newClient(f.cfg())
	gs, err := c.ListUserGroups(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListUserGroups: %v", err)
	}
	if len(gs) != 2 || gs[0].Login != "team-a" {
		t.Errorf("got %+v", gs)
	}
}

func TestClient_ListUserRepos_FiltersType(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	var gotQuery string
	f.mux.HandleFunc("/api/v2/users/alice/repos", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v2RepoListResponse{Data: []v2Repo{
			{ID: 9, Slug: "book1", Name: "Book 1", Type: "Book", Namespace: "alice/book1"},
		}})
	})

	c := newClient(f.cfg())
	repos, err := c.ListUserRepos(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ListUserRepos: %v", err)
	}
	if !strings.Contains(gotQuery, "type=Book") {
		t.Errorf("expected type=Book in query, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "offset=0") {
		t.Errorf("expected offset=0 in query, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "limit=100") {
		t.Errorf("expected limit=100 in query, got %q", gotQuery)
	}
	if len(repos) != 1 || repos[0].Namespace != "alice/book1" {
		t.Errorf("got %+v", repos)
	}
}

func TestClient_ListGroupRepos(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/groups/team-a/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 11, Slug: "tb", Name: "Team Book", Type: "Book", Namespace: "team-a/tb"},
	}})

	c := newClient(f.cfg())
	repos, err := c.ListGroupRepos(context.Background(), "team-a")
	if err != nil {
		t.Fatalf("ListGroupRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].ID != 11 {
		t.Errorf("got %+v", repos)
	}
}

func TestClient_ListBookDocs_Pagination(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	// Page 1: 100 docs, page 2: 30 docs, then stop.
	callCount := 0
	f.mux.HandleFunc("/api/v2/repos/555/docs", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		offset := r.URL.Query().Get("offset")
		docs := make([]v2Doc, 0)
		switch offset {
		case "0":
			for i := 0; i < 100; i++ {
				docs = append(docs, v2Doc{ID: int64(i + 1), Type: "Doc", Status: "1", Title: fmt.Sprintf("D%d", i+1), ContentUpdatedAt: "2026-04-20T00:00:00Z"})
			}
		case "100":
			for i := 0; i < 30; i++ {
				docs = append(docs, v2Doc{ID: int64(i + 101), Type: "Doc", Status: "1", Title: fmt.Sprintf("D%d", i+101), ContentUpdatedAt: "2026-04-20T00:00:00Z"})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v2DocListResponse{Data: docs})
	})

	c := newClient(f.cfg())
	docs, err := c.ListBookDocs(context.Background(), 555)
	if err != nil {
		t.Fatalf("ListBookDocs: %v", err)
	}
	if len(docs) != 130 {
		t.Errorf("len(docs) = %d, want 130", len(docs))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (one full page + one partial)", callCount)
	}
}

func TestClient_GetDocDetail(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/docs/7", 200, v2DocDetailResponse{
		Data: v2DocDetail{
			ID:               7,
			Title:            "Hello",
			Body:             "# Hello\n\nworld",
			Format:           "markdown",
			Status:           "1",
			ContentUpdatedAt: "2026-04-20T12:00:00Z",
		},
	})

	c := newClient(f.cfg())
	d, err := c.GetDocDetail(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetDocDetail: %v", err)
	}
	if d.Body != "# Hello\n\nworld" {
		t.Errorf("Body = %q", d.Body)
	}
}
