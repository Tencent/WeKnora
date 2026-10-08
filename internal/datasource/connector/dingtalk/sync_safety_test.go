package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func twoFolderFixture(t *testing.T) (*Connector, *fakeAPI, *types.DataSourceConfig, string, string) {
	t.Helper()
	a, err := encodeResourceReference(resourceReference{WorkspaceID: "space", NodeID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeResourceReference(resourceReference{WorkspaceID: "space", NodeID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root"}},
		nodes:      map[string][]node{"root": {{ID: "a", Type: "FOLDER"}, {ID: "b", Type: "FOLDER"}}},
		blocks: map[string][]json.RawMessage{
			"doc": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"content"}}`)},
		},
		nodeErrors: make(map[string]error), blockErrors: make(map[string]error),
	}
	return testConnector(api), api, testConfig(a, b), a, b
}

func syncDocument() node {
	return node{
		ID: "doc", WorkspaceID: "space", Name: "Doc", Type: "FILE",
		Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r1",
	}
}

func assertNoDeletion(t *testing.T, items []types.FetchedItem) {
	t.Helper()
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("unexpected deletion: %#v", item)
		}
	}
}

func TestIncrementalMoveBetweenSelectedFolders(t *testing.T) {
	for _, from := range []string{"a", "b"} {
		t.Run(from, func(t *testing.T) {
			c, api, cfg, a, b := twoFolderFixture(t)
			to, fromID, toID := "b", a, b
			if from == "b" {
				to, fromID, toID = "a", b, a
			}
			api.nodes[from] = []node{syncDocument()}
			_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			api.nodes[from] = nil
			api.nodes[to] = []node{syncDocument()}
			items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
			if err != nil {
				t.Fatal(err)
			}
			assertNoDeletion(t, items)
			if len(items) != 1 || len(items[0].Content) == 0 {
				t.Fatalf("move result: %#v", items)
			}
			state, err := decodeCursor(next)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := state.Resources[fromID]["doc"]; ok {
				t.Fatal("old scope retained moved document")
			}
			if state.Resources[toID]["doc"] != "r1" {
				t.Fatalf("destination cursor: %#v", state)
			}
			items, _, err = c.FetchIncremental(context.Background(), cfg, next)
			if err != nil || len(items) != 0 {
				t.Fatalf("unchanged after move: %#v, %v", items, err)
			}
		})
	}
}

func TestIncrementalMoveWithContentFailureRetries(t *testing.T) {
	c, api, cfg, _, b := twoFolderFixture(t)
	api.nodes["a"] = []node{syncDocument()}
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.nodes["a"] = nil
	api.nodes["b"] = []node{syncDocument()}
	api.blockErrors["doc"] = errors.New("permission denied")
	items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("want partial, got %v", err)
	}
	assertNoDeletion(t, items)
	if len(items) != 1 || items[0].Metadata["error_reason_code"] != "dingtalk_document_failed" {
		t.Fatalf("failure result: %#v", items)
	}
	state, _ := decodeCursor(next)
	if _, ok := state.Resources[b]["doc"]; ok {
		t.Fatal("failed destination advanced cursor")
	}
	delete(api.blockErrors, "doc")
	items, _, err = c.FetchIncremental(context.Background(), cfg, next)
	if err != nil || len(items) != 1 || len(items[0].Content) == 0 {
		t.Fatalf("retry result: %#v, %v", items, err)
	}
}

func TestIncrementalDefersDeletionUntilEveryScopeRecovers(t *testing.T) {
	for _, resolveFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "scan", true: "resolve"}[resolveFailure], func(t *testing.T) {
			c, api, cfg, a, _ := twoFolderFixture(t)
			api.nodes["a"] = []node{syncDocument()}
			_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			api.nodes["a"] = nil
			if resolveFailure {
				api.nodes["root"] = api.nodes["root"][:1]
			} else {
				api.nodeErrors["b"] = errors.New("temporary failure")
			}
			items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
			var partial *datasource.PartialFetchError
			if !errors.As(err, &partial) {
				t.Fatalf("want partial, got %v", err)
			}
			assertNoDeletion(t, items)
			state, _ := decodeCursor(next)
			if state.Resources[a]["doc"] != "r1" {
				t.Fatal("deferred deletion lost revision")
			}
			api.nodes["root"] = []node{{ID: "a", Type: "FOLDER"}, {ID: "b", Type: "FOLDER"}}
			delete(api.nodeErrors, "b")
			items, next, err = c.FetchIncremental(context.Background(), cfg, next)
			if err != nil || len(items) != 1 || !items[0].IsDeleted {
				t.Fatalf("recovered deletion: %#v, %v", items, err)
			}
			items, _, err = c.FetchIncremental(context.Background(), cfg, next)
			if err != nil || len(items) != 0 {
				t.Fatalf("duplicate deletion: %#v, %v", items, err)
			}
		})
	}
}

func TestUnavailableSelectionPreservesCursorAndHealthyWork(t *testing.T) {
	c, api, cfg, a, b := twoFolderFixture(t)
	api.nodes["a"] = []node{syncDocument()}
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The saved folder no longer resolves, but the other selected folder works.
	api.nodes["root"] = api.nodes["root"][1:]
	healthy := syncDocument()
	healthy.ID = "healthy"
	api.nodes["b"] = []node{healthy}
	api.blocks["healthy"] = api.blocks["doc"]
	items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) || len(items) != 2 {
		t.Fatalf("result: %#v, %v", items, err)
	}
	if len(partial.Details) != 0 {
		t.Fatal("raw diagnostics duplicated localized item errors")
	}
	assertNoDeletion(t, items)
	if items[0].Metadata["error_reason_code"] != "dingtalk_resource_failed" ||
		items[1].ExternalID != "healthy" || len(items[1].Content) == 0 {
		t.Fatalf("items: %#v", items)
	}
	state, _ := decodeCursor(next)
	if state.Resources[a]["doc"] != "r1" || state.Resources[b]["healthy"] != "r1" {
		t.Fatalf("cursor: %#v", state)
	}
	// Recovery must retry the old scope instead of dropping its previous state.
	api.nodes["root"] = []node{{ID: "a", Type: "FOLDER"}, {ID: "b", Type: "FOLDER"}}
	changed := syncDocument()
	changed.ModifiedTime = "r2"
	api.nodes["a"] = []node{changed}
	items, _, err = c.FetchIncremental(context.Background(), cfg, next)
	if err != nil || len(items) != 1 || items[0].ExternalID != "doc" {
		t.Fatalf("recovery: %#v, %v", items, err)
	}
	// Full sync isolates the same missing selection too.
	api.nodes["root"] = api.nodes["root"][1:]
	items, err = c.FetchAll(context.Background(), cfg, cfg.ResourceIDs)
	if !errors.As(err, &partial) || len(items) != 2 || items[1].ExternalID != "healthy" {
		t.Fatalf("full sync: %#v, %v", items, err)
	}
}

func TestScopeResolutionCancellationDoesNotProduceCursor(t *testing.T) {
	c, api, cfg, _, _ := twoFolderFixture(t)
	api.nodeErrors["root"] = context.Canceled
	items, next, err := c.FetchIncremental(context.Background(), cfg, nil)
	if !errors.Is(err, context.Canceled) || next != nil || len(items) != 0 {
		t.Fatalf("canceled: %#v, %#v, %v", items, next, err)
	}
}

func TestFetchAllFromCursorReFetchesAndReconcilesDeletions(t *testing.T) {
	c, api, cfg, _, _ := twoFolderFixture(t)
	api.nodes["a"] = []node{syncDocument()}
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	items, next, err := c.FetchAllFromCursor(context.Background(), cfg, cfg.ResourceIDs, cursor)
	if err != nil || next == nil || len(items) != 1 || len(items[0].Content) == 0 {
		t.Fatalf("full re-fetch: %#v, %#v, %v", items, next, err)
	}

	api.nodes["a"] = nil
	items, err = c.FetchAll(context.Background(), cfg, cfg.ResourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	assertNoDeletion(t, items)

	items, _, err = c.FetchAllFromCursor(context.Background(), cfg, cfg.ResourceIDs, next)
	if err != nil || len(items) != 1 || !items[0].IsDeleted {
		t.Fatalf("full sync deletion: %#v, %v", items, err)
	}
}
