package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// Native DingTalk spreadsheets (axls). Sheets are listed per workbook,
	// their extents are read per sheet, and cells are served per ranges window
	// so a test can assert the exact windows the connector asked for.
	sheets           map[string][]sheet
	sheetInfos       map[string]sheet
	sheetValues      map[string][][]string
	sheetListErrors  map[string]error
	sheetInfoErrors  map[string]error
	sheetRangeErrors map[string]error
	sheetListCalls   map[string]int
	sheetRangeCalls  []string
	// sheetRangeFunc, when set, synthesises a window's cells instead of reading
	// sheetValues: chunking and cap tests need rows that depend on the range.
	sheetRangeFunc func(workbookID, sheetID, ranges string) ([][]string, error)
}

// sheetKey addresses one sheet's metadata in the fake.
func sheetKey(workbookID, sheetID string) string {
	return workbookID + "/" + sheetID
}

// sheetRangeKey addresses one ranges request in the fake.
func sheetRangeKey(workbookID, sheetID, ranges string) string {
	return sheetKey(workbookID, sheetID) + "/" + ranges
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

// listNodesPage serves every child of parentID as a single page.
func (f *fakeAPI) listNodesPage(ctx context.Context, parentID, _ string) ([]node, string, error) {
	nodes, err := f.listNodes(ctx, parentID)
	return nodes, "", err
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

func (f *fakeAPI) listSheets(_ context.Context, workbookID string) ([]sheet, error) {
	if f.sheetListCalls == nil {
		f.sheetListCalls = make(map[string]int)
	}
	f.sheetListCalls[workbookID]++
	if err := f.sheetListErrors[workbookID]; err != nil {
		return nil, err
	}
	return f.sheets[workbookID], nil
}

func (f *fakeAPI) sheetInfo(_ context.Context, workbookID, sheetID string) (sheet, error) {
	if err := f.sheetInfoErrors[sheetKey(workbookID, sheetID)]; err != nil {
		return sheet{}, err
	}
	return f.sheetInfos[sheetKey(workbookID, sheetID)], nil
}

func (f *fakeAPI) sheetRange(
	_ context.Context,
	workbookID, sheetID, ranges string,
) ([][]string, error) {
	key := sheetRangeKey(workbookID, sheetID, ranges)
	f.sheetRangeCalls = append(f.sheetRangeCalls, key)
	if f.sheetRangeFunc != nil {
		return f.sheetRangeFunc(workbookID, sheetID, ranges)
	}
	if err := f.sheetRangeErrors[key]; err != nil {
		return nil, err
	}
	return f.sheetValues[key], nil
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
