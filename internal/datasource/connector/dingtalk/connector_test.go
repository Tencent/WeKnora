package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAPI struct {
	workspaces  []workspace
	nodes       map[string][]node
	blocks      map[string][]json.RawMessage
	nodeErrors  map[string]error
	blockErrors map[string]error
	blockCalls  map[string]int

	// workspaceCalls counts workspace listings, so a test can prove a selection
	// that needs no workspace tree never asks for one.
	workspaceCalls int

	// getNodes holds nodes that are reachable by id but absent from every
	// parent listing — exactly the personal-space case — while getNodeErrors
	// and getNodeCalls make a direct read observable.
	getNodes      map[string]node
	getNodeErrors map[string]error
	getNodeCalls  map[string]int

	// Multi-dimensional tables (able). Tables are listed per Base, fields per
	// table and records one page at a time; notableRecordsFunc, when set,
	// synthesises a page from the requested token so pagination and the record
	// cap can be exercised without materialising thousands of rows.
	notableTables      map[string][]notableTable
	notableFields      map[string][]notableField
	notablePages       map[string]notableRecordPage
	notableTableErrors map[string]error
	notableFieldErrors map[string]error
	notableError       error
	notableTableCalls  map[string]int
	notableFieldCalls  map[string]int
	notableRecordCalls []string
	notableRecordsFunc func(baseID, tableID, nextToken string) (notableRecordPage, error)
}

// notableTableKey addresses one table's metadata in the fake.
func notableTableKey(baseID, tableID string) string {
	return baseID + "/" + tableID
}

// notablePageKey addresses one records/list page; the empty token is page one.
func notablePageKey(baseID, tableID, nextToken string) string {
	return notableTableKey(baseID, tableID) + "/" + nextToken
}

func (f *fakeAPI) listWorkspaces(context.Context) ([]workspace, error) {
	f.workspaceCalls++
	return f.workspaces, nil
}

func (f *fakeAPI) listNodes(_ context.Context, parentID string) ([]node, error) {
	if err := f.nodeErrors[parentID]; err != nil {
		return nil, err
	}
	return f.nodes[parentID], nil
}

// listNodesPage serves every child of parentID as a single page.
func (f *fakeAPI) listNodesPage(ctx context.Context, parentID, _ string) ([]node, string, error) {
	nodes, err := f.listNodes(ctx, parentID)
	return nodes, "", err
}

// getNode resolves a node by id: the explicit index first — that is how a
// personal-space node exists without appearing in any listing — then the parent
// listings, so every existing fixture keeps resolving.
func (f *fakeAPI) getNode(_ context.Context, nodeID string) (node, error) {
	if f.getNodeCalls == nil {
		f.getNodeCalls = make(map[string]int)
	}
	f.getNodeCalls[nodeID]++
	if err := f.getNodeErrors[nodeID]; err != nil {
		return node{}, err
	}
	if item, exists := f.getNodes[nodeID]; exists {
		return item, nil
	}
	for _, children := range f.nodes {
		if item, exists := childByID(children, nodeID); exists {
			return item, nil
		}
	}
	return node{}, fmt.Errorf("%w: DingTalk node %q is unavailable",
		datasource.ErrResourceNotFound, nodeID)
}

func (f *fakeAPI) listNotableTables(_ context.Context, baseID string) ([]notableTable, error) {
	if f.notableTableCalls == nil {
		f.notableTableCalls = make(map[string]int)
	}
	f.notableTableCalls[baseID]++
	if err := f.notableTableErrors[baseID]; err != nil {
		return nil, err
	}
	return f.notableTables[baseID], nil
}

func (f *fakeAPI) listNotableFields(
	_ context.Context,
	baseID, tableID string,
) ([]notableField, error) {
	if f.notableFieldCalls == nil {
		f.notableFieldCalls = make(map[string]int)
	}
	key := notableTableKey(baseID, tableID)
	f.notableFieldCalls[key]++
	if err := f.notableFieldErrors[key]; err != nil {
		return nil, err
	}
	return f.notableFields[key], nil
}

func (f *fakeAPI) listNotableRecords(
	_ context.Context,
	baseID, tableID, nextToken string,
) (notableRecordPage, error) {
	key := notableTableKey(baseID, tableID)
	f.notableRecordCalls = append(f.notableRecordCalls, key+"/"+nextToken)
	if f.notableRecordsFunc != nil {
		return f.notableRecordsFunc(baseID, tableID, nextToken)
	}
	if f.notableError != nil {
		return notableRecordPage{}, f.notableError
	}
	return f.notablePages[notablePageKey(baseID, tableID, nextToken)], nil
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
	// A document is a leaf: expanding it is answered with no children rather
	// than an error, so a picker expansion cannot turn a selection that syncs
	// fine into a failure.
	leafChildren, err := connector.ListResources(
		context.Background(), testConfig(), documents[0].ExternalID,
	)
	if err != nil || len(leafChildren) != 0 {
		t.Fatalf("expanding a document = %#v, %v; want no children", leafChildren, err)
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
	if len(items) != 1 || items[0].Metadata["error_reason_code"] != "dingtalk_resource_failed" || next == nil {
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
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) || len(items) != 1 ||
		!strings.Contains(items[0].Metadata["error"], "different workspace") || len(api.blockCalls) != 0 {
		t.Fatalf("FetchAll() = %#v, %v; want isolated workspace mismatch", items, err)
	}
}

// A node in a workspace that GET /v2.0/wiki/workspaces never returns — the
// operator's personal space — is still a valid explicit selection. The
// reference names the node, so the connector reads that node by id instead of
// requiring its workspace to be enumerated.
func TestFetchAllResolvesExplicitNodeOutsideTheWorkspaceListing(t *testing.T) {
	api := &fakeAPI{
		// Only team workspaces are listed; the personal space is not.
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"personal-doc": {
				ID: "personal-doc", WorkspaceID: "personal-space",
				Name: "Weekly.adoc", Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				ModifiedTimestamp: 1_767_508_041_000,
			},
		},
		blocks: map[string][]json.RawMessage{
			"personal-doc": {rawJSON(`{
				"blockType":"paragraph",
				"children":[{"elementType":"text","text":"Week 12"}]
			}`)},
		},
	}
	resourceID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "personal-space", NodeID: "personal-doc",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "personal-doc" ||
		items[0].SourceResourceID != resourceID ||
		string(items[0].Content) != "# Weekly.adoc\n\nWeek 12\n" {
		t.Fatalf("FetchAll() = %#v, want the personal-space document", items)
	}
	if api.getNodeCalls["personal-doc"] != 1 {
		t.Fatalf("node reads = %#v, want the node read by id", api.getNodeCalls)
	}
	if items[0].Metadata["workspace_id"] != "personal-space" {
		t.Fatalf("workspace metadata = %#v", items[0].Metadata)
	}
}

// The exception is limited to references that name a node. A whole-workspace
// selection still needs the listing, because that is the only way to learn the
// workspace root.
func TestFetchAllRejectsUnlistedWorkspaceWithoutAnExplicitNode(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
	}

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("personal-space"), []string{"personal-space"},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v; want one failed resource", items, err)
	}
	if items[0].Metadata["error_reason_code"] != "dingtalk_resource_failed" ||
		!strings.Contains(items[0].Metadata["error"], `workspace "personal-space" is unavailable`) {
		t.Fatalf("failure item = %#v", items[0])
	}
	if len(api.getNodeCalls) != 0 {
		t.Fatalf("an unnamed node was read: %#v", api.getNodeCalls)
	}
}

// Expanding an explicit folder outside the workspace listing works the same
// way: the folder is read by id and then its children are listed.
func TestListResourcesExpandsAnExplicitNodeOutsideTheWorkspaceListing(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"personal-folder": {
				ID: "personal-folder", WorkspaceID: "personal-space",
				Name: "我的文档", Type: "FOLDER", HasChildren: true,
			},
		},
		nodes: map[string][]node{
			"personal-folder": {{
				ID: "personal-doc", WorkspaceID: "personal-space", Name: "Plan.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			}},
		},
	}
	resourceID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "personal-space", NodeID: "personal-folder",
	})
	if err != nil {
		t.Fatal(err)
	}

	resources, err := testConnector(api).ListResources(context.Background(), testConfig(), resourceID)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].Type != "document" || resources[0].Name != "Plan.adoc" {
		t.Fatalf("ListResources() = %#v, want the folder's document", resources)
	}
	ref, err := decodeResourceReference(resources[0].ExternalID)
	if err != nil || ref.NodeID != "personal-doc" || ref.WorkspaceID != "personal-space" {
		t.Fatalf("child reference = %#v, %v", ref, err)
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

func TestNodeRevisionPrefersMillisecondTimestamp(t *testing.T) {
	n := node{ModifiedTime: "2023-05-15T11:29Z", ModifiedTimestamp: 1_684_148_940_123}
	if n.revision() != "1684148940123" {
		t.Fatalf("revision() = %q, want millisecond timestamp", n.revision())
	}
	if got := n.modifiedAt(); got.UnixMilli() != 1_684_148_940_123 {
		t.Fatalf("modifiedAt() = %s", got)
	}

	onlyTime := node{ModifiedTime: "2023-05-15T11:29Z"}
	if onlyTime.revision() != "2023-05-15T11:29Z" {
		t.Fatalf("revision() without timestamp = %q", onlyTime.revision())
	}
	if onlyTime.modifiedAt().IsZero() {
		t.Fatal("modifiedAt() rejected documented minute-precision time")
	}
}

func TestParseDingTalkTimeAcceptsMinutePrecision(t *testing.T) {
	parsed := parseDingTalkTime("2023-05-15T11:29Z")
	if parsed.IsZero() || parsed.UTC().Format("2006-01-02T15:04Z") != "2023-05-15T11:29Z" {
		t.Fatalf("parseDingTalkTime() = %s", parsed)
	}
}

func TestValidateProbesNodeAndDocumentAccess(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root"}},
		nodes: map[string][]node{
			"root": {syncDocument()},
		},
		blocks: map[string][]json.RawMessage{
			"doc": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"ok"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
	if err := testConnector(api).Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if api.blockCalls["doc"] != 1 {
		t.Fatalf("document probe calls = %#v", api.blockCalls)
	}

	api.blockErrors["doc"] = errors.New("missing Storage.File.Read")
	if err := testConnector(api).Validate(context.Background(), testConfig()); err == nil {
		t.Fatal("Validate() error = nil, want document access failure")
	}

	api = &fakeAPI{
		workspaces:  []workspace{{ID: "space", RootNodeID: "root"}},
		nodes:       map[string][]node{},
		nodeErrors:  map[string]error{"root": errors.New("missing Wiki.Node.Read")},
		blockErrors: make(map[string]error),
	}
	if err := testConnector(api).Validate(context.Background(), testConfig()); err == nil {
		t.Fatal("Validate() error = nil, want node access failure")
	}
}

// Expanding a document is answered with no children rather than an error: a
// leaf genuinely has none, and the picker renders an error as a failed
// expansion — a toast on a step whose selection syncs perfectly well. The
// listing already reports HasChildren=false for a document, which keeps the
// picker from offering the expander; this is the answer for a client that
// expands anyway, or for a selection saved by a build that did offer it.
func TestListResourcesReportsNoChildrenForALeafDocument(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		nodes: map[string][]node{
			"team-root": {{
				ID: "doc-1", WorkspaceID: "team", Name: "Plan.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			}},
		},
	}
	documentID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "team", NodeID: "doc-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	resources, err := testConnector(api).ListResources(context.Background(), testConfig(), documentID)
	if err != nil {
		t.Fatalf("ListResources(%q) error = %v, want an empty listing", documentID, err)
	}
	if len(resources) != 0 {
		t.Fatalf("ListResources(%q) = %#v, want no children", documentID, resources)
	}
}

// captureLogs redirects the process logger into a buffer for one test. The sync
// log is the surface a skipped node has to appear on, so it is asserted on
// directly rather than through an internal counter.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	logger.SetOutput(&buffer)
	t.Cleanup(logger.ConfigureFromEnv)
	return &buffer
}

// skippedNodesFixture mixes supported documents with the node types this
// connector deliberately cannot ingest, spread over a folder tree: reporting
// must survive nesting and must not disturb what is fetched.
func skippedNodesFixture() *fakeAPI {
	return &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root", Name: "Space"}},
		nodes: map[string][]node{
			"root": {
				{ID: "docs", WorkspaceID: "space", Name: "Docs", Type: "FOLDER"},
				{
					ID: "video-1", WorkspaceID: "space", Name: "Lesson.mp4", Type: "FILE",
					Category: "VIDEO", Extension: "mp4",
				},
				{
					ID: "table-1", WorkspaceID: "space", Name: "Roadmap.able", Type: "FILE",
					Category: "ALIDOC", Extension: "able",
				},
			},
			"docs": {
				{
					ID: "doc-1", WorkspaceID: "space", Name: "Runbook.adoc", Type: "FILE",
					Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r1",
				},
				{
					ID: "doc-2", WorkspaceID: "space", Name: "Policy.adoc", Type: "FILE",
					Category: "ALIDOC", Extension: "adoc", ModifiedTime: "r2",
				},
				{
					ID: "mind-1", WorkspaceID: "space", Name: "Plan.amind", Type: "FILE",
					Category: "ALIDOC", Extension: "amind",
				},
				{ID: "empty", WorkspaceID: "space", Name: "Empty", Type: "FOLDER"},
			},
			"empty": nil,
		},
		blocks: map[string][]json.RawMessage{
			"doc-1": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"run"}}`)},
			"doc-2": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"policy"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
}

// Seeing an unsupported node must not change what is fetched: the supported
// siblings still sync exactly as before, and nothing is requested for a skip.
func TestFetchAllKeepsSyncingSupportedSiblingsOfSkippedNodes(t *testing.T) {
	api := skippedNodesFixture()
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("FetchAll() returned %d items, want 2: %#v", len(items), items)
	}
	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if len(byID) != 2 || byID["doc-1"].ContentType != "text/markdown" ||
		byID["doc-2"].ContentType != "text/markdown" {
		t.Fatalf("supported documents = %#v", byID)
	}
	for _, skipped := range []string{"video-1", "table-1", "mind-1"} {
		if _, exists := byID[skipped]; exists {
			t.Fatalf("unsupported node %q was synced: %#v", skipped, byID[skipped])
		}
		if api.blockCalls[skipped] != 0 {
			t.Fatalf("unsupported node %q was requested: %#v", skipped, api.blockCalls)
		}
	}
	if len(api.blockCalls) != 2 || api.blockCalls["doc-1"] != 1 || api.blockCalls["doc-2"] != 1 {
		t.Fatalf("block calls = %#v, want one per supported document", api.blockCalls)
	}
}

// Every node the connector drops must reach the sync log with its identity and
// the concrete reason it cannot be ingested: media is never downloaded on
// purpose, while a native DingTalk type simply has no ingest path yet.
func TestSyncLogsEverySkippedNodeWithItsReason(t *testing.T) {
	api := skippedNodesFixture()
	logs := captureLogs(t)
	if _, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	); err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}

	output := logs.String()
	for _, want := range []string{
		`[DingTalk] skip node video-1 (name="Lesson.mp4" type=FILE category=VIDEO extension=mp4): ` +
			"video/media files are deliberately not downloaded by this connector",
		`[DingTalk] skip node table-1 (name="Roadmap.able" type=FILE category=ALIDOC extension=able): ` +
			"DingTalk multi-dimensional table has no ingest path in this connector yet",
		`[DingTalk] skip node mind-1 (name="Plan.amind" type=FILE category=ALIDOC extension=amind): ` +
			"DingTalk mind map has no ingest path in this connector yet",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("sync log is missing:\n%s\ngot:\n%s", want, output)
		}
	}
	for _, folder := range []string{"skip node docs", "skip node empty"} {
		if strings.Contains(output, folder) {
			t.Fatalf("folder reported as a skipped node: %q in\n%s", folder, output)
		}
	}
}

// The per-scope summary states how much of the scope arrived, in the same shape
// as the IMA connector's per-knowledge-base line.
func TestSyncLogsPerScopeSummary(t *testing.T) {
	api := skippedNodesFixture()
	logs := captureLogs(t)
	if _, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	); err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	want := "[DingTalk] scope space/root: total=5 synced=2 skipped=3 failed=0"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("sync log is missing %q:\n%s", want, logs.String())
	}
}

// A transient read failure is counted as failed — not as skipped — is logged as
// retryable, and still leaves the document out of the cursor.
func TestSyncSummaryCountsTransientFailuresAndStillRetries(t *testing.T) {
	api := skippedNodesFixture()
	api.blockErrors = map[string]error{"doc-2": errors.New("storage temporarily unavailable")}
	logs := captureLogs(t)

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchAll() error = %v, want PartialFetchError", err)
	}
	if len(items) != 2 {
		t.Fatalf("FetchAll() items = %#v, want 1 document and 1 failure", items)
	}
	output := logs.String()
	if !strings.Contains(output, "storage temporarily unavailable") ||
		!strings.Contains(output, "failed, will retry next sync") {
		t.Fatalf("transient failure was not logged as retryable:\n%s", output)
	}
	want := "[DingTalk] scope space/root: total=5 synced=1 skipped=3 failed=1"
	if !strings.Contains(output, want) {
		t.Fatalf("sync log is missing %q:\n%s", want, output)
	}
}

// Containers are not content: a tree of folders alone must produce no skip
// report at all, only a zeroed summary.
func TestSyncLogsNoSkipsForFolderOnlyTree(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root"}},
		nodes: map[string][]node{
			"root":  {{ID: "outer", WorkspaceID: "space", Name: "Outer", Type: "FOLDER"}},
			"outer": {{ID: "inner", WorkspaceID: "space", Name: "Inner", Type: "FOLDER"}},
			"inner": nil,
		},
	}
	logs := captureLogs(t)
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil || len(items) != 0 {
		t.Fatalf("FetchAll() = %#v, %v; want no items", items, err)
	}
	output := logs.String()
	if strings.Contains(output, "skip node") {
		t.Fatalf("folder-only tree reported skips:\n%s", output)
	}
	want := "[DingTalk] scope space/root: total=0 synced=0 skipped=0 failed=0"
	if !strings.Contains(output, want) {
		t.Fatalf("sync log is missing %q:\n%s", want, output)
	}
}

// scanScope hands the caller the skipped nodes with their DingTalk identity
// intact, and a single-document scope has no tree to skip anything from.
func TestScanScopeReportsSkippedNodesAndSingleDocumentScopeHasNone(t *testing.T) {
	api := skippedNodesFixture()
	documents, skipped, err := scanScope(context.Background(), api, syncScope{
		ResourceID:  "resource",
		Reference:   resourceReference{WorkspaceID: "space"},
		StartNodeID: "root",
	})
	if err != nil {
		t.Fatalf("scanScope() error = %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("scanScope() documents = %#v, want 2", documents)
	}
	if len(skipped) != 3 {
		t.Fatalf("scanScope() skipped = %#v, want 3", skipped)
	}
	got := make(map[string]node, len(skipped))
	for _, item := range skipped {
		got[item.ID] = item
	}
	if got["video-1"].Category != "VIDEO" || got["video-1"].Extension != "mp4" ||
		got["video-1"].Name != "Lesson.mp4" ||
		got["table-1"].Category != "ALIDOC" || got["table-1"].Extension != "able" ||
		got["mind-1"].Extension != "amind" {
		t.Fatalf("skipped nodes lost their identity: %#v", got)
	}

	document := syncDocument()
	documents, skipped, err = scanScope(context.Background(), api, syncScope{
		ResourceID: "resource",
		Reference:  resourceReference{WorkspaceID: "space"},
		Document:   &document,
	})
	if err != nil || len(documents) != 1 || documents[0].ID != "doc" || len(skipped) != 0 {
		t.Fatalf("single-document scanScope() = %#v, %#v, %v", documents, skipped, err)
	}
}

// The reason has to say which kind of unsupported a node is: a video is skipped
// on purpose, a native type has simply not been implemented.
func TestSkipReasonDistinguishesMediaFromUnimplementedTypes(t *testing.T) {
	for _, testCase := range []struct {
		label string
		node  node
		want  string
	}{
		{
			"video category",
			node{Type: "FILE", Category: "VIDEO", Extension: "mp4"},
			"video/media files are deliberately not downloaded by this connector",
		},
		{
			"video extension without a category",
			node{Type: "FILE", Category: "OTHER", Extension: "MOV"},
			"video/media files are deliberately not downloaded by this connector",
		},
		{
			"audio extension",
			node{Type: "FILE", Category: "OTHER", Extension: "mp3"},
			"video/media files are deliberately not downloaded by this connector",
		},
		{
			// Only the types with a name worth printing are in the map; every
			// other unsupported node gets the generic reason. Kept as a
			// regression guard on the map.
			"type without a dedicated label",
			node{Type: "FILE", Category: "ALIDOC", Extension: "axls"},
			"no ingest path for this DingTalk node type in this connector yet",
		},
		{
			"multidimensional table",
			node{Type: "FILE", Category: "ALIDOC", Extension: "able"},
			"DingTalk multi-dimensional table has no ingest path in this connector yet",
		},
		{
			"mind map",
			node{Type: "FILE", Category: "ALIDOC", Extension: "amind"},
			"DingTalk mind map has no ingest path in this connector yet",
		},
		{
			"unknown type",
			node{Type: "FILE", Category: "OTHER", Extension: "bin"},
			"no ingest path for this DingTalk node type in this connector yet",
		},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			if got := skipReason(testCase.node); got != testCase.want {
				t.Fatalf("skipReason() = %q, want %q", got, testCase.want)
			}
		})
	}
}
