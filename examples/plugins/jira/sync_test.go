package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// fetchUntilFailure runs a sync expected to fail and returns its error and
// its last checkpoint, which WeKnora hands to the retry.
func (r *run) fetchUntilFailure(
	instance map[string]any, mode string, cursor *pluginapi.Cursor, projects ...string,
) (*pluginapi.Cursor, error) {
	r.t.Helper()
	var last *pluginapi.Cursor
	_, err := r.client.Stream(context.Background(), pluginapi.ConnectorFetchPath("jira"),
		pluginapi.Envelope{Config: pluginapi.Config{Instance: instance}},
		pluginapi.FetchInput{Mode: mode, Cursor: cursor, ResourceIDs: projects},
		func(ev pluginapi.Event) error {
			if ev.Type == pluginapi.EventCheckpoint {
				last = &pluginapi.Cursor{}
				return json.Unmarshal(ev.Data, last)
			}
			return nil
		})
	return last, err
}

func TestFullSyncReportsIssuesOfDeselectedProjects(t *testing.T) {
	f := newFakeJira(t)
	f.put("101", "ENG-1", "a", "2026-09-20T10:00:00.000+0800")
	f.put("201", "OPS-1", "b", "2026-09-22T10:00:00.000+0800")
	r := newRun(t)
	inst := tokenInstance(f, map[string]any{})
	full := r.fetch(inst, pluginapi.FetchFull, nil, "ENG", "OPS")

	// OPS is deselected. Incremental syncs cannot tell what is gone, so
	// they keep its issues for the next full sync.
	inc := r.fetch(inst, pluginapi.FetchIncremental, full.cursor, "ENG")
	if slices.ContainsFunc(inc.items, func(it pluginapi.FetchedItem) bool { return it.IsDeleted }) ||
		stateFrom(inc.cursor).Projects["OPS"] == nil {
		t.Fatalf("incremental = %v, cursor %v", summary(inc.items), inc.cursor.State)
	}
	again := r.fetch(inst, pluginapi.FetchFull, inc.cursor, "ENG")
	if got := strings.Join(summary(again.items), "|"); got != "101 ENG-1 a|-201" {
		t.Fatalf("full sync after deselecting OPS = %s", got)
	}
	if st := stateFrom(again.cursor); st.Projects["OPS"] != nil || st.Full != nil {
		t.Fatalf("cursor = %v", again.cursor.State)
	}
	// Selected again, OPS starts over; nothing is reported twice.
	f.remove("201")
	third := r.fetch(inst, pluginapi.FetchFull, again.cursor, "ENG", "OPS")
	if got := strings.Join(summary(third.items), "|"); got != "101 ENG-1 a" {
		t.Fatalf("full sync after reselecting OPS = %s", got)
	}
}

// A long full sync outlives its OAuth token; the retry, with a fresh one,
// goes on from the last checkpoint and still reports deletions.
func TestFullSyncResumesFromItsCheckpoint(t *testing.T) {
	f := newFakeJira(t)
	for i, id := range []string{"101", "102", "103", "104", "105"} {
		f.put(id, "ENG-"+id[2:], "issue "+id, "2026-09-2"+string(rune('0'+i))+"T10:00:00.000+0800")
	}
	f.put("201", "OPS-1", "ops", "2026-09-22T10:00:00.000+0800")
	r := newRun(t)
	inst := tokenInstance(f, nil)
	base := r.fetch(inst, pluginapi.FetchFull, nil, "ENG", "OPS")

	f.remove("105")
	f.searchesLeft = 2 // the first page of ENG, then the token expires
	checkpoint, err := r.fetchUntilFailure(inst, pluginapi.FetchFull, base.cursor, "ENG", "OPS")
	if pe, ok := pluginapi.AsError(err); !ok || pe.Code != pluginapi.CodeUnauthorized || checkpoint == nil {
		t.Fatalf("interrupted sync = %v, checkpoint %v", err, checkpoint)
	}

	retry := r.fetch(inst, pluginapi.FetchFull, checkpoint, "ENG", "OPS")
	if jql := f.jql[len(f.jql)-3]; !strings.Contains(jql, `project = "ENG" AND updated >= "2026/09/21 10:00"`) {
		t.Fatalf("the retry starts over: %s", jql)
	}
	got := strings.Join(summary(retry.items), "|")
	if got != "102 ENG-2 issue 102|103 ENG-3 issue 103|104 ENG-4 issue 104|201 OPS-1 ops|-105" {
		t.Fatalf("retry = %s", got)
	}
	st := stateFrom(retry.cursor)
	if st.Full != nil || !slices.Equal(st.Projects["ENG"].IDs, []string{"101", "102", "103", "104"}) ||
		!st.Projects["ENG"].Since.Equal(time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("cursor = %v", retry.cursor.State)
	}
}

// When Jira keeps the user's time zone to itself, a sync looks back far
// enough for any zone instead of guessing UTC.
func TestUnknownTimeZoneWidensTheWindow(t *testing.T) {
	since := time.Date(2026, 9, 22, 2, 0, 59, 0, time.UTC)
	if got := buildJQL("ENG", settings{}, since, nil); !strings.Contains(got, `updated >= "2026/09/21 14:00"`) {
		t.Fatal(got)
	}
	f := newFakeJira(t)
	f.tz = ""
	f.put("101", "ENG-1", "a", "2026-09-20T10:00:00.000+0800")
	r := newRun(t)
	inst := tokenInstance(f, nil)
	full := r.fetch(inst, pluginapi.FetchFull, nil, "ENG")
	r.fetch(inst, pluginapi.FetchIncremental, full.cursor, "ENG")
	if jql := f.lastJQL(); !strings.Contains(jql, `updated >= "2026/09/19 14:00"`) {
		t.Fatalf("incremental jql = %s", jql)
	}
}

// fakeHostKV is the Host API: data sources and a key-value store.
func fakeHostKV(t *testing.T, kv map[string]string) *pluginapi.HostAccess {
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == pluginapi.HostDataSourcesPath:
			writeJSON(w, pluginapi.DataSourceList{DataSources: []pluginapi.DataSourceInfo{
				{ID: "ds-eng", Connector: "jira", ResourceIDs: []string{"ENG"}},
			}})
		case r.Method == http.MethodPost:
			writeJSON(w, pluginapi.SyncStarted{Status: pluginapi.SyncQueued})
		case r.Method == http.MethodPut && r.URL.Path == pluginapi.HostKVPath:
			var in pluginapi.KVPut
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.TTLSeconds <= 0 {
				t.Errorf("note %s kept forever", in.Key)
			}
			kv[in.Key] = string(in.Value)
			writeJSON(w, pluginapi.KVEntry{Key: in.Key, Value: in.Value})
		case r.Method == http.MethodGet && r.URL.Path == pluginapi.HostKVListPath:
			out := pluginapi.KVList{Entries: []pluginapi.KVEntry{}}
			for k, v := range kv {
				if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
					out.Entries = append(out.Entries, pluginapi.KVEntry{Key: k, Value: json.RawMessage(v)})
				}
			}
			writeJSON(w, out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &pluginapi.HostAccess{URL: srv.URL, Token: "tok"}
}

// Jira's delete events leave a note; the incremental sync they start
// reports the issue once Jira confirms it is gone.
func TestDeletedIssuesReachIncrementalSyncs(t *testing.T) {
	f := newFakeJira(t)
	f.put("101", "ENG-1", "kept", "2026-09-20T10:00:00.000+0800")
	f.put("102", "ENG-2", "deleted", "2026-09-21T10:00:00.000+0800")
	r := newRun(t)
	inst := tokenInstance(f, nil)
	full := r.fetch(inst, pluginapi.FetchFull, nil, "ENG")

	kv := map[string]string{}
	r.host = fakeHostKV(t, kv)
	f.remove("102")
	var out pluginapi.WebhookResponse
	event := `{"webhookEvent":"jira:issue_deleted",` +
		`"issue":{"id":"102","key":"ENG-2","fields":{"project":{"key":"ENG"}}}}`
	err := r.client.Call(context.Background(), pluginapi.WebhookPath("issues"),
		pluginapi.Envelope{Context: pluginapi.Context{TenantID: 7, Host: r.host}},
		pluginapi.WebhookRequest{Method: http.MethodPost, Body: []byte(event)}, &out)
	if err != nil || out.Status != http.StatusAccepted || kv["deleted/102"] != `"ENG-2"` {
		t.Fatalf("webhook = %+v %v, kv %v", out, err, kv)
	}
	// Anyone may post to an unsigned webhook: a note about an issue Jira
	// still has deletes nothing.
	kv["deleted/101"] = `"ENG-1"`

	inc := r.fetch(inst, pluginapi.FetchIncremental, full.cursor, "ENG")
	if got := strings.Join(summary(inc.items), "|"); got != "102 ENG-2 deleted|-102" &&
		got != "-102" {
		t.Fatalf("incremental = %s", got)
	}
	if ids := stateFrom(inc.cursor).Projects["ENG"].IDs; !slices.Equal(ids, []string{"101"}) {
		t.Fatalf("ids = %v", ids)
	}
	again := r.fetch(inst, pluginapi.FetchIncremental, inc.cursor, "ENG")
	for _, it := range again.items {
		if it.IsDeleted {
			t.Fatalf("deletion reported twice: %v", summary(again.items))
		}
	}
}
