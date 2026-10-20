package lingo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeLingoAPI struct {
	repos       []Repository
	reposErr    error
	entities    map[string][]Entity
	entitiesErr map[string]error
}

func (f *fakeLingoAPI) ListRepos(context.Context) ([]Repository, error) {
	return f.repos, f.reposErr
}

func (f *fakeLingoAPI) ListEntities(_ context.Context, repoID string, _ int) ([]Entity, error) {
	if err := f.entitiesErr[repoID]; err != nil {
		return nil, err
	}
	return f.entities[repoID], nil
}

func newTestConnector(api lingoAPI) *Connector {
	connector := NewConnector(core.RegionFeishuLingo)
	connector.newAPI = func(*core.Config) lingoAPI { return api }
	return connector
}

func testConfig(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeFeishuLingo,
		Credentials: map[string]interface{}{
			"app_id":     "cli_test",
			"app_secret": "secret",
		},
		ResourceIDs: resourceIDs,
	}
}

func entity(id, title, definition, updated string) Entity {
	return Entity{
		ID:          id,
		MainKeys:    []MainKey{{Key: title}},
		Description: definition,
		UpdateTime:  updated,
	}
}

func TestListResources(t *testing.T) {
	api := &fakeLingoAPI{repos: []Repository{{ID: "repo-2", Name: "B"}, {ID: "repo-1", Name: "A"}}}
	resources, err := newTestConnector(api).ListResources(context.Background(), testConfig(), "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 2 || resources[0].ExternalID != "repo-1" || resources[1].ExternalID != "repo-2" {
		t.Fatalf("resources = %#v", resources)
	}

	children, err := newTestConnector(api).ListResources(context.Background(), testConfig(), "repo-1")
	if err != nil || len(children) != 0 {
		t.Fatalf("children = %#v, err = %v", children, err)
	}
}

func TestListResourcesFallsBackToDefault(t *testing.T) {
	for name, api := range map[string]*fakeLingoAPI{
		"empty": {repos: nil},
		"error": {reposErr: errors.New("permission denied")},
	} {
		t.Run(name, func(t *testing.T) {
			resources, err := newTestConnector(api).ListResources(context.Background(), testConfig(), "")
			if err != nil {
				t.Fatalf("ListResources() error = %v", err)
			}
			if len(resources) != 1 || resources[0].ExternalID != defaultRepoID {
				t.Fatalf("resources = %#v", resources)
			}
		})
	}
}

func TestFetchAllDeduplicatesAndPlacesFolder(t *testing.T) {
	api := &fakeLingoAPI{entities: map[string][]Entity{
		"repo-1": {
			entity("e1", "OT", "Overtime", "100"),
			entity("e2", "AS", "As soon as possible", "200"),
		},
		"repo-2": {
			entity("e1", "OT", "duplicate in another repo", "999"),
		},
	}}
	connector := newTestConnector(api)
	items, err := connector.FetchAll(context.Background(), testConfig("repo-1", "repo-2"), nil)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	for _, item := range items {
		if !strings.HasPrefix(item.FileName, "飞书词典/") {
			t.Fatalf("FileName = %q", item.FileName)
		}
	}
	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	ot, ok := byID["e1"]
	if !ok || ot.SourceResourceID != "repo-1" || ot.Title != "OT" {
		t.Fatalf("e1 = %#v", ot)
	}
	if !strings.Contains(string(ot.Content), "Overtime") {
		t.Fatalf("first copy of a duplicated entity must win: %s", ot.Content)
	}
}

// When the data source has no explicit resource selection, every repository the
// app can see must be synced. This is the multi-repository case: the app is
// added to each 词库设置 page in Feishu Lingo, and /lingo/v1/repos returns them.
func TestFetchAllWithoutSelectionSyncsEveryVisibleRepo(t *testing.T) {
	api := &fakeLingoAPI{
		repos: []Repository{{ID: "repo-1", Name: "A"}, {ID: "repo-2", Name: "B"}},
		entities: map[string][]Entity{
			"repo-1": {entity("e1", "OT", "one", "100")},
			"repo-2": {entity("e2", "AS", "two", "200")},
		},
	}
	items, err := newTestConnector(api).FetchAll(context.Background(), testConfig(), nil)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	got := map[string]string{}
	for _, item := range items {
		got[item.ExternalID] = item.SourceResourceID
	}
	if got["e1"] != "repo-1" || got["e2"] != "repo-2" {
		t.Fatalf("entities were not read from every repo: %#v", got)
	}
}

// If the repo listing is unavailable (for example the app was never added to
// the Lingo console), fall back to the organisation-wide dictionary so the data
// source stays usable instead of failing validation and every sync.
func TestFetchAllFallsBackToDefaultDictionary(t *testing.T) {
	api := &fakeLingoAPI{
		reposErr: errors.New("permission denied"),
		entities: map[string][]Entity{
			defaultRepoID: {entity("e1", "OT", "one", "100")},
		},
	}
	items, err := newTestConnector(api).FetchAll(context.Background(), testConfig(), nil)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 || items[0].SourceResourceID != defaultRepoID {
		t.Fatalf("items = %#v", items)
	}
}

func TestFetchIncrementalEmitsChangesAndDeletions(t *testing.T) {
	api := &fakeLingoAPI{entities: map[string][]Entity{
		"repo-1": {
			entity("e1", "OT", "updated", "101"),
			entity("e2", "AS", "unchanged", "200"),
		},
	}}
	previous := &lingoCursor{
		LastSyncTime: time.Now().UTC().Add(-time.Hour),
		EntityTimes:  map[string]string{"e1": "100", "e2": "200", "e3": "300"},
		EntityRepos:  map[string]string{"e1": "repo-1", "e2": "repo-1", "e3": "repo-1"},
	}
	connector := newTestConnector(api)
	items, next, err := connector.FetchIncremental(context.Background(), testConfig("repo-1"), previous.encode())
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (one update, one deletion)", len(items))
	}
	seen := map[string]types.FetchedItem{}
	for _, item := range items {
		seen[item.ExternalID] = item
	}
	if _, ok := seen["e2"]; ok {
		t.Fatal("unchanged entity must not be emitted")
	}
	if updated, ok := seen["e1"]; !ok || updated.IsDeleted {
		t.Fatalf("e1 = %#v", updated)
	}
	if deleted, ok := seen["e3"]; !ok || !deleted.IsDeleted || deleted.SourceResourceID != "repo-1" {
		t.Fatalf("e3 = %#v", deleted)
	}
	if next == nil {
		t.Fatal("next cursor is nil")
	}
	decoded := decodeLingoCursor(next)
	if decoded == nil || decoded.EntityTimes["e1"] != "101" || decoded.EntityTimes["e2"] != "200" {
		t.Fatalf("cursor = %#v", decoded)
	}
	if _, ok := decoded.EntityTimes["e3"]; ok {
		t.Fatal("deleted entity must leave the cursor")
	}
}

func TestFetchIncrementalDoesNotDeleteOutsideSelectedRepos(t *testing.T) {
	api := &fakeLingoAPI{entities: map[string][]Entity{
		"repo-1": {entity("e1", "OT", "updated", "101")},
	}}
	previous := &lingoCursor{
		EntityTimes: map[string]string{"e1": "100", "other": "500"},
		EntityRepos: map[string]string{"e1": "repo-1", "other": "repo-2"},
	}
	items, _, err := newTestConnector(api).FetchIncremental(
		context.Background(), testConfig("repo-1"), previous.encode())
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("unexpected deletion outside selected repo: %#v", item)
		}
	}
}

func TestFetchIncrementalKeepsCursorOnError(t *testing.T) {
	api := &fakeLingoAPI{entitiesErr: map[string]error{"repo-1": errors.New("boom")}}
	items, next, err := newTestConnector(api).FetchIncremental(
		context.Background(), testConfig("repo-1"), nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if items != nil || next != nil {
		t.Fatalf("items = %#v, next = %#v", items, next)
	}
}

func TestFetchSkipsUnsupportedEntityButAdvancesCursor(t *testing.T) {
	api := &fakeLingoAPI{entities: map[string][]Entity{
		"repo-1": {
			entity("e1", "OT", "valid", "100"),
			{ID: "e2", MainKeys: []MainKey{{Key: "IMG"}}, RichText: `<img src="x"/>`, UpdateTime: "200"},
		},
	}}
	items, next, err := newTestConnector(api).fetch(
		context.Background(), testConfig("repo-1"), []string{"repo-1"}, nil)
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "e1" {
		t.Fatalf("items = %#v", items)
	}
	if next.EntityTimes["e2"] != "200" {
		t.Fatalf("cursor must record skipped entity to avoid retry loops: %#v", next.EntityTimes)
	}
}

func TestResolveResourceAncestors(t *testing.T) {
	got, err := newTestConnector(&fakeLingoAPI{}).ResolveResourceAncestors(
		context.Background(), testConfig(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %#v", got)
	}
}

func TestExternalIDFromDocument(t *testing.T) {
	connector := NewConnector(core.RegionFeishuLingo)
	config := testConfig()
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "colon with space", content: "# OT\n\n飞书词条 ID：enterprise_123", want: "enterprise_123"},
		{name: "no space", content: "飞书词条ID: enterprise_456\n", want: "enterprise_456"},
		{name: "absent", content: "# OT\n\nno traceability line", want: ""},
		{name: "empty id", content: "飞书词条 ID：\n", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connector.ExternalIDFromDocument(context.Background(), config, "OT", []byte(tc.content))
			if got != tc.want {
				t.Fatalf("ExternalIDFromDocument() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateRejectsBadCredentials(t *testing.T) {
	config := &types.DataSourceConfig{Type: types.ConnectorTypeFeishuLingo}
	err := newTestConnector(&fakeLingoAPI{}).Validate(context.Background(), config)
	if err == nil {
		t.Fatal("expected credential error")
	}
}
