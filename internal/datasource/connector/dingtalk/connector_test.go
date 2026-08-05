package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeDingTalk is a scriptable stand-in for the DingTalk Open API.
type fakeDingTalk struct {
	mu         *http.ServeMux
	srv        *httptest.Server
	tokenCalls int64
	nodeCalls  int64

	// workspaces keyed by workspaceId
	workspaces []workspace
	// children keyed by parentNodeId
	children map[string][]node
	// details keyed by nodeId
	details map[string]node
	// docs keyed by docKey -> blocks
	docs map[string][]block

	// failChildrenOf makes listing children of this parent return 500 forever.
	failChildrenOf string
	// failDocKey makes fetching this document's blocks fail.
	failDocKey string
}

func newFakeDingTalk(t *testing.T) *fakeDingTalk {
	t.Helper()
	f := &fakeDingTalk{
		mu:       http.NewServeMux(),
		children: make(map[string][]node),
		details:  make(map[string]node),
		docs:     make(map[string][]block),
	}
	f.mu.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.tokenCalls, 1)
		fmt.Fprint(w, `{"accessToken":"tok","expireIn":7200}`)
	})
	f.mu.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspacesPage{Workspaces: f.workspaces})
	})
	f.mu.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.nodeCalls, 1)
		parent := r.URL.Query().Get("parentNodeId")
		if parent != "" && parent == f.failChildrenOf {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"code":"InternalError","message":"boom"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(nodesPage{Nodes: f.children[parent]})
	})
	f.mu.HandleFunc("/v2.0/wiki/nodes/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/nodes/")
		detail, ok := f.details[nodeID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"code":"NotFound","message":"node not found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(nodeDetail{Node: detail})
	})
	f.mu.HandleFunc("/v1.0/doc/suites/documents/", func(w http.ResponseWriter, r *http.Request) {
		// path: /v1.0/doc/suites/documents/{docKey}/blocks
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 6 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		docKey := parts[4]
		if docKey == f.failDocKey {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"code":"InternalError","message":"doc boom"}`)
			return
		}
		response := blocksResponse{Success: true}
		response.Result.Data = f.docs[docKey]
		_ = json.NewEncoder(w).Encode(response)
	})
	f.srv = httptest.NewServer(f.mu)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDingTalk) dsConfig(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":     "dingabcdefghij",
			"app_secret":  "s3cret-value-should-never-leak",
			"operator_id": "OPERATOR_UNION_ID",
		},
		Settings:    map[string]interface{}{"tenant_id": "t1", "data_source_id": "ds1"},
		ResourceIDs: resourceIDs,
	}
}

// connector is the only endpoint injection path used by connector contract
// tests. NewConnector always talks to the fixed official DingTalk API.
func (f *fakeDingTalk) connector() *Connector {
	return &Connector{
		clientFactory: func(cfg *config) (*client, error) {
			testCfg := *cfg
			testCfg.BaseURL = f.srv.URL
			return &client{cfg: &testCfg, httpClient: f.srv.Client()}, nil
		},
	}
}

// textBlock builds a paragraph block carrying the given text.
func textBlock(text string) block {
	return block{BlockType: "paragraph", Value: map[string]interface{}{"text": text}}
}

func TestConnectorType(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeDingTalk {
		t.Fatalf("Type() = %q, want %q", got, types.ConnectorTypeDingTalk)
	}
	meta, ok := datasource.ConnectorMetadataRegistry[types.ConnectorTypeDingTalk]
	if !ok {
		t.Fatal("DingTalk connector metadata is not registered")
	}
	if meta.AuthType != "oauth2" {
		t.Fatalf("DingTalk auth type = %q, want oauth2 app-token flow", meta.AuthType)
	}
	if !containsString(meta.Capabilities, "incremental") ||
		!containsString(meta.Capabilities, "deletion_sync") {
		t.Fatalf("DingTalk capabilities = %v", meta.Capabilities)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestFetchAllDirectDocumentSelectionFetchesTheDocument(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.details["n1"] = node{
		NodeID:       "n1",
		WorkspaceID:  "ws1",
		Name:         "Direct document",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		ModifiedTime: "2026-07-28T11:12:13Z",
		URL:          "https://alidocs.dingtalk.com/i/n1?signature=private-source-secret#fragment",
	}
	f.docs["n1"] = []block{textBlock("selected content")}

	items, err := f.connector().FetchAll(context.Background(), f.dsConfig("n1"), []string{"n1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll returned %d items, want selected document", len(items))
	}
	if items[0].ExternalID != "n1" || !strings.Contains(string(items[0].Content), "selected content") {
		t.Fatalf("unexpected selected document: %+v", items[0])
	}
	if items[0].URL != "https://alidocs.dingtalk.com/i/n1" {
		t.Fatalf("source URL retained private query material: %q", items[0].URL)
	}
}

func TestFetchAllSurfacesUnknownBlockDiagnosticWithoutDroppingChildren(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.details["n1"] = node{
		NodeID:       "n1",
		WorkspaceID:  "ws1",
		Name:         "Future block document",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		ModifiedTime: "1000",
	}
	f.docs["n1"] = []block{{
		BlockType: "future_layout",
		Children: []block{{
			BlockType: "paragraph",
			Value:     map[string]interface{}{"text": "preserved child"},
		}},
	}}

	items, err := f.connector().FetchAll(
		context.Background(),
		f.dsConfig("n1"),
		[]string{"n1"},
	)

	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchAll error = %v, want PartialFetchError diagnostic", err)
	}
	if len(items) != 1 || !strings.Contains(string(items[0].Content), "preserved child") {
		t.Fatalf("unknown block content was not preserved: %+v", items)
	}
	if len(partial.Details) != 1 ||
		!strings.Contains(partial.Details[0], "unsupported block type") {
		t.Fatalf("unknown block diagnostics = %v", partial.Details)
	}
}

func TestFetchModesRejectEmptyResourceSelection(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	connector := f.connector()
	config := f.dsConfig()

	if _, err := connector.FetchAll(context.Background(), config, nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("FetchAll empty selection error = %v, want ErrInvalidConfig", err)
	}
	if _, _, err := connector.FetchAllWithCursor(
		context.Background(),
		config,
		nil,
		&types.SyncCursor{ConnectorCursor: map[string]interface{}{}},
	); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("FetchAllWithCursor empty selection error = %v, want ErrInvalidConfig", err)
	}
	if _, _, err := connector.FetchIncremental(
		context.Background(),
		config,
		nil,
	); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("FetchIncremental empty selection error = %v, want ErrInvalidConfig", err)
	}
}

func TestFetchAllEmptyDocumentUsesTitleMarkdownInsteadOfPrivateURLFallback(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.details["n-empty"] = node{
		NodeID:       "n-empty",
		WorkspaceID:  "ws1",
		Name:         "Empty planning note",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		ModifiedTime: "2026-07-28T11:12:13Z",
		URL:          "https://alidocs.dingtalk.com/i/n-empty?signature=private-source-secret",
	}
	f.docs["n-empty"] = nil

	items, err := f.connector().FetchAll(
		context.Background(),
		f.dsConfig("n-empty"),
		[]string{"n-empty"},
	)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll returned %d items, want empty document placeholder", len(items))
	}
	if got := string(items[0].Content); got != "# Empty planning note\n" {
		t.Fatalf("empty document content = %q, want title-only Markdown", got)
	}
}

func TestFetchAllOverlappingSelectionsEmitEachDocumentOnce(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	doc := node{
		NodeID:       "n1",
		WorkspaceID:  "ws1",
		Name:         "Shared document",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		ModifiedTime: "2026-07-28T11:12:13Z",
	}
	f.children["root"] = []node{doc}
	f.details["n1"] = doc
	f.docs["n1"] = []block{textBlock("only once")}

	items, err := f.connector().FetchAll(
		context.Background(),
		f.dsConfig("ws1", "n1"),
		[]string{"ws1", "n1"},
	)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "n1" {
		t.Fatalf("overlapping selections returned %+v, want n1 exactly once", items)
	}
}

func TestFetchAllDocumentWithChildrenFetchesParentAndDescends(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	parent := node{
		NodeID:       "parent-doc",
		WorkspaceID:  "ws1",
		Name:         "Parent document",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		HasChildren:  true,
		ModifiedTime: "1000",
	}
	child := node{
		NodeID:       "child-doc",
		WorkspaceID:  "ws1",
		Name:         "Child document",
		Type:         "FILE",
		Category:     "ALIDOC",
		Extension:    "adoc",
		ModifiedTime: "2000",
	}
	f.children["root"] = []node{parent}
	f.children[parent.NodeID] = []node{child}
	f.docs[parent.NodeID] = []block{textBlock("parent body")}
	f.docs[child.NodeID] = []block{textBlock("child body")}

	items, err := f.connector().FetchAll(
		context.Background(),
		f.dsConfig("ws1"),
		[]string{"ws1"},
	)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("FetchAll returned %d items, want parent and child", len(items))
	}
	got := map[string]string{}
	for _, item := range items {
		got[item.ExternalID] = string(item.Content)
	}
	if !strings.Contains(got[parent.NodeID], "parent body") {
		t.Fatalf("parent document was not fetched: %+v", got)
	}
	if !strings.Contains(got[child.NodeID], "child body") {
		t.Fatalf("child document was not fetched: %+v", got)
	}
}

func TestLazyResourceIDsCarryPathForAncestorRecovery(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	folder := node{
		NodeID:      "folder-1",
		WorkspaceID: "ws1",
		Name:        "Folder",
		Type:        "FOLDER",
		HasChildren: true,
	}
	doc := node{
		NodeID:      "doc-1",
		WorkspaceID: "ws1",
		Name:        "Document",
		Type:        "FILE",
		Category:    "ALIDOC",
		Extension:   "adoc",
	}
	f.children["root"] = []node{folder}
	f.children["folder-1"] = []node{doc}
	f.details["folder-1"] = folder
	f.details["doc-1"] = doc

	connector := f.connector()
	cfg := f.dsConfig()
	top, err := connector.ListResources(context.Background(), cfg, "ws1")
	if err != nil {
		t.Fatalf("ListResources(workspace): %v", err)
	}
	if len(top) != 1 {
		t.Fatalf("top resources = %+v", top)
	}
	folderID := top[0].ExternalID
	if folderID == "" || folderID == "folder-1" {
		t.Fatalf("folder ID %q does not carry its workspace/path context", folderID)
	}

	children, err := connector.ListResources(context.Background(), cfg, folderID)
	if err != nil {
		t.Fatalf("ListResources(folder): %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("folder children = %+v", children)
	}
	docID := children[0].ExternalID
	if docID == "" || docID == "doc-1" {
		t.Fatalf("document ID %q does not carry its ancestor path", docID)
	}

	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(),
		cfg,
		[]string{docID},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors: %v", err)
	}
	if len(ancestors) != 2 || ancestors[0] != "ws1" || ancestors[1] != folderID {
		t.Fatalf("ancestors = %v, want [ws1 %s]", ancestors, folderID)
	}
}

func TestResolveResourceAncestorsUsesEncodedPathWhenNodeIsUnavailable(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	folderID := makeNodeResourceID("ws1", []string{"folder-1"})
	docID := makeNodeResourceID("ws1", []string{"folder-1", "deleted-doc"})

	ancestors, err := f.connector().ResolveResourceAncestors(
		context.Background(),
		f.dsConfig(),
		[]string{docID},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors: %v", err)
	}
	if len(ancestors) != 2 || ancestors[0] != "ws1" || ancestors[1] != folderID {
		t.Fatalf("ancestors = %v, want encoded recovery path [ws1 %s]", ancestors, folderID)
	}
	if got := atomic.LoadInt64(&f.nodeCalls); got != 0 {
		t.Fatalf("ancestor recovery made %d node API calls for an unavailable node", got)
	}
}

func TestListResourcesRejectsCrossWorkspaceChildren(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "foreign-doc", WorkspaceID: "ws2", Name: "Foreign",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
	}}

	_, err := f.connector().ListResources(context.Background(), f.dsConfig(), "ws1")
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("ListResources cross-workspace child error = %v, want ErrInvalidConfig", err)
	}
}

func TestMalformedOrCyclicOpaqueResourceIDIsRejected(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	cyclicID := makeNodeResourceID("ws1", []string{"folder-1", "folder-1"})

	for _, resourceID := range []string{
		nodeResourcePrefix + "not-base64!",
		cyclicID,
	} {
		_, err := f.connector().ResolveResourceAncestors(
			context.Background(),
			f.dsConfig(),
			[]string{resourceID},
		)
		if !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Errorf("ResolveResourceAncestors(%q) error = %v, want ErrInvalidConfig",
				resourceID, err)
		}
	}
}

func TestOpaqueResourceIDCannotCrossWorkspaces(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.details["doc-1"] = node{
		NodeID:      "doc-1",
		WorkspaceID: "ws2",
		Name:        "Other workspace document",
		Type:        "FILE",
		Category:    "ALIDOC",
		Extension:   "adoc",
	}
	resourceID := makeNodeResourceID("ws1", []string{"doc-1"})

	_, err := f.connector().FetchAll(
		context.Background(),
		f.dsConfig(resourceID),
		[]string{resourceID},
	)
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("FetchAll cross-workspace resource error = %v, want ErrInvalidConfig", err)
	}
}

func TestTraversalCycleIsVisitedOnlyOnce(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "folder-1", WorkspaceID: "ws1", Name: "Folder", Type: "FOLDER", HasChildren: true,
	}}
	f.children["folder-1"] = []node{{
		NodeID: "root", WorkspaceID: "ws1", Name: "Cycle", Type: "FOLDER", HasChildren: true,
	}}

	items, err := f.connector().FetchAll(context.Background(), f.dsConfig("ws1"), []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll cyclic tree: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("cyclic empty tree emitted items: %+v", items)
	}
	if got := atomic.LoadInt64(&f.nodeCalls); got != 2 {
		t.Fatalf("cyclic tree made %d node-list calls, want each container once", got)
	}
}

func TestCursorAwareFullSyncRefetchesCurrentItemsAndReconcilesDeletion(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "d1", WorkspaceID: "ws1", Name: "One", Type: "FILE", DocKey: "doc-1", ModifiedTime: "1000"},
		{NodeID: "d2", WorkspaceID: "ws1", Name: "Two", Type: "FILE", DocKey: "doc-2", ModifiedTime: "1000"},
	}
	f.docs["doc-1"] = []block{textBlock("one")}
	f.docs["doc-2"] = []block{textBlock("two")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	initial, cursor, err := connector.FetchAllWithCursor(
		context.Background(),
		config,
		config.ResourceIDs,
		nil,
	)
	if err != nil {
		t.Fatalf("initial full snapshot: %v", err)
	}
	if len(initial) != 2 || cursor == nil {
		t.Fatalf("initial full snapshot = %d items, cursor=%v", len(initial), cursor)
	}

	f.children["root"] = f.children["root"][:1]
	nextItems, nextCursor, err := connector.FetchAllWithCursor(
		context.Background(),
		config,
		config.ResourceIDs,
		cursor,
	)
	if err != nil {
		t.Fatalf("replacement full snapshot: %v", err)
	}
	var refetched, deleted bool
	for _, item := range nextItems {
		switch {
		case item.ExternalID == "d1" && !item.IsDeleted:
			refetched = true
		case item.ExternalID == "d2" && item.IsDeleted:
			deleted = true
		}
	}
	if !refetched || !deleted {
		t.Fatalf("full reconciliation refetched=%v deleted=%v items=%+v", refetched, deleted, nextItems)
	}
	if cursorKnowsDocument(t, nextCursor, "d2") {
		t.Fatal("replacement full cursor retained the deleted document")
	}
}

// A successful HTTP response is not sufficient evidence that every source
// document disappeared. Permission convergence and upstream incidents can both
// produce a transient 200 + empty collection. The first empty observation must
// therefore preserve the previous snapshot and surface a partial result instead
// of emitting deletion markers.
func TestIncrementalSuccessfulEmptySnapshotPreservesHistory(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", DocKey: "d1", ModifiedTime: "1000"},
	}
	f.docs["d1"] = []block{textBlock("still present")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root"] = nil

	items, next, err := connector.FetchIncremental(context.Background(), config, cursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("empty snapshot error = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("transient empty snapshot emitted deletion for %q", item.ExternalID)
		}
	}
	if !cursorKnowsDocument(t, next, "n1") {
		t.Fatal("empty snapshot dropped the previous document revision")
	}
}

// D1 — the headline differentiator. When one folder's listing fails mid-walk,
// the documents beneath it are unreachable this run. They must NOT be reported
// as deleted: doing so would drive downstream deletion of live knowledge-base
// content on a transient 500.
func TestIncrementalEmptyConfirmationIsPerSelectedResource(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{
		{WorkspaceID: "ws-a", Name: "A", RootNodeID: "root-a"},
		{WorkspaceID: "ws-b", Name: "B", RootNodeID: "root-b"},
	}
	f.children["root-a"] = []node{{
		NodeID: "doc-a", WorkspaceID: "ws-a", Name: "Doc A",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.children["root-b"] = []node{{
		NodeID: "doc-b", WorkspaceID: "ws-b", Name: "Doc B",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "2000",
	}}
	f.docs["doc-a"] = []block{textBlock("A")}
	f.docs["doc-b"] = []block{textBlock("B")}

	connector := f.connector()
	config := f.dsConfig("ws-a", "ws-b")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root-a"] = nil
	firstItems, firstEmptyCursor, err := connector.FetchIncremental(
		context.Background(),
		config,
		baseline,
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("first per-resource empty error = %v, want PartialFetchError", err)
	}
	for _, item := range firstItems {
		if item.IsDeleted {
			t.Fatalf("first empty snapshot for A deleted %q while B remained non-empty", item.ExternalID)
		}
	}
	if !cursorKnowsDocument(t, firstEmptyCursor, "doc-a") {
		t.Fatal("first empty snapshot for A dropped its previous revision")
	}

	secondItems, _, err := connector.FetchIncremental(
		context.Background(),
		config,
		firstEmptyCursor,
	)
	if err != nil {
		t.Fatalf("second per-resource empty snapshot: %v", err)
	}
	var deleted []string
	for _, item := range secondItems {
		if item.IsDeleted {
			deleted = append(deleted, item.ExternalID)
		}
	}
	if len(deleted) != 1 || deleted[0] != "doc-a" {
		t.Fatalf("confirmed per-resource deletions = %v, want doc-a only", deleted)
	}
}

func TestIncrementalConsecutiveHealthyEmptySnapshotConfirmsDeletion(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", DocKey: "d1", ModifiedTime: "1000"},
	}
	f.docs["d1"] = []block{textBlock("to be removed")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root"] = nil
	firstItems, firstEmptyCursor, err := connector.FetchIncremental(
		context.Background(),
		config,
		baseline,
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("first empty error = %v, want PartialFetchError", err)
	}
	for _, item := range firstItems {
		if item.IsDeleted {
			t.Fatal("first empty snapshot must not emit a deletion")
		}
	}

	secondItems, confirmedCursor, err := connector.FetchIncremental(
		context.Background(),
		config,
		firstEmptyCursor,
	)
	if err != nil {
		t.Fatalf("second consecutive healthy empty snapshot: %v", err)
	}
	if len(secondItems) != 1 || !secondItems[0].IsDeleted || secondItems[0].ExternalID != "n1" {
		t.Fatalf("second empty snapshot items = %+v, want confirmed deletion for n1", secondItems)
	}
	if cursorKnowsDocument(t, confirmedCursor, "n1") {
		t.Fatal("confirmed deletion remained in document revision cursor")
	}
}

func TestIncrementalDirectSelectionConfirmsVerifiedMissingNode(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	doc := node{
		NodeID: "direct-doc", WorkspaceID: "ws1", Name: "Direct",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
		DocKey: "direct-key", ModifiedTime: "1000",
	}
	f.children["root"] = []node{doc}
	f.details[doc.NodeID] = doc
	f.docs[doc.DocKey] = []block{textBlock("directly selected content")}

	resourceID := makeNodeResourceID("ws1", []string{doc.NodeID})
	connector := f.connector()
	config := f.dsConfig(resourceID)
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial direct selection sync: %v", err)
	}
	if !cursorKnowsDocument(t, baseline, doc.NodeID) {
		t.Fatal("baseline cursor did not retain the directly selected document")
	}

	delete(f.details, doc.NodeID)
	f.children["root"] = nil
	firstItems, firstMissingCursor, err := connector.FetchIncremental(
		context.Background(), config, baseline,
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("first verified missing snapshot error = %v, want PartialFetchError", err)
	}
	for _, item := range firstItems {
		if item.IsDeleted {
			t.Fatal("first verified missing snapshot must not emit a deletion")
		}
	}
	if !cursorKnowsDocument(t, firstMissingCursor, doc.NodeID) {
		t.Fatal("first verified missing snapshot dropped its previous revision")
	}

	secondItems, confirmedCursor, err := connector.FetchIncremental(
		context.Background(), config, firstMissingCursor,
	)
	if err != nil {
		t.Fatalf("second verified missing snapshot: %v", err)
	}
	if len(secondItems) != 1 ||
		!secondItems[0].IsDeleted ||
		secondItems[0].ExternalID != doc.NodeID {
		t.Fatalf("confirmed direct-selection deletion items = %+v", secondItems)
	}
	if cursorKnowsDocument(t, confirmedCursor, doc.NodeID) {
		t.Fatal("confirmed direct-selection deletion remained in the cursor")
	}
}

func TestIncrementalRawSelectionsConfirmVerifiedDisappearance(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		documentID string
		setup      func(*fakeDingTalk)
		remove     func(*fakeDingTalk)
	}{
		{
			name:       "workspace",
			resourceID: "ws1",
			documentID: "workspace-doc",
			setup: func(f *fakeDingTalk) {
				f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
				f.children["root"] = []node{{
					NodeID: "workspace-doc", WorkspaceID: "ws1", Name: "Workspace doc",
					Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
				}}
				f.docs["workspace-doc"] = []block{textBlock("workspace content")}
			},
			remove: func(f *fakeDingTalk) {
				f.workspaces = nil
			},
		},
		{
			name:       "legacy raw node",
			resourceID: "legacy-raw-doc",
			documentID: "legacy-raw-doc",
			setup: func(f *fakeDingTalk) {
				f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
				f.details["legacy-raw-doc"] = node{
					NodeID: "legacy-raw-doc", WorkspaceID: "ws1", Name: "Legacy raw doc",
					Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
				}
				f.docs["legacy-raw-doc"] = []block{textBlock("legacy content")}
			},
			remove: func(f *fakeDingTalk) {
				delete(f.details, "legacy-raw-doc")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetTokenCacheForTest()
			f := newFakeDingTalk(t)
			tt.setup(f)
			connector := f.connector()
			config := f.dsConfig(tt.resourceID)

			_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
			if err != nil {
				t.Fatalf("initial raw selection sync: %v", err)
			}
			if !cursorKnowsDocument(t, baseline, tt.documentID) {
				t.Fatal("baseline cursor did not retain the selected document")
			}

			tt.remove(f)
			firstItems, firstMissingCursor, err := connector.FetchIncremental(
				context.Background(),
				config,
				baseline,
			)
			var partial *datasource.PartialFetchError
			if !errors.As(err, &partial) {
				t.Fatalf("first verified disappearance error = %v, want PartialFetchError", err)
			}
			for _, item := range firstItems {
				if item.IsDeleted {
					t.Fatalf("first verified disappearance emitted deletion: %+v", item)
				}
			}
			firstState := decodeSyncState(firstMissingCursor)
			if got := firstState.Scopes[tt.resourceID].EmptySnapshotCount; got != 1 {
				t.Fatalf("first empty snapshot count = %d, want 1", got)
			}
			if !cursorKnowsDocument(t, firstMissingCursor, tt.documentID) {
				t.Fatal("first verified disappearance dropped the previous revision")
			}

			secondItems, confirmedCursor, err := connector.FetchIncremental(
				context.Background(),
				config,
				firstMissingCursor,
			)
			if err != nil {
				t.Fatalf("second verified disappearance: %v", err)
			}
			if len(secondItems) != 1 ||
				!secondItems[0].IsDeleted ||
				secondItems[0].ExternalID != tt.documentID {
				t.Fatalf("confirmed raw-selection deletion items = %+v", secondItems)
			}
			if cursorKnowsDocument(t, confirmedCursor, tt.documentID) {
				t.Fatal("confirmed raw-selection deletion remained in the cursor")
			}
		})
	}
}

func TestIncrementalDirectSelectionClassificationDriftWithChildrenPreservesHistoryAndDescends(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	parent := node{
		NodeID: "direct-parent", WorkspaceID: "ws1", Name: "Direct parent",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
		ModifiedTime: "1000", HasChildren: true,
	}
	child := node{
		NodeID: "direct-child", WorkspaceID: "ws1", Name: "Direct child",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}
	f.details[parent.NodeID] = parent
	f.children[parent.NodeID] = []node{child}
	f.docs[parent.NodeID] = []block{textBlock("previous direct parent content")}
	f.docs[child.NodeID] = []block{textBlock("previous direct child content")}

	resourceID := makeNodeResourceID("ws1", []string{parent.NodeID})
	connector := f.connector()
	config := f.dsConfig(resourceID)
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial direct selection sync: %v", err)
	}

	// The selected parent still resolves and has children, but DingTalk omits
	// every online-document classification hint in the detail response.
	f.details[parent.NodeID] = node{
		NodeID: parent.NodeID, WorkspaceID: "ws1", Name: parent.Name,
		Type: "FILE", ModifiedTime: "2000", HasChildren: true,
	}
	f.children[parent.NodeID][0].ModifiedTime = "2000"
	f.docs[child.NodeID] = []block{textBlock("updated direct child content")}

	items, next, err := connector.FetchIncremental(context.Background(), config, baseline)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("direct-selection classification drift error = %v, want PartialFetchError", err)
	}

	var updatedChild bool
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("direct-selection classification drift emitted deletion: %+v", item)
		}
		if item.ExternalID == child.NodeID &&
			strings.Contains(string(item.Content), "updated direct child content") {
			updatedChild = true
		}
	}
	if !updatedChild {
		t.Fatalf("direct-selection classification drift skipped updated child: %+v", items)
	}
	state := decodeSyncState(next)
	if got := state.DocRevisions[parent.NodeID]; got != "1000" {
		t.Fatalf("direct parent revision = %q, want retained revision 1000", got)
	}
	if got := state.DocRevisions[child.NodeID]; got != "2000" {
		t.Fatalf("direct child revision = %q, want 2000", got)
	}
}

func TestIncrementalIncompleteSnapshotResetsEmptyConfirmation(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.docs["n1"] = []block{textBlock("to be removed")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root"] = nil
	_, firstEmptyCursor, err := connector.FetchIncremental(
		context.Background(), config, baseline,
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("first empty error = %v, want PartialFetchError", err)
	}

	f.failChildrenOf = "root"
	items, incompleteCursor, err := connector.FetchIncremental(
		context.Background(), config, firstEmptyCursor,
	)
	if !errors.As(err, &partial) {
		t.Fatalf("incomplete traversal error = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatal("incomplete traversal must not confirm a deletion")
		}
	}
	if !cursorKnowsDocument(t, incompleteCursor, "n1") {
		t.Fatal("incomplete traversal dropped the previous document revision")
	}

	f.failChildrenOf = ""
	items, restartedCursor, err := connector.FetchIncremental(
		context.Background(), config, incompleteCursor,
	)
	if !errors.As(err, &partial) {
		t.Fatalf("first healthy empty after interruption error = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatal("empty confirmation must restart after an incomplete traversal")
		}
	}

	items, _, err = connector.FetchIncremental(
		context.Background(), config, restartedCursor,
	)
	if err != nil {
		t.Fatalf("second consecutive healthy empty after interruption: %v", err)
	}
	if len(items) != 1 || !items[0].IsDeleted || items[0].ExternalID != "n1" {
		t.Fatalf("confirmed deletion items = %+v, want n1 after two new healthy empties", items)
	}
}

func TestIncrementalClassificationDriftPreservesHistoryAsPartial(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000"},
	}
	f.docs["n1"] = []block{textBlock("previously imported")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", ModifiedTime: "2000"},
	}
	items, next, err := connector.FetchIncremental(context.Background(), config, baseline)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("classification drift error = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("classification drift emitted deletion: %+v", item)
		}
	}
	if !cursorKnowsDocument(t, next, "n1") {
		t.Fatal("classification drift dropped the prior document revision")
	}
}

func TestIncrementalClassificationDriftWithChildrenPreservesHistoryAndDescends(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Parent doc",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc",
		ModifiedTime: "1000", HasChildren: true,
	}}
	f.children["n1"] = []node{{
		NodeID: "child", WorkspaceID: "ws1", Name: "Child doc",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.docs["n1"] = []block{textBlock("previous parent content")}
	f.docs["child"] = []block{textBlock("previous child content")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	// DingTalk can temporarily omit the online-document classification fields
	// while still advertising children. The parent remains ambiguous, but its
	// subtree must still be traversed.
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Parent doc",
		Type: "FILE", ModifiedTime: "2000", HasChildren: true,
	}}
	f.children["n1"][0].ModifiedTime = "2000"
	f.docs["child"] = []block{textBlock("updated child content")}

	items, next, err := connector.FetchIncremental(context.Background(), config, baseline)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("classification drift with children error = %v, want PartialFetchError", err)
	}

	var updatedChild bool
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("classification drift with children emitted deletion: %+v", item)
		}
		if item.ExternalID == "child" &&
			strings.Contains(string(item.Content), "updated child content") {
			updatedChild = true
		}
	}
	if !updatedChild {
		t.Fatalf("classification drift stopped traversal before updated child: %+v", items)
	}
	if !cursorKnowsDocument(t, next, "n1") {
		t.Fatal("classification drift with children dropped the prior parent revision")
	}
	if !cursorKnowsDocument(t, next, "child") {
		t.Fatal("classification drift with children dropped the child revision")
	}
	state := decodeSyncState(next)
	if got := state.DocRevisions["n1"]; got != "1000" {
		t.Fatalf("classification drift advanced parent revision to %q, want retained revision 1000", got)
	}
	if got := state.DocRevisions["child"]; got != "2000" {
		t.Fatalf("updated child revision = %q, want 2000", got)
	}
}

func TestIncrementalCrossWorkspaceNodeIsSkippedAndPreservesHistory(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.docs["n1"] = []block{textBlock("trusted workspace")}

	connector := f.connector()
	config := f.dsConfig("ws1")
	_, baseline, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws2", Name: "Foreign Doc",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "2000",
	}}
	items, next, err := connector.FetchIncremental(context.Background(), config, baseline)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("cross-workspace response error = %v, want PartialFetchError", err)
	}
	if len(items) != 0 {
		t.Fatalf("cross-workspace response emitted items: %+v", items)
	}
	if !cursorKnowsDocument(t, next, "n1") {
		t.Fatal("cross-workspace response dropped the prior document revision")
	}
}

func TestIncrementalFailedResourceSwitchPreservesRemovedScope(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{
		{WorkspaceID: "ws-old", Name: "Old", RootNodeID: "root-old"},
		{WorkspaceID: "ws-new", Name: "New", RootNodeID: "root-new"},
	}
	f.children["root-old"] = []node{{
		NodeID: "old-doc", WorkspaceID: "ws-old", Name: "Old Doc",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.docs["old-doc"] = []block{textBlock("old content")}

	connector := f.connector()
	oldConfig := f.dsConfig("ws-old")
	_, baseline, err := connector.FetchIncremental(context.Background(), oldConfig, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}

	f.failChildrenOf = "root-new"
	newConfig := f.dsConfig("ws-new")
	items, next, err := connector.FetchIncremental(context.Background(), newConfig, baseline)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("failed resource switch error = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("failed resource switch deleted %q from the old scope", item.ExternalID)
		}
	}
	if !cursorKnowsDocument(t, next, "old-doc") {
		t.Fatal("failed resource switch dropped the previous scope from the cursor")
	}
}

func TestIncrementalCursorCannotCrossCredentialIdentity(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTimestamp: 1000,
	}}
	f.docs["n1"] = []block{textBlock("old identity content")}

	connector := f.connector()
	oldConfig := f.dsConfig("ws1")
	_, oldCursor, err := connector.FetchIncremental(context.Background(), oldConfig, nil)
	if err != nil {
		t.Fatalf("old identity FetchIncremental: %v", err)
	}

	newConfig := f.dsConfig("ws1")
	newConfig.Credentials["app_key"] = "candidate-identity-app-key"
	f.docs["n1"] = []block{textBlock("new identity content")}
	items, _, err := connector.FetchIncremental(context.Background(), newConfig, oldCursor)
	if err != nil {
		t.Fatalf("new identity FetchIncremental with late old cursor: %v", err)
	}
	if len(items) != 1 ||
		items[0].ExternalID != "n1" ||
		!strings.Contains(string(items[0].Content), "new identity content") {
		t.Fatalf("new identity reused the stale cursor instead of refetching: %+v", items)
	}
}

func TestIncrementalIdentitySwitchDeletesRowsOwnedOnlyByPreviousIdentity(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "old-doc", WorkspaceID: "ws1", Name: "Old",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTimestamp: 1000,
	}}
	f.docs["old-doc"] = []block{textBlock("old identity content")}
	connector := f.connector()
	oldConfig := f.dsConfig("ws1")
	_, oldCursor, err := connector.FetchIncremental(context.Background(), oldConfig, nil)
	if err != nil {
		t.Fatalf("old identity baseline: %v", err)
	}

	f.children["root"] = []node{{
		NodeID: "new-doc", WorkspaceID: "ws1", Name: "New",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTimestamp: 2000,
	}}
	f.docs["new-doc"] = []block{textBlock("new identity content")}
	newConfig := f.dsConfig("ws1")
	newConfig.Credentials["app_secret"] = "rotated-identity-secret"
	items, next, err := connector.FetchIncremental(context.Background(), newConfig, oldCursor)
	if err != nil {
		t.Fatalf("replacement identity sync: %v", err)
	}

	var sawNew, deletedOld bool
	for _, item := range items {
		if item.ExternalID == "new-doc" && !item.IsDeleted {
			sawNew = true
		}
		if item.ExternalID == "old-doc" && item.IsDeleted {
			deletedOld = true
		}
	}
	if !sawNew || !deletedOld {
		t.Fatalf("identity replacement items = %+v, want new-doc plus old-doc deletion", items)
	}
	if cursorKnowsDocument(t, next, "old-doc") {
		t.Fatal("previous identity document remained in the replacement cursor")
	}
}

func TestIncrementalIdentitySwitchPreservesOldRowsWhenReplacementBodyFails(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "old-doc", WorkspaceID: "ws1", Name: "Old",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTimestamp: 1000,
	}}
	f.docs["old-doc"] = []block{textBlock("old identity content")}
	connector := f.connector()
	oldConfig := f.dsConfig("ws1")
	_, oldCursor, err := connector.FetchIncremental(context.Background(), oldConfig, nil)
	if err != nil {
		t.Fatalf("old identity baseline: %v", err)
	}

	f.children["root"] = []node{{
		NodeID: "new-doc", WorkspaceID: "ws1", Name: "New",
		Type: "FILE", Category: "ALIDOC", Extension: "adoc", ModifiedTimestamp: 2000,
	}}
	f.failDocKey = "new-doc"
	newConfig := f.dsConfig("ws1")
	newConfig.Credentials["app_secret"] = "rotated-identity-secret"
	items, next, err := connector.FetchIncremental(context.Background(), newConfig, oldCursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("replacement body failure = %v, want PartialFetchError", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("failed replacement body deleted prior identity row: %+v", items)
		}
	}
	if !cursorKnowsDocument(t, next, "old-doc") {
		t.Fatal("failed replacement body dropped previous identity cleanup evidence")
	}
}

func TestIncrementalPartialFailureNeverEmitsFalseDeletions(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "folderA", WorkspaceID: "ws1", Name: "A", Type: "FOLDER", HasChildren: true},
		{NodeID: "folderB", WorkspaceID: "ws1", Name: "B", Type: "FOLDER", HasChildren: true},
	}
	f.children["folderA"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", DocKey: "d1", ModifiedTime: "1000"},
	}
	f.children["folderB"] = []node{
		{NodeID: "n2", WorkspaceID: "ws1", Name: "Doc2", Type: "FILE", DocKey: "d2", ModifiedTime: "2000"},
	}
	f.docs["d1"] = []block{textBlock("hello one")}
	f.docs["d2"] = []block{textBlock("hello two")}

	c := f.connector()
	cfg := f.dsConfig("ws1")

	// First a clean full sync so the cursor knows about both documents.
	items, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial FetchIncremental: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("first sync returned %d items, want 2", len(items))
	}
	if cursor == nil {
		t.Fatal("cursor must not be nil after a successful sync")
	}

	// Now folderB's listing breaks. Doc2 is unreachable, but it still exists.
	f.failChildrenOf = "folderB"

	items2, cursor2, err := c.FetchIncremental(context.Background(), cfg, cursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("degraded FetchIncremental error = %v, want PartialFetchError", err)
	}
	for _, it := range items2 {
		if it.IsDeleted {
			t.Fatalf("emitted deletion for %q after a partial listing failure; "+
				"unreachable is not deleted", it.ExternalID)
		}
	}
	if cursor2 == nil {
		t.Fatal("cursor must still advance for the subtrees that did succeed")
	}

	// The still-known document must survive in the new cursor, so that a later
	// healthy run does not mistake it for new-and-then-deleted.
	if !cursorKnowsDocument(t, cursor2, "n2") {
		t.Fatal("cursor dropped the unreachable document; the next successful " +
			"run would have no record of it and could mis-handle deletions")
	}
}

func TestDocumentFetchFailureIsVisibleAndPreservesRevision(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE",
		Category: "ALIDOC", Extension: "adoc", ModifiedTime: "1000",
	}}
	f.docs["n1"] = []block{textBlock("baseline")}

	c := f.connector()
	cfg := f.dsConfig("ws1")
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}

	f.children["root"][0].ModifiedTime = "2000"
	f.failDocKey = "n1"
	items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("failed document error = %v, want PartialFetchError", err)
	}
	if len(items) != 0 {
		t.Fatalf("failed document emitted items: %+v", items)
	}
	if !cursorKnowsDocument(t, next, "n1") {
		t.Fatal("failed document fetch dropped its prior revision")
	}
}

func TestFullFetchWithUnreachableSubtreeReturnsPartialError(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.failChildrenOf = "root"

	_, err := f.connector().FetchAll(context.Background(), f.dsConfig("ws1"), []string{"ws1"})
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchAll unreachable subtree error = %v, want PartialFetchError", err)
	}
}

// A genuine deletion, observed through a fully successful listing, must still
// be reported — the safety rule above must not disable deletion detection.
func TestIncrementalReportsDeletionWhenListingSucceeds(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", DocKey: "d1", ModifiedTime: "1000"},
		{NodeID: "n2", WorkspaceID: "ws1", Name: "Doc2", Type: "FILE", DocKey: "d2", ModifiedTime: "2000"},
	}
	f.docs["d1"] = []block{textBlock("one")}
	f.docs["d2"] = []block{textBlock("two")}

	c := f.connector()
	cfg := f.dsConfig("ws1")
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}

	// Doc2 is really gone now, and the listing is healthy.
	f.children["root"] = f.children["root"][:1]

	items, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	var deleted []string
	for _, it := range items {
		if it.IsDeleted {
			deleted = append(deleted, it.ExternalID)
		}
	}
	if len(deleted) != 1 || deleted[0] != "n2" {
		t.Fatalf("want exactly one deletion for n2, got %v", deleted)
	}
}

// Unchanged documents must not be re-downloaded on an incremental run.
func TestIncrementalSkipsUnchangedDocuments(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{NodeID: "n1", WorkspaceID: "ws1", Name: "Doc1", Type: "FILE", DocKey: "d1", ModifiedTime: "1000"},
	}
	f.docs["d1"] = []block{textBlock("stable")}

	c := f.connector()
	cfg := f.dsConfig("ws1")
	first, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first run returned %d items, want 1", len(first))
	}

	second, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("unchanged document was re-fetched: %d items", len(second))
	}
}

func TestIncrementalRefetchesRenameWithStableTimestamp(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID:       "n1",
		WorkspaceID:  "ws1",
		Name:         "Original name",
		Type:         "FILE",
		DocKey:       "d1",
		ModifiedTime: "1000",
	}}
	f.docs["d1"] = []block{textBlock("stable body")}

	c := f.connector()
	cfg := f.dsConfig("ws1")
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}

	f.children["root"][0].Name = "Renamed without timestamp change"
	items, next, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("renamed incremental: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Renamed without timestamp change" {
		t.Fatalf("renamed items = %+v", items)
	}

	stable, _, err := c.FetchIncremental(context.Background(), cfg, next)
	if err != nil {
		t.Fatalf("post-rename incremental: %v", err)
	}
	if len(stable) != 0 {
		t.Fatalf("stable renamed document re-fetched: %+v", stable)
	}
}

func TestIncrementalSeedsLegacyMetadataHashWithoutRefetch(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{{
		NodeID:       "n1",
		WorkspaceID:  "ws1",
		Name:         "Stable",
		Type:         "FILE",
		DocKey:       "d1",
		ModifiedTime: "1000",
	}}
	f.docs["d1"] = []block{textBlock("stable body")}

	c := f.connector()
	cfg := f.dsConfig("ws1")
	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	legacy := decodeSyncState(cursor)
	legacy.DocMetadataHashes = nil
	for resourceID, scope := range legacy.Scopes {
		scope.DocMetadataHashes = nil
		legacy.Scopes[resourceID] = scope
	}

	items, upgradedCursor, err := c.FetchIncremental(
		context.Background(),
		cfg,
		encodeSyncState(legacy),
	)
	if err != nil {
		t.Fatalf("legacy upgrade: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("legacy cursor caused a one-time refetch: %+v", items)
	}
	upgraded := decodeSyncState(upgradedCursor)
	if upgraded.DocMetadataHashes["n1"] == "" {
		t.Fatal("legacy cursor did not seed the current metadata hash")
	}
}

func TestUnsupportedNodeDoesNotEnterDocumentRevisionCursor(t *testing.T) {
	resetTokenCacheForTest()
	f := newFakeDingTalk(t)
	f.workspaces = []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root"}}
	f.children["root"] = []node{
		{
			NodeID:       "binary-1",
			WorkspaceID:  "ws1",
			Name:         "Archive.zip",
			Type:         "FILE",
			Category:     "BINARY",
			Extension:    "zip",
			ModifiedTime: "2026-07-28T11:12:13Z",
		},
	}

	items, cursor, err := f.connector().FetchIncremental(
		context.Background(),
		f.dsConfig("ws1"),
		nil,
	)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("unsupported node emitted items: %+v", items)
	}
	if cursorKnowsDocument(t, cursor, "binary-1") {
		t.Fatal("unsupported node was written into the document revision cursor")
	}
}

// cursorKnowsDocument reports whether the connector cursor still tracks the node.
func cursorKnowsDocument(t *testing.T, cursor *types.SyncCursor, nodeID string) bool {
	t.Helper()
	if cursor == nil || cursor.ConnectorCursor == nil {
		return false
	}
	var st syncState
	b, _ := json.Marshal(cursor.ConnectorCursor)
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	_, ok := st.DocRevisions[nodeID]
	return ok
}
