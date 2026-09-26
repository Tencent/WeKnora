package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pluginclient "github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const (
	oauthToken = "access-token-1"
	cloudID    = "cloud-1"
	email      = "me@example.com"
	apiToken   = "api-token"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

// fakeJira is a Jira Cloud site reachable both directly (API token) and
// through the OAuth gateway (/ex/jira/{cloudId}).
type fakeJira struct {
	*httptest.Server
	mu     sync.Mutex
	issues map[string]map[string]any // id → issue JSON
	jql    []string
}

func newFakeJira(t *testing.T) *fakeJira {
	f := &fakeJira{issues: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/token/accessible-resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+oauthToken {
			http.Error(w, `{"message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		writeJSON(w, []site{{ID: cloudID, URL: f.URL, Name: "Acme"}})
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /rest/api/3/myself", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, myself{AccountID: "a1", DisplayName: "Me", TimeZone: "Asia/Shanghai"})
	})
	api.HandleFunc("GET /rest/api/3/project/search", func(w http.ResponseWriter, r *http.Request) {
		all := []project{{Key: "ENG", Name: "Engineering"}, {Key: "OPS", Name: "Operations"}}
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		// One per page, to exercise paging.
		writeJSON(w, map[string]any{"values": all[start : start+1], "isLast": start+1 == len(all)})
	})
	api.HandleFunc("GET /rest/api/3/issuetype", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []issueType{{Name: "Task"}, {Name: "Bug"}, {Name: "Bug"}, {Name: "Sub-task", Subtask: true}})
	})
	api.HandleFunc("POST /rest/api/3/search/jql", f.search)
	api.HandleFunc("GET /rest/api/3/issue/{key}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, is := range f.issues {
			if is["key"] == r.PathValue("key") {
				writeJSON(w, is)
				return
			}
		}
		http.Error(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`,
			http.StatusNotFound)
	})
	mux.Handle("/ex/jira/"+cloudID+"/", http.StripPrefix("/ex/jira/"+cloudID, bearer(api)))
	mux.Handle("/rest/", basic(api))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	old := gatewayURL
	gatewayURL = f.URL
	t.Cleanup(func() { gatewayURL = old })
	return f
}

func bearer(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+oauthToken {
			http.Error(w, `{"errorMessages":["token"]}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func basic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != email || p != apiToken {
			http.Error(w, `{"errorMessages":["Client must be authenticated"]}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

var (
	projectClause = regexp.MustCompile(`project = "([A-Z]+)"`)
	updatedClause = regexp.MustCompile(`updated >= "([0-9/: ]+)"`)
)

// search understands the clauses the connector writes and pages two
// issues at a time, oldest update first.
func (f *fakeJira) search(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JQL           string   `json:"jql"`
		NextPageToken string   `json:"nextPageToken"`
		Fields        []string `json:"fields"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if strings.Contains(in.JQL, "BROKEN") {
		http.Error(w, `{"errorMessages":["Error in the JQL Query"]}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jql = append(f.jql, in.JQL)
	key := ""
	if m := projectClause.FindStringSubmatch(in.JQL); m != nil {
		key = m[1]
	}
	var since time.Time
	if m := updatedClause.FindStringSubmatch(in.JQL); m != nil {
		since, _ = time.ParseInLocation("2006/01/02 15:04", m[1], shanghai)
	}
	var list []map[string]any
	for _, is := range f.issues {
		fields := is["fields"].(map[string]any)
		updated, _ := time.Parse("2006-01-02T15:04:05.000-0700", fields["updated"].(string))
		if strings.HasPrefix(is["key"].(string), key) && !updated.Before(since) {
			list = append(list, is)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		updated := func(is map[string]any) string { return is["fields"].(map[string]any)["updated"].(string) }
		return updated(list[i]) < updated(list[j])
	})
	start, _ := strconv.Atoi(in.NextPageToken)
	end := min(start+2, len(list))
	page := map[string]any{"issues": list[start:end], "isLast": end == len(list)}
	if end < len(list) {
		page["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, page)
}

func (f *fakeJira) put(id, key, summary, updated string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[id] = map[string]any{"id": id, "key": key, "fields": map[string]any{
		"summary": summary,
		"updated": updated,
		"created": "2026-09-01T09:00:00.000+0800",
		"status":  map[string]any{"name": "In Progress"},
		"description": map[string]any{"type": "doc", "version": 1, "content": []any{
			map[string]any{"type": "paragraph", "content": []any{
				map[string]any{"type": "text", "text": "Steps to "},
				map[string]any{"type": "text", "text": "reproduce", "marks": []any{map[string]any{"type": "strong"}}},
			}},
		}},
		"comment": map[string]any{"total": 3, "comments": []any{map[string]any{
			"author":  map[string]any{"displayName": "Ann"},
			"created": "2026-09-02T09:00:00.000+0800",
			"body": map[string]any{
				"type": "doc",
				"content": []any{
					map[string]any{
						"type":    "paragraph",
						"content": []any{map[string]any{"type": "text", "text": "Seen on staging"}},
					},
				},
			},
		}}},
	}}
}

func (f *fakeJira) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.issues, id)
}

func (f *fakeJira) lastJQL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jql[len(f.jql)-1]
}

// run is the plugin behind an HTTP server, called like WeKnora calls it.
type run struct {
	t      *testing.T
	client *pluginclient.Client
}

func newRun(t *testing.T) *run {
	srv := httptest.NewServer(newPlugin().Handler())
	t.Cleanup(srv.Close)
	return &run{t: t, client: pluginclient.New(srv.URL, nil, nil)}
}

func tokenInstance(f *fakeJira, settings map[string]any) map[string]any {
	return map[string]any{
		"credentials": map[string]any{"auth": "token", "base_url": f.URL + "/", "email": email, "api_token": apiToken},
		"settings":    settings,
	}
}

func oauthInstance(site string) map[string]any {
	// WeKnora has already swapped the "oauth:<id>" reference for a token.
	return map[string]any{
		"credentials": map[string]any{"auth": "oauth", "account": oauthToken, "site": site},
		"settings":    map[string]any{},
	}
}

type fetched struct {
	items       []pluginapi.FetchedItem
	checkpoints int
	cursor      *pluginapi.Cursor
}

func (r *run) fetch(instance map[string]any, mode string, cursor *pluginapi.Cursor, projects ...string) fetched {
	r.t.Helper()
	var out fetched
	end, err := r.client.Stream(context.Background(), pluginapi.ConnectorFetchPath("jira"),
		pluginapi.Envelope{Config: pluginapi.Config{Instance: instance}},
		pluginapi.FetchInput{Mode: mode, Cursor: cursor, ResourceIDs: projects},
		func(ev pluginapi.Event) error {
			switch ev.Type {
			case pluginapi.EventItem:
				var it pluginapi.FetchedItem
				if err := json.Unmarshal(ev.Data, &it); err != nil {
					return err
				}
				out.items = append(out.items, it)
			case pluginapi.EventCheckpoint:
				out.checkpoints++
			}
			return nil
		})
	if err != nil {
		r.t.Fatalf("fetch: %v", err)
	}
	out.cursor = &pluginapi.Cursor{}
	if err := json.Unmarshal(end, out.cursor); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *run) call(path string, instance map[string]any, input, out any) error {
	return r.client.Call(context.Background(), path,
		pluginapi.Envelope{Config: pluginapi.Config{Instance: instance}}, input, out)
}

func summary(items []pluginapi.FetchedItem) []string {
	var out []string
	for _, it := range items {
		if it.IsDeleted {
			out = append(out, "-"+it.ExternalID)
		} else {
			out = append(out, it.ExternalID+" "+it.Title)
		}
	}
	return out
}

func TestSyncIsIncrementalAndReportsDeletions(t *testing.T) {
	f := newFakeJira(t)
	f.put("101", "ENG-1", "Login fails", "2026-09-20T10:00:00.000+0800")
	f.put("102", "ENG-2", "Slow search", "2026-09-21T10:00:30.000+0800")
	f.put("103", "ENG-3", "Crash on save", "2026-09-22T10:00:00.000+0800")
	f.put("201", "OPS-1", "Not selected", "2026-09-22T10:00:00.000+0800")
	r := newRun(t)
	inst := tokenInstance(f, map[string]any{"issue_types": []string{"Bug", `Odd "type"`}, "jql": "labels = x"})

	full := r.fetch(inst, pluginapi.FetchFull, nil, "ENG")
	if got := summary(full.items); strings.Join(
		got,
		"|",
	) != "101 ENG-1 Login fails|102 ENG-2 Slow search|103 ENG-3 Crash on save" {
		t.Fatalf("full sync = %v", got)
	}
	if full.checkpoints != 2 {
		t.Fatalf("a checkpoint per page, got %d", full.checkpoints)
	}
	wantJQL := `project = "ENG" AND issuetype in ("Bug", "Odd \"type\"") AND (labels = x) ORDER BY updated ASC`
	if jql := f.lastJQL(); jql != wantJQL {
		t.Fatalf("jql = %s", jql)
	}
	doc := string(full.items[0].Content)
	for _, want := range []string{
		"# ENG-1: Login fails", "- **Status:** In Progress", "- **Link:** " + f.URL + "/browse/ENG-1",
		"## Description\n\nSteps to **reproduce**", "### Ann · 2026-09-02 09:00 +0800\n\nSeen on staging",
		"_2 more comments are not included._",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q:\n%s", want, doc)
		}
	}
	if it := full.items[0]; it.SourceResourceID != "ENG" || it.Metadata["jira_key"] != "ENG-1" ||
		it.FileName != "ENG-1 Login fails.md" || it.UpdatedAt == nil {
		t.Fatalf("item = %+v", it)
	}

	// Incremental: from the newest update seen, in the user's time zone.
	f.put("102", "ENG-2", "Slow search (again)", "2026-09-25T08:00:00.000+0800")
	inc := r.fetch(inst, pluginapi.FetchIncremental, full.cursor, "ENG")
	if !strings.Contains(f.lastJQL(), `updated >= "2026/09/22 10:00"`) {
		t.Fatalf("incremental jql = %s", f.lastJQL())
	}
	if got := summary(inc.items); strings.Join(got, "|") != "103 ENG-3 Crash on save|102 ENG-2 Slow search (again)" {
		t.Fatalf("incremental = %v", got)
	}

	// A full sync reports what disappeared, once.
	f.remove("101")
	again := r.fetch(inst, pluginapi.FetchFull, inc.cursor, "ENG")
	if got := summary(again.items); got[len(got)-1] != "-101" || len(got) != 3 {
		t.Fatalf("full sync after a deletion = %v", got)
	}
	once := r.fetch(inst, pluginapi.FetchFull, again.cursor, "ENG")
	for _, it := range once.items {
		if it.IsDeleted {
			t.Fatalf("deletion reported twice: %v", summary(once.items))
		}
	}
}

func TestOAuthAccountsReachTheirSites(t *testing.T) {
	f := newFakeJira(t)
	f.put("101", "ENG-1", "Login fails", "2026-09-20T10:00:00.000+0800")
	r := newRun(t)

	var sites pluginapi.OptionsOutput
	err := r.call(pluginapi.OptionsPath("sites"), oauthInstance(""), pluginapi.OptionsInput{Field: "site"}, &sites)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites.Options) != 1 || sites.Options[0].Value != cloudID || sites.Options[0].Label != "Acme" {
		t.Fatalf("sites = %+v", sites)
	}
	var types pluginapi.OptionsOutput
	err = r.call(pluginapi.OptionsPath("issue_types"), oauthInstance(cloudID), pluginapi.OptionsInput{}, &types)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(types.Options) != "[{Bug  } {Sub-task  } {Task  }]" {
		t.Fatalf("issue types = %v", types.Options)
	}
	var res pluginapi.ListResourcesOutput
	listPath := pluginapi.ConnectorListResourcesPath("jira")
	err = r.call(listPath, oauthInstance(cloudID), pluginapi.ListResourcesInput{}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 2 || res.Resources[1].ExternalID != "OPS" || res.Resources[0].URL != f.URL+"/browse/ENG" {
		t.Fatalf("projects = %+v", res.Resources)
	}
	got := r.fetch(oauthInstance(cloudID), pluginapi.FetchFull, nil, "ENG")
	if len(got.items) != 1 || got.items[0].URL != f.URL+"/browse/ENG-1" {
		t.Fatalf("oauth sync = %v", summary(got.items))
	}
}

func TestProblemsReachTheForm(t *testing.T) {
	f := newFakeJira(t)
	r := newRun(t)
	cases := []struct {
		name     string
		instance map[string]any
		code     pluginapi.ErrorCode
	}{
		{"no account", map[string]any{"credentials": map[string]any{"auth": "oauth"}}, pluginapi.CodeInvalidConfig},
		{"site not granted", oauthInstance("cloud-2"), pluginapi.CodeInvalidConfig},
		{
			"expired token",
			map[string]any{"credentials": map[string]any{"auth": "oauth", "account": "old", "site": cloudID}},
			pluginapi.CodeUnauthorized,
		},
		{
			"missing token fields",
			map[string]any{"credentials": map[string]any{"auth": "token"}},
			pluginapi.CodeInvalidConfig,
		},
		{"wrong api token", map[string]any{"credentials": map[string]any{
			"auth": "token", "base_url": f.URL, "email": email, "api_token": "nope",
		}}, pluginapi.CodeUnauthorized},
	}
	for _, tc := range cases {
		err := r.call(pluginapi.ConnectorValidatePath("jira"), tc.instance, struct{}{}, nil)
		if pe, ok := pluginapi.AsError(err); !ok || pe.Code != tc.code {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// Messages about the form speak the user's language.
	err := r.client.Call(context.Background(), pluginapi.OptionsPath("sites"), pluginapi.Envelope{
		Context: pluginapi.Context{Locale: "zh-CN"},
		Config:  pluginapi.Config{Instance: map[string]any{"credentials": map[string]any{"auth": "oauth"}}},
	}, pluginapi.OptionsInput{}, nil)
	if pe, ok := pluginapi.AsError(err); !ok || pe.Message != msgConnectAccount.zh {
		t.Fatalf("zh-CN hint = %v", err)
	}
	if err := r.call(pluginapi.ConnectorValidatePath("jira"), tokenInstance(f, nil), struct{}{}, nil); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	_, err = r.client.Stream(
		context.Background(),
		pluginapi.ConnectorFetchPath("jira"),
		pluginapi.Envelope{Config: pluginapi.Config{Instance: tokenInstance(f, map[string]any{"jql": "BROKEN"})}},
		pluginapi.FetchInput{
			Mode:        pluginapi.FetchFull,
			ResourceIDs: []string{"ENG"},
		},
		func(pluginapi.Event) error { return nil },
	)
	if pe, ok := pluginapi.AsError(err); !ok || pe.Code != pluginapi.CodeInvalidConfig ||
		!strings.Contains(pe.Message, "JQL") {
		t.Fatalf("bad jql = %v", err)
	}
}

func TestRateLimitsAreRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &client{api: srv.URL}
	_, err := c.myself(context.Background())
	pe, ok := pluginapi.AsError(err)
	if !ok || pe.Code != pluginapi.CodeRateLimited || !pe.Retryable || pe.Details.RetryAfter != 7 {
		t.Fatalf("429 = %v", err)
	}
}

func TestADFToMarkdown(t *testing.T) {
	doc, err := os.ReadFile("testdata/adf.json")
	if err != nil {
		t.Fatal(err)
	}
	want := "## Plan\n\n- one\n  1. nested\n- [docs](https://example.com) by @Ann\n\n```go\nx := 1\n```\n\n" +
		"| a\\|b |\n| --- |\n| 1 |\n\n[attachment]\n\nkept"
	if got := adfToMarkdown(doc); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if adfToMarkdown(json.RawMessage(`"plain"`)) != "plain" || adfToMarkdown(nil) != "" {
		t.Fatal("plain strings pass through")
	}
}

func TestJQLDatesUseTheUsersZone(t *testing.T) {
	since := time.Date(2026, 9, 22, 2, 0, 59, 0, time.UTC)
	got := buildJQL("ENG", settings{}, since, shanghai)
	if got != `project = "ENG" AND updated >= "2026/09/22 10:00" ORDER BY updated ASC` {
		t.Fatal(got)
	}
}

// The example must stay an external plugin: the SDK and ordinary libraries,
// nothing from WeKnora's internals.
func TestPluginImportsNoInternals(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.Contains(imp.Path.Value, "github.com/Tencent/WeKnora/internal") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}

func TestPluginPassesConformance(t *testing.T) {
	srv := httptest.NewServer(newPlugin().Handler())
	defer srv.Close()
	rep := conformance.Run(context.Background(), conformance.Target{
		Client: pluginclient.New(srv.URL, nil, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, bytes.NewReader(body))
			return http.DefaultClient.Do(req)
		},
	})
	for _, r := range rep.Results {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Detail)
		}
	}
}

func TestAgentTools(t *testing.T) {
	f := newFakeJira(t)
	f.put("101", "ENG-1", "Login fails", "2026-09-20T10:00:00.000+0800")
	f.put("102", "ENG-2", "Login is slow", "2026-09-21T10:00:00.000+0800")
	f.put("103", "ENG-3", "Other", "2026-09-22T10:00:00.000+0800")
	r := newRun(t)
	tenant := map[string]any{"auth": "token", "base_url": f.URL, "email": email, "api_token": apiToken}
	call := func(tool string, args string, tenant map[string]any, locale string) pluginapi.ToolResult {
		t.Helper()
		req := pluginapi.MCPRequest{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
			Params: json.RawMessage(`{"name":"` + tool + `","arguments":` + args + `}`),
		}
		var out pluginapi.MCPResponse
		env := pluginapi.Envelope{Context: pluginapi.Context{Locale: locale}, Config: pluginapi.Config{Tenant: tenant}}
		if err := r.client.Call(context.Background(), pluginapi.MCPPath(toolsServer), env, req, &out); err != nil {
			t.Fatal(err)
		}
		var res pluginapi.ToolResult
		if out.Error != nil || json.Unmarshal(out.Result, &res) != nil {
			t.Fatalf("%s: %+v", tool, out)
		}
		return res
	}

	res := call("search_issues", `{"text":"login","project":"ENG","limit":1}`, tenant, "")
	if f.lastJQL() != `project = "ENG" AND text ~ "login" ORDER BY updated DESC` || res.IsError {
		t.Fatalf("jql = %s, %+v", f.lastJQL(), res)
	}
	rows, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(rows), `"key":"ENG-1"`) || !strings.Contains(string(rows), `"more":true`) ||
		!strings.Contains(res.Content[0].Text, "- ENG-1 [In Progress] Login fails (unassigned") {
		t.Fatalf("search = %s\n%s", rows, res.Content[0].Text)
	}
	call("search_issues", `{"jql":"assignee = currentUser()"}`, tenant, "")
	if f.lastJQL() != "assignee = currentUser() ORDER BY updated DESC" {
		t.Fatalf("jql = %s", f.lastJQL())
	}
	if res := call("search_issues", `{}`, tenant, ""); !res.IsError {
		t.Fatal("a search needs jql or text")
	}

	res = call("get_issue", `{"key":"eng-2"}`, tenant, "")
	md, _ := json.Marshal(res.StructuredContent)
	if res.IsError || !strings.HasPrefix(res.Content[0].Text, "# ENG-2: Login is slow") ||
		!strings.Contains(string(md), `"url":"`+f.URL+`/browse/ENG-2"`) {
		t.Fatalf("get_issue = %+v", res)
	}
	if res := call("get_issue", `{"key":"ENG-9"}`, tenant, ""); !res.IsError ||
		!strings.Contains(res.Content[0].Text, "does not exist") {
		t.Fatalf("missing issue = %+v", res)
	}
	if res := call("get_issue", `{"key":"DROP TABLE"}`, tenant, ""); !res.IsError {
		t.Fatal("a malformed key reaches Jira")
	}
	res = call("search_issues", `{"text":"x"}`, map[string]any{"auth": "oauth"}, "zh-CN")
	if !res.IsError || !strings.Contains(res.Content[0].Text, "本空间尚未连接 Jira") {
		t.Fatalf("unconfigured = %+v", res)
	}

	// The workspace form offers sites and issue types too.
	var sites pluginapi.OptionsOutput
	err := r.client.Call(context.Background(), pluginapi.OptionsPath("sites"),
		pluginapi.Envelope{Config: pluginapi.Config{Tenant: map[string]any{"auth": "oauth", "account": oauthToken}}},
		pluginapi.OptionsInput{Field: "site", Scope: pluginapi.OptionsScopeTenant}, &sites)
	if err != nil || len(sites.Options) != 1 || sites.Options[0].Value != cloudID {
		t.Fatalf("tenant sites = %+v, %v", sites, err)
	}
}
