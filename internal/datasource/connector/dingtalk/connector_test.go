package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAPI struct {
	validateErr  error
	workspaces   []workspace
	workspaceMap map[string]workspace
	workspaceErr map[string]error
	children     map[string][]node
	childrenErr  map[string]error
	nodes        map[string]node
	nodeErr      map[string]error
	blocks       map[string][]json.RawMessage
	blockErr     map[string]error
	blockCalls   []string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		workspaceMap: make(map[string]workspace),
		workspaceErr: make(map[string]error),
		children:     make(map[string][]node),
		childrenErr:  make(map[string]error),
		nodes:        make(map[string]node),
		nodeErr:      make(map[string]error),
		blocks:       make(map[string][]json.RawMessage),
		blockErr:     make(map[string]error),
	}
}

func (f *fakeAPI) validate(context.Context) error { return f.validateErr }

func (f *fakeAPI) listWorkspaces(context.Context) ([]workspace, error) {
	return append([]workspace(nil), f.workspaces...), nil
}

func (f *fakeAPI) getWorkspace(_ context.Context, id string) (workspace, error) {
	if err := f.workspaceErr[id]; err != nil {
		return workspace{}, err
	}
	value, ok := f.workspaceMap[id]
	if !ok {
		return workspace{}, fmt.Errorf("workspace %s missing", id)
	}
	return value, nil
}

func (f *fakeAPI) listChildren(_ context.Context, parent string) ([]node, error) {
	if err := f.childrenErr[parent]; err != nil {
		return nil, err
	}
	out := append([]node(nil), f.children[parent]...)
	for i := range out {
		out[i].ParentNodeID = parent
	}
	return out, nil
}

func (f *fakeAPI) getNode(_ context.Context, id string) (node, error) {
	if err := f.nodeErr[id]; err != nil {
		return node{}, err
	}
	value, ok := f.nodes[id]
	if !ok {
		return node{}, fmt.Errorf("node %s missing", id)
	}
	return value, nil
}

func (f *fakeAPI) listDocumentBlocks(_ context.Context, id string) ([]json.RawMessage, error) {
	f.blockCalls = append(f.blockCalls, id)
	if err := f.blockErr[id]; err != nil {
		return nil, err
	}
	return append([]json.RawMessage(nil), f.blocks[id]...), nil
}

func connectorWithAPI(api dingTalkAPI) *Connector {
	return &Connector{newAPI: func(*Config) dingTalkAPI { return api }}
}

func testDataSourceConfig(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id": "id", "client_secret": "secret", "operator_id": "operator",
		},
		ResourceIDs: resourceIDs,
	}
}

func onlineDoc(id, title, modified string) node {
	return node{
		NodeID: id, WorkspaceID: "w1", Name: title,
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
		ModifiedTime: modified, URL: "https://alidocs.dingtalk.com/i/nodes/" + id,
	}
}

func paragraph(t *testing.T, text string) json.RawMessage {
	t.Helper()
	return blockJSON(t, map[string]interface{}{
		"blockType": "paragraph",
		"paragraph": map[string]interface{}{"text": text},
	})
}

func mustResourceID(t *testing.T, ref resourceReference) string {
	t.Helper()
	id, err := encodeResourceReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestConnectorValidateAndType(t *testing.T) {
	api := newFakeAPI()
	connector := connectorWithAPI(api)
	if connector.Type() != types.ConnectorTypeDingTalk {
		t.Fatalf("Type() = %q", connector.Type())
	}
	if err := connector.Validate(context.Background(), testDataSourceConfig()); err != nil {
		t.Fatal(err)
	}
	api.validateErr = errors.New("permission denied")
	if err := connector.Validate(context.Background(), testDataSourceConfig()); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestConnectorListsLazyResourcesAndAncestors(t *testing.T) {
	api := newFakeAPI()
	api.workspaces = []workspace{
		{WorkspaceID: "w2", RootNodeID: "r2", Name: "Zulu"},
		{WorkspaceID: "w1", RootNodeID: "r1", Name: "Alpha"},
	}
	api.workspaceMap["w1"] = api.workspaces[1]
	api.children["r1"] = []node{
		{NodeID: "doc", WorkspaceID: "w1", Name: "Doc", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
		{NodeID: "folder", WorkspaceID: "w1", Name: "Folder", Type: "FOLDER", HasChildren: true},
		{NodeID: "sheet", WorkspaceID: "w1", Name: "Sheet", Type: "FILE", Category: "ALIDOC", Extension: "axls"},
	}
	api.children["folder"] = []node{onlineDoc("deep", "Deep", "v1")}
	connector := connectorWithAPI(api)

	roots, err := connector.ListResources(context.Background(), testDataSourceConfig(), "")
	if err != nil || len(roots) != 2 || roots[0].Name != "Alpha" {
		t.Fatalf("root resources=%#v err=%v", roots, err)
	}
	children, err := connector.ListResources(context.Background(), testDataSourceConfig(), roots[0].ExternalID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children=%#v err=%v", children, err)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Type < children[j].Type })
	if children[0].Type != "document" || children[1].Type != "folder" {
		t.Fatalf("child types=%q,%q", children[0].Type, children[1].Type)
	}
	var folder types.Resource
	for _, child := range children {
		if child.Type == "folder" {
			folder = child
		}
	}
	deep, err := connector.ListResources(context.Background(), testDataSourceConfig(), folder.ExternalID)
	if err != nil || len(deep) != 1 || deep[0].Name != "Deep" {
		t.Fatalf("deep resources=%#v err=%v", deep, err)
	}
	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(), testDataSourceConfig(), []string{deep[0].ExternalID},
	)
	if err != nil || len(ancestors) != 2 ||
		ancestors[0] != roots[0].ExternalID || ancestors[1] != folder.ExternalID {
		t.Fatalf("ancestors=%#v err=%v", ancestors, err)
	}
}

func TestConnectorFetchesWorkspaceFolderAndDocumentSelections(t *testing.T) {
	api := newFakeAPI()
	api.workspaceMap["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	folder := node{NodeID: "folder", WorkspaceID: "w1", Name: "Folder", Type: "FOLDER", HasChildren: true}
	doc1 := onlineDoc("doc1", "One", "v1")
	doc2 := onlineDoc("doc2", "Two", "v1")
	api.children["root"] = []node{folder, doc1}
	api.children["folder"] = []node{doc2}
	api.nodes["folder"] = folder
	api.nodes["doc1"] = doc1
	api.blocks["doc1"] = []json.RawMessage{paragraph(t, "one")}
	api.blocks["doc2"] = []json.RawMessage{paragraph(t, "two")}
	connector := connectorWithAPI(api)

	workspaceID := mustResourceID(t, resourceReference{WorkspaceID: "w1"})
	items, err := connector.FetchAll(context.Background(), testDataSourceConfig(), []string{workspaceID})
	if err != nil || len(items) != 2 || findItem(items, "doc1") == nil || findItem(items, "doc2") == nil {
		t.Fatalf("workspace items=%#v err=%v", items, err)
	}

	folderID := mustResourceID(t, resourceReference{WorkspaceID: "w1", NodeID: "folder"})
	items, err = connector.FetchAll(context.Background(), testDataSourceConfig(), []string{folderID})
	if err != nil || len(items) != 1 || items[0].ExternalID != "doc2" {
		t.Fatalf("folder items=%#v err=%v", items, err)
	}

	docID := mustResourceID(t, resourceReference{WorkspaceID: "w1", NodeID: "doc1"})
	items, err = connector.FetchAll(context.Background(), testDataSourceConfig(), []string{docID})
	if err != nil || len(items) != 1 || items[0].ExternalID != "doc1" {
		t.Fatalf("document items=%#v err=%v", items, err)
	}
	if got := string(items[0].Content); got != "# One\n\none\n" {
		t.Fatalf("document content=%q", got)
	}
}

func TestConnectorIncrementalRetriesFailuresAndDetectsDeletion(t *testing.T) {
	api := newFakeAPI()
	api.workspaceMap["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	doc1 := onlineDoc("doc1", "One", "v1")
	doc2 := onlineDoc("doc2", "Two", "v1")
	api.children["root"] = []node{doc1, doc2}
	api.blocks["doc1"] = []json.RawMessage{paragraph(t, "one")}
	api.blocks["doc2"] = []json.RawMessage{paragraph(t, "two")}
	resourceID := mustResourceID(t, resourceReference{WorkspaceID: "w1"})
	cfg := testDataSourceConfig(resourceID)
	connector := connectorWithAPI(api)

	items, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	if err != nil || len(items) != 2 {
		t.Fatalf("initial items=%#v cursor=%#v err=%v", items, cursor, err)
	}
	api.blockCalls = nil
	items, cursor, err = connector.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil || len(items) != 0 || len(api.blockCalls) != 0 {
		t.Fatalf("unchanged items=%#v calls=%v err=%v", items, api.blockCalls, err)
	}

	doc1.ModifiedTime = "v2"
	api.children["root"] = []node{doc1}
	api.blockErr["doc1"] = errors.New("temporary failure")
	items, next, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	requirePartial(t, err)
	failure := findItem(items, "doc1")
	deletion := findItem(items, "doc2")
	if failure == nil || failure.Metadata["failure_stage"] != "fetch_content" {
		t.Fatalf("failure item missing: %#v", items)
	}
	if deletion == nil || !deletion.IsDeleted {
		t.Fatalf("deletion item missing: %#v", items)
	}
	state := decodeTestCursor(t, next).Resources[resourceID]
	if state.Documents["doc1"] != "v1" {
		t.Fatalf("failed document cursor advanced: %#v", state.Documents)
	}
	if _, exists := state.Documents["doc2"]; exists {
		t.Fatalf("deleted document remained in cursor: %#v", state.Documents)
	}

	api.blockCalls = nil
	_, _, err = connector.FetchIncremental(context.Background(), cfg, next)
	requirePartial(t, err)
	if len(api.blockCalls) != 1 || api.blockCalls[0] != "doc1" {
		t.Fatalf("failed document was not retried: %v", api.blockCalls)
	}
}

func TestConnectorIncompleteTraversalPreservesCursorWithoutDeletion(t *testing.T) {
	api := newFakeAPI()
	api.workspaceMap["w1"] = workspace{WorkspaceID: "w1", RootNodeID: "root"}
	folder := node{NodeID: "folder", WorkspaceID: "w1", Type: "FOLDER", HasChildren: true}
	doc := onlineDoc("doc", "Doc", "v1")
	api.children["root"] = []node{folder}
	api.children["folder"] = []node{doc}
	api.blocks["doc"] = []json.RawMessage{paragraph(t, "content")}
	resourceID := mustResourceID(t, resourceReference{WorkspaceID: "w1"})
	cfg := testDataSourceConfig(resourceID)
	connector := connectorWithAPI(api)

	_, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.childrenErr["folder"] = errors.New("branch unavailable")
	items, next, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	requirePartial(t, err)
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("incomplete traversal emitted deletion: %#v", item)
		}
	}
	if decodeTestCursor(t, next).Resources[resourceID].Documents["doc"] != "v1" {
		t.Fatalf("previous cursor not preserved")
	}
}

func TestConnectorRejectsSelectedNodeFromAnotherWorkspace(t *testing.T) {
	api := newFakeAPI()
	api.nodes["doc"] = node{
		NodeID: "doc", WorkspaceID: "other",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
	}
	resourceID := mustResourceID(t, resourceReference{WorkspaceID: "w1", NodeID: "doc"})
	_, err := connectorWithAPI(api).FetchAll(
		context.Background(), testDataSourceConfig(), []string{resourceID},
	)
	requirePartial(t, err)
}

func TestFailureClassification(t *testing.T) {
	tests := []struct {
		err      error
		wantCode string
	}{
		{err: &apiError{StatusCode: 403}, wantCode: "dingtalk_auth_or_permission"},
		{err: &apiError{StatusCode: 429}, wantCode: "dingtalk_rate_limited"},
		{err: &apiError{StatusCode: 503}, wantCode: "dingtalk_unavailable"},
		{err: context.DeadlineExceeded, wantCode: "dingtalk_timeout"},
	}
	for _, tt := range tests {
		code, _, fallback := classifyFailure(tt.err)
		if code != tt.wantCode || fallback == "" {
			t.Fatalf("classifyFailure(%v) = (%q, %q)", tt.err, code, fallback)
		}
	}
}

func findItem(items []types.FetchedItem, id string) *types.FetchedItem {
	for i := range items {
		if items[i].ExternalID == id {
			return &items[i]
		}
	}
	return nil
}

func requirePartial(t *testing.T, err error) {
	t.Helper()
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("error=%v, want PartialFetchError", err)
	}
}

func decodeTestCursor(t *testing.T, cursor *types.SyncCursor) connectorCursor {
	t.Helper()
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		t.Fatal(err)
	}
	var decoded connectorCursor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
