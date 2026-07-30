package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAPI struct {
	workspaces  []workspace
	nodes       map[string][]node
	blocks      map[string][]json.RawMessage
	nodeErrors  map[string]error
	blockErrors map[string]error
	blockCalls  map[string]int
}

func (f *fakeAPI) listWorkspaces(context.Context) ([]workspace, error) {
	return f.workspaces, nil
}

func (f *fakeAPI) listNodes(_ context.Context, parentID string) ([]node, error) {
	if err := f.nodeErrors[parentID]; err != nil {
		return nil, err
	}
	return f.nodes[parentID], nil
}

func (f *fakeAPI) documentBlocks(_ context.Context, documentID string) ([]json.RawMessage, error) {
	if f.blockCalls == nil {
		f.blockCalls = make(map[string]int)
	}
	f.blockCalls[documentID]++
	if err := f.blockErrors[documentID]; err != nil {
		return nil, err
	}
	return f.blocks[documentID], nil
}

func testConnector(api dingTalkAPI) *Connector {
	return &Connector{newAPI: func(*config) dingTalkAPI { return api }}
}

func testConfig(resources ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id":     "ding-app",
			"client_secret": "secret",
			"operator_id":   "union-id",
		},
		ResourceIDs: resources,
	}
}

func rawJSON(value string) json.RawMessage {
	return json.RawMessage(value)
}

func TestConnectorListsWorkspacesAndFetchesNestedDocuments(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "b", RootNodeID: "root-b", Name: "Beta"},
			{ID: "a", RootNodeID: "root-a", Name: "Alpha", Description: "Team docs"},
		},
		nodes: map[string][]node{
			"root-a": {
				{ID: "folder", Type: "FOLDER"},
				{ID: "ignored", Type: "FILE", Category: "FILE", Extension: "pdf"},
			},
			"folder": {
				{
					ID: "doc-1", Type: "FILE", Category: "ALIDOC", Extension: "adoc",
					Name: "Roadmap", WorkspaceID: "a", ModifiedTime: "2026-07-25T08:00:00Z",
				},
			},
		},
		blocks: map[string][]json.RawMessage{
			"doc-1": {rawJSON(`{
				"blockType":"paragraph",
				"children":[{"elementType":"text","text":"Q3 goals","bold":true}]
			}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
	connector := testConnector(api)

	resources, err := connector.ListResources(context.Background(), testConfig(), "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 2 || resources[0].ExternalID != "a" || resources[1].ExternalID != "b" {
		t.Fatalf("ListResources() = %#v, want workspaces sorted by name", resources)
	}
	if !resources[0].HasChildren {
		t.Fatalf("workspace resource = %#v, want expandable", resources[0])
	}
	children, err := connector.ListResources(context.Background(), testConfig(), "a")
	if err != nil || len(children) != 1 || children[0].Type != "folder" {
		t.Fatalf("workspace children = %#v, %v; want one folder", children, err)
	}
	folderID := children[0].ExternalID
	documents, err := connector.ListResources(context.Background(), testConfig(), folderID)
	if err != nil || len(documents) != 1 || documents[0].Type != "document" {
		t.Fatalf("folder children = %#v, %v; want one document", documents, err)
	}
	if _, err := connector.ListResources(
		context.Background(), testConfig(), documents[0].ExternalID,
	); !errors.Is(err, datasource.ErrResourceNotFound) {
		t.Fatalf("expanding a document error = %v, want ErrResourceNotFound", err)
	}
	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(), testConfig(), []string{documents[0].ExternalID},
	)
	if err != nil || len(ancestors) != 2 || ancestors[0] != "a" || ancestors[1] != folderID {
		t.Fatalf("ResolveResourceAncestors() = %#v, %v", ancestors, err)
	}

	items, err := connector.FetchAll(context.Background(), testConfig("a"), []string{"a"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll() returned %d items, want 1", len(items))
	}
	item := items[0]
	if item.ExternalID != "doc-1" || item.Title != "Roadmap" ||
		string(item.Content) != "# Roadmap\n\n**Q3 goals**\n" {
		t.Fatalf("FetchAll() item = %#v", item)
	}
	if item.ContentType != "text/markdown" || item.SourceResourceID != "a" ||
		item.Metadata["channel"] != types.ChannelDingtalk {
		t.Fatalf("FetchAll() metadata = %#v", item)
	}
}

func TestIncrementalSyncRetriesFailuresAndReportsDeletions(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root", Name: "Space"}},
		nodes: map[string][]node{
			"root": {
				{ID: "unchanged", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r1"},
				{ID: "changed", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r2"},
				{ID: "broken", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r2"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"changed": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"updated"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: map[string]error{"broken": errors.New("permission denied")},
	}
	connector := testConnector(api)
	cursorMap, err := encodeCursor(&cursorState{
		Version: cursorVersion,
		Resources: map[string]map[string]string{
			"space": {
				"unchanged": "r1",
				"changed":   "r1",
				"broken":    "r1",
				"deleted":   "r1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	items, next, syncErr := connector.FetchIncremental(
		context.Background(),
		testConfig("space"),
		&types.SyncCursor{ConnectorCursor: cursorMap},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(syncErr, &partial) {
		t.Fatalf("FetchIncremental() error = %v, want PartialFetchError", syncErr)
	}
	if next == nil {
		t.Fatal("FetchIncremental() returned nil cursor")
	}
	if api.blockCalls["unchanged"] != 0 || api.blockCalls["changed"] != 1 ||
		api.blockCalls["broken"] != 1 {
		t.Fatalf("document block calls = %#v", api.blockCalls)
	}

	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if len(byID) != 3 || !byID["deleted"].IsDeleted {
		t.Fatalf("FetchIncremental() items = %#v", items)
	}
	if byID["broken"].Metadata["error"] == "" {
		t.Fatalf("failed item metadata = %#v", byID["broken"].Metadata)
	}

	decoded, err := decodeCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	revisions := decoded.Resources["space"]
	if revisions["unchanged"] != "r1" || revisions["changed"] != "r2" ||
		revisions["broken"] != "r1" {
		t.Fatalf("next cursor revisions = %#v", revisions)
	}
	if _, exists := revisions["deleted"]; exists {
		t.Fatalf("deleted document remained in cursor: %#v", revisions)
	}
}

func TestIncrementalSyncDoesNotInferDeletionsFromIncompleteTree(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root"}},
		nodes: map[string][]node{
			"root": {{ID: "folder", Type: "FOLDER"}},
		},
		nodeErrors:  map[string]error{"folder": errors.New("temporary failure")},
		blockErrors: make(map[string]error),
	}
	cursorMap, err := encodeCursor(&cursorState{
		Version: cursorVersion,
		Resources: map[string]map[string]string{
			"space": {"existing": "r1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	items, next, err := testConnector(api).FetchIncremental(
		context.Background(),
		testConfig("space"),
		&types.SyncCursor{ConnectorCursor: cursorMap},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchIncremental() error = %v, want PartialFetchError", err)
	}
	if len(items) != 0 || next == nil {
		t.Fatalf("FetchIncremental() = %#v, %#v; want preserved cursor", items, next)
	}
	decoded, decodeErr := decodeCursor(next)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if decoded.Resources["space"]["existing"] != "r1" {
		t.Fatalf("preserved cursor = %#v", decoded.Resources)
	}
}

func TestConnectorSupportsFolderAndDocumentScopesWithoutDuplicates(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root"}},
		nodes: map[string][]node{
			"root": {
				{ID: "folder", WorkspaceID: "space", Type: "FOLDER", Name: "Folder"},
				{
					ID: "standalone", WorkspaceID: "space", Type: "FILE",
					Category: "ALIDOC", Extension: "adoc", Name: "Standalone",
				},
			},
			"folder": {
				{
					ID: "nested", WorkspaceID: "space", Type: "FILE",
					Category: "ALIDOC", Extension: "adoc", Name: "Nested",
				},
			},
		},
		blocks: map[string][]json.RawMessage{
			"standalone": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"one"}}`)},
			"nested":     {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"two"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
	connector := testConnector(api)
	folderID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "space", NodeID: "folder",
	})
	if err != nil {
		t.Fatal(err)
	}
	nestedID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "space", NodeID: "nested", Ancestors: []string{"folder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	standaloneID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "space", NodeID: "standalone",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := connector.FetchAll(
		context.Background(),
		testConfig(folderID, nestedID, standaloneID),
		[]string{folderID, nestedID, standaloneID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || api.blockCalls["nested"] != 1 || api.blockCalls["standalone"] != 1 {
		t.Fatalf("items = %#v, block calls = %#v", items, api.blockCalls)
	}
	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if byID["nested"].SourceResourceID != folderID ||
		byID["standalone"].SourceResourceID != standaloneID {
		t.Fatalf("source resource IDs = %#v", byID)
	}
}

func TestConnectorRejectsCrossWorkspaceResourcePath(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "space-a", RootNodeID: "root-a"},
			{ID: "space-b", RootNodeID: "root-b"},
		},
		nodes: map[string][]node{
			"root-a": {{
				ID: "foreign", WorkspaceID: "space-b", Type: "FILE",
				Category: "ALIDOC", Extension: "adoc",
			}},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
	resourceID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "space-a", NodeID: "foreign",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("FetchAll() error = %v", err)
	}
}

func TestDecodeCursorMigratesWorkspaceCursorV1(t *testing.T) {
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"version": 1,
		"workspaces": map[string]interface{}{
			"legacy-space": map[string]interface{}{"document": "revision"},
		},
	}}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version != cursorVersion ||
		decoded.Resources["legacy-space"]["document"] != "revision" {
		t.Fatalf("decoded cursor = %#v", decoded)
	}
}

func TestParseConfigRejectsMissingCredentials(t *testing.T) {
	for _, credentials := range []map[string]interface{}{
		nil,
		{"client_id": "app"},
		{"client_id": "app", "client_secret": "secret"},
		{"client_id": 42, "client_secret": "secret", "operator_id": "operator"},
	} {
		_, err := parseConfig(&types.DataSourceConfig{Credentials: credentials})
		if !errors.Is(err, datasource.ErrInvalidCredentials) {
			t.Fatalf("parseConfig(%#v) error = %v", credentials, err)
		}
	}
}
