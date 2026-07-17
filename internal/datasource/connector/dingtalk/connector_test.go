package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeDingTalkAPI struct {
	pingErr error

	workspaces    []workspace
	workspacesErr error
	workspaceByID map[string]workspace
	workspaceErr  map[string]error
	nodesByParent map[string][]wikiNode
	nodesErr      map[string]error
	nodeByID      map[string]wikiNode
	nodeErr       map[string]error
	blocksByNode  map[string][]json.RawMessage
	blocksErr     map[string]error

	pingCalls          int
	listWorkspaceCalls int
	getWorkspaceCalls  []string
	listNodeCalls      []string
	getNodeCalls       []string
	blockCalls         []string
}

func (f *fakeDingTalkAPI) Ping(context.Context) error {
	f.pingCalls++
	return f.pingErr
}

func (f *fakeDingTalkAPI) ListWorkspaces(context.Context) ([]workspace, error) {
	f.listWorkspaceCalls++
	return append([]workspace(nil), f.workspaces...), f.workspacesErr
}

func (f *fakeDingTalkAPI) GetWorkspace(_ context.Context, id string) (workspace, error) {
	f.getWorkspaceCalls = append(f.getWorkspaceCalls, id)
	if err := f.workspaceErr[id]; err != nil {
		return workspace{}, err
	}
	return f.workspaceByID[id], nil
}

func (f *fakeDingTalkAPI) ListNodes(_ context.Context, parentID string) ([]wikiNode, error) {
	f.listNodeCalls = append(f.listNodeCalls, parentID)
	if err := f.nodesErr[parentID]; err != nil {
		return nil, err
	}
	return append([]wikiNode(nil), f.nodesByParent[parentID]...), nil
}

func (f *fakeDingTalkAPI) GetNode(_ context.Context, nodeID string) (wikiNode, error) {
	f.getNodeCalls = append(f.getNodeCalls, nodeID)
	if err := f.nodeErr[nodeID]; err != nil {
		return wikiNode{}, err
	}
	return f.nodeByID[nodeID], nil
}

func (f *fakeDingTalkAPI) GetDocumentBlocks(_ context.Context, nodeID string) ([]json.RawMessage, error) {
	f.blockCalls = append(f.blockCalls, nodeID)
	if err := f.blocksErr[nodeID]; err != nil {
		return nil, err
	}
	return append([]json.RawMessage(nil), f.blocksByNode[nodeID]...), nil
}

func newFakeAPI() *fakeDingTalkAPI {
	return &fakeDingTalkAPI{
		workspaceByID: make(map[string]workspace),
		workspaceErr:  make(map[string]error),
		nodesByParent: make(map[string][]wikiNode),
		nodesErr:      make(map[string]error),
		nodeByID:      make(map[string]wikiNode),
		nodeErr:       make(map[string]error),
		blocksByNode:  make(map[string][]json.RawMessage),
		blocksErr:     make(map[string]error),
	}
}

func connectorWithFake(fake *fakeDingTalkAPI) *Connector {
	return &Connector{newClient: func(*Config) dingTalkAPI { return fake }}
}

func dingtalkTestConfig(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id": "app-key", "client_secret": "secret", "operator_id": "union-id",
		},
		ResourceIDs: resourceIDs,
	}
}

func docNode(id, name, modified string) wikiNode {
	return wikiNode{NodeID: id, WorkspaceID: "w1", Name: name, NodeType: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: modified}
}

func paragraphBlock(t *testing.T, text string) json.RawMessage {
	t.Helper()
	return rawBlock(t, map[string]interface{}{"blockType": "paragraph", "paragraph": map[string]interface{}{"text": text}})
}

func itemByExternalID(items []types.FetchedItem, id string) *types.FetchedItem {
	for i := range items {
		if items[i].ExternalID == id {
			return &items[i]
		}
	}
	return nil
}

func requirePartialFetch(t *testing.T, err error) *datasource.PartialFetchError {
	t.Helper()
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialFetchError", err)
	}
	if len(partial.Details) == 0 {
		t.Fatal("PartialFetchError has no details")
	}
	return partial
}

func decodedCursor(t *testing.T, cursor *types.SyncCursor) *dingTalkCursor {
	t.Helper()
	decoded, err := decodeDingTalkCursor(cursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	return decoded
}

func TestConnectorValidateAndType(t *testing.T) {
	fake := newFakeAPI()
	connector := connectorWithFake(fake)
	if connector.Type() != types.ConnectorTypeDingTalk {
		t.Fatalf("Type() = %q", connector.Type())
	}
	if err := connector.Validate(context.Background(), dingtalkTestConfig()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if fake.pingCalls != 1 {
		t.Fatalf("Ping calls = %d, want 1", fake.pingCalls)
	}
	fake.pingErr = errors.New("permission denied")
	if err := connector.Validate(context.Background(), dingtalkTestConfig()); err == nil || !strings.Contains(err.Error(), "connection failed") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConnectorListResourcesAndAncestors(t *testing.T) {
	fake := newFakeAPI()
	fake.workspaces = []workspace{
		{WorkspaceID: "w2", RootNodeID: "root2", Name: "Zulu"},
		{WorkspaceID: "w1", RootNodeID: "root1", Name: "Alpha", Description: "Docs"},
	}
	fake.workspaceByID["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root1", Name: "Alpha"}
	fake.nodesByParent["root1"] = []wikiNode{
		{NodeID: "doc-z", WorkspaceID: "w1", Name: "Zulu doc", NodeType: "FILE", Category: "ALIDOC", Extension: "adoc"},
		{NodeID: "folder-a", WorkspaceID: "w1", Name: "Alpha folder", NodeType: "FOLDER", HasChildren: true},
		{NodeID: "sheet", WorkspaceID: "w1", Name: "Sheet", NodeType: "FILE", Category: "ALIDOC", Extension: "axls"},
	}
	fake.nodesByParent["folder-a"] = []wikiNode{docNode("deep-doc", "Deep doc", "2025-01-01T00:00:00Z")}
	connector := connectorWithFake(fake)
	cfg := dingtalkTestConfig()

	roots, err := connector.ListResources(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("ListResources(root) error = %v", err)
	}
	if len(roots) != 2 || roots[0].Name != "Alpha" || roots[1].Name != "Zulu" {
		t.Fatalf("root resources = %#v", roots)
	}
	workspaceRef, err := decodeResourceRef(roots[0].ExternalID)
	if err != nil || workspaceRef.WorkspaceID != "w1" || workspaceRef.NodeID != "" {
		t.Fatalf("workspace ref=%#v err=%v", workspaceRef, err)
	}

	children, err := connector.ListResources(context.Background(), cfg, roots[0].ExternalID)
	if err != nil {
		t.Fatalf("ListResources(workspace) error = %v", err)
	}
	if len(children) != 2 || children[0].Type != "folder" || children[1].Type != "document" {
		t.Fatalf("children = %#v", children)
	}
	if children[0].ParentID != roots[0].ExternalID || children[1].ParentID != roots[0].ExternalID {
		t.Fatalf("children parent IDs not preserved")
	}

	grandchildren, err := connector.ListResources(context.Background(), cfg, children[0].ExternalID)
	if err != nil {
		t.Fatalf("ListResources(folder) error = %v", err)
	}
	if len(grandchildren) != 1 || grandchildren[0].Name != "Deep doc" || grandchildren[0].ParentID != children[0].ExternalID {
		t.Fatalf("grandchildren = %#v", grandchildren)
	}
	deepRef, err := decodeResourceRef(grandchildren[0].ExternalID)
	if err != nil || deepRef.NodeID != "deep-doc" || len(deepRef.Ancestors) != 1 || deepRef.Ancestors[0] != "folder-a" {
		t.Fatalf("deep ref=%#v err=%v", deepRef, err)
	}

	callsBefore := len(fake.listNodeCalls) + len(fake.getNodeCalls) + len(fake.getWorkspaceCalls)
	ancestors, err := connector.ResolveResourceAncestors(context.Background(), cfg, []string{grandchildren[0].ExternalID})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error = %v", err)
	}
	if len(ancestors) != 2 {
		t.Fatalf("ancestors = %v, want workspace and folder", ancestors)
	}
	callsAfter := len(fake.listNodeCalls) + len(fake.getNodeCalls) + len(fake.getWorkspaceCalls)
	if callsAfter != callsBefore {
		t.Fatalf("ancestor restoration made API calls: before=%d after=%d", callsBefore, callsAfter)
	}
}

func TestConnectorFetchAllWorkspaceFolderAndDocument(t *testing.T) {
	fake := newFakeAPI()
	fake.workspaceByID["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	folder := wikiNode{NodeID: "folder", WorkspaceID: "w1", Name: "Folder", NodeType: "FOLDER", HasChildren: true}
	doc1 := docNode("doc1", "One", "2025-01-01T00:00:00Z")
	doc2 := docNode("doc2", "Two", "2025-01-02T00:00:00Z")
	fake.nodesByParent["root"] = []wikiNode{folder, doc1}
	fake.nodesByParent["folder"] = []wikiNode{doc2}
	fake.nodeByID["folder"] = folder
	fake.nodeByID["doc1"] = doc1
	fake.blocksByNode["doc1"] = []json.RawMessage{paragraphBlock(t, "content one")}
	fake.blocksByNode["doc2"] = []json.RawMessage{paragraphBlock(t, "content two")}
	connector := connectorWithFake(fake)

	workspaceID, _ := encodeResourceRef(workspaceResourceRef("w1"))
	items, err := connector.FetchAll(context.Background(), dingtalkTestConfig(), []string{workspaceID, workspaceID})
	if err != nil {
		t.Fatalf("FetchAll(workspace) error = %v", err)
	}
	if len(items) != 2 || itemByExternalID(items, "doc1") == nil || itemByExternalID(items, "doc2") == nil {
		t.Fatalf("workspace items = %#v", items)
	}
	if got := string(itemByExternalID(items, "doc1").Content); !strings.Contains(got, "content one") {
		t.Fatalf("doc1 content = %q", got)
	}

	folderID, _ := encodeResourceRef(childResourceRef(workspaceResourceRef("w1"), "folder"))
	items, err = connector.FetchAll(context.Background(), dingtalkTestConfig(), []string{folderID})
	if err != nil || len(items) != 1 || items[0].ExternalID != "doc2" {
		t.Fatalf("FetchAll(folder) items=%#v err=%v", items, err)
	}

	docID, _ := encodeResourceRef(childResourceRef(workspaceResourceRef("w1"), "doc1"))
	items, err = connector.FetchAll(context.Background(), dingtalkTestConfig(), []string{docID})
	if err != nil || len(items) != 1 || items[0].ExternalID != "doc1" {
		t.Fatalf("FetchAll(document) items=%#v err=%v", items, err)
	}
}

func TestConnectorIncrementalSkipsUnchangedRetriesFailedAndDetectsDeletion(t *testing.T) {
	fake := newFakeAPI()
	fake.workspaceByID["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	doc1 := docNode("doc1", "One", "v1")
	doc2 := docNode("doc2", "Two", "v1")
	fake.nodesByParent["root"] = []wikiNode{doc1, doc2}
	fake.blocksByNode["doc1"] = []json.RawMessage{paragraphBlock(t, "one v1")}
	fake.blocksByNode["doc2"] = []json.RawMessage{paragraphBlock(t, "two v1")}
	workspaceID, _ := encodeResourceRef(workspaceResourceRef("w1"))
	cfg := dingtalkTestConfig(workspaceID)
	connector := connectorWithFake(fake)

	initial, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	if err != nil || len(initial) != 2 {
		t.Fatalf("initial items=%#v cursor=%#v err=%v", initial, cursor, err)
	}
	fake.blockCalls = nil
	unchanged, cursor2, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil || len(unchanged) != 0 || len(fake.blockCalls) != 0 {
		t.Fatalf("unchanged items=%#v blockCalls=%v err=%v", unchanged, fake.blockCalls, err)
	}

	doc1.ModifiedTime = "v2"
	fake.nodesByParent["root"] = []wikiNode{doc1}
	fake.blocksErr["doc1"] = errors.New("temporary content failure")
	changed, cursor3, err := connector.FetchIncremental(context.Background(), cfg, cursor2)
	requirePartialFetch(t, err)
	failure := itemByExternalID(changed, "doc1")
	deletion := itemByExternalID(changed, "doc2")
	if failure == nil || failure.Metadata["failure_stage"] != "fetch_content" {
		t.Fatalf("missing doc failure: %#v", changed)
	}
	if deletion == nil || !deletion.IsDeleted {
		t.Fatalf("missing doc2 deletion: %#v", changed)
	}
	state := decodedCursor(t, cursor3).Resources[workspaceID]
	if state.Nodes["doc1"].ModifiedTime != "v1" {
		t.Fatalf("failed doc cursor advanced: %#v", state.Nodes["doc1"])
	}
	if _, exists := state.Nodes["doc2"]; exists {
		t.Fatalf("deleted doc remained in complete cursor: %#v", state.Nodes)
	}

	fake.blockCalls = nil
	_, _, err = connector.FetchIncremental(context.Background(), cfg, cursor3)
	requirePartialFetch(t, err)
	if len(fake.blockCalls) != 1 || fake.blockCalls[0] != "doc1" {
		t.Fatalf("failed changed doc was not retried: %v", fake.blockCalls)
	}
}

func TestConnectorPartialTraversalPreservesCursorAndAvoidsFalseDeletion(t *testing.T) {
	fake := newFakeAPI()
	fake.workspaceByID["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	folder := wikiNode{NodeID: "folder", WorkspaceID: "w1", Name: "Folder", NodeType: "FOLDER", HasChildren: true}
	oldDoc := docNode("old", "Old", "v1")
	fake.nodesByParent["root"] = []wikiNode{folder}
	fake.nodesByParent["folder"] = []wikiNode{oldDoc}
	fake.blocksByNode["old"] = []json.RawMessage{paragraphBlock(t, "old")}
	workspaceID, _ := encodeResourceRef(workspaceResourceRef("w1"))
	cfg := dingtalkTestConfig(workspaceID)
	connector := connectorWithFake(fake)

	_, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake.nodesErr["folder"] = errors.New("branch unavailable")
	items, nextCursor, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	requirePartialFetch(t, err)
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("partial traversal emitted false deletion: %#v", item)
		}
	}
	var branchFailure *types.FetchedItem
	for i := range items {
		if items[i].Metadata["failure_stage"] == "list_children" {
			branchFailure = &items[i]
		}
	}
	if branchFailure == nil {
		t.Fatalf("missing branch failure item: %#v", items)
	}
	state := decodedCursor(t, nextCursor).Resources[workspaceID]
	if state.Nodes["old"].ModifiedTime != "v1" {
		t.Fatalf("partial traversal lost previous cursor: %#v", state.Nodes)
	}
}

func TestConnectorResourceFailurePreservesPreviousState(t *testing.T) {
	fake := newFakeAPI()
	fake.workspaceByID["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	doc := docNode("doc", "Doc", "v1")
	fake.nodesByParent["root"] = []wikiNode{doc}
	fake.blocksByNode["doc"] = []json.RawMessage{paragraphBlock(t, "content")}
	workspaceID, _ := encodeResourceRef(workspaceResourceRef("w1"))
	cfg := dingtalkTestConfig(workspaceID)
	connector := connectorWithFake(fake)
	_, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake.workspaceErr["w1"] = errors.New("workspace unavailable")
	items, nextCursor, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	requirePartialFetch(t, err)
	if len(items) != 1 || items[0].Metadata["failure_stage"] != "resolve_resource" {
		t.Fatalf("resource failure items = %#v", items)
	}
	state := decodedCursor(t, nextCursor).Resources[workspaceID]
	if state.Nodes["doc"].ModifiedTime != "v1" {
		t.Fatalf("resource failure lost cursor: %#v", state.Nodes)
	}
}

func TestConnectorRejectsNodeFromAnotherWorkspace(t *testing.T) {
	fake := newFakeAPI()
	fake.nodeByID["doc"] = wikiNode{NodeID: "doc", WorkspaceID: "w2", Name: "Doc", NodeType: "FILE", Category: "ALIDOC", Extension: "adoc"}
	resourceID, _ := encodeResourceRef(childResourceRef(workspaceResourceRef("w1"), "doc"))
	items, err := connectorWithFake(fake).FetchAll(context.Background(), dingtalkTestConfig(), []string{resourceID})
	requirePartialFetch(t, err)
	if len(items) != 1 || items[0].Metadata["failure_stage"] != "resolve_resource" {
		t.Fatalf("items = %#v", items)
	}
}

func TestDecodeCursorVersionAndResourceSorting(t *testing.T) {
	_, err := decodeDingTalkCursor(&types.SyncCursor{ConnectorCursor: map[string]interface{}{"version": 99}})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("decode cursor error = %v", err)
	}

	resources := []types.Resource{
		{Name: "z", Type: "document", ExternalID: "3"},
		{Name: "b", Type: "folder", ExternalID: "2"},
		{Name: "A", Type: "document", ExternalID: "1"},
	}
	sortResources(resources)
	got := []string{resources[0].Name, resources[1].Name, resources[2].Name}
	want := []string{"b", "A", "z"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sorted names = %v, want %v", got, want)
	}
}
