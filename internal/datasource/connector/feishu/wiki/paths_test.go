package wiki

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// ──────────────────────────────────────────────────────────────────────
// P1 directory mapping tests: path-qualified FileName, shortcut subtree
// dedup, move/rename following, anchored against the fake wiki tree below.
// ──────────────────────────────────────────────────────────────────────

// fakeWikiTree serves a mutable wiki hierarchy: the test can rewrite the tree
// between sync runs to model moves/renames. All nodes are "file" obj_type so
// fetchDriveFile uses the node title verbatim as the base file name — the
// assertions then exercise only the path prefix logic under test.
type fakeWikiTree struct {
	top      []core.WikiNode
	children map[string][]core.WikiNode
	ts       *httptest.Server
	cfg      *core.Config
}

func newFakeWikiTree(t *testing.T, top []core.WikiNode, children map[string][]core.WikiNode) *fakeWikiTree {
	t.Helper()
	f := &fakeWikiTree{top: top, children: children}

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.TokenResponse{ApiResponse: core.ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.WikiSpaceListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiSpaceListData{Items: []core.WikiSpace{{SpaceID: "space1", Name: "Test Space"}}},
		})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentToken := r.URL.Query().Get("parent_node_token")
		nodes := f.top
		if parentToken != "" {
			nodes = f.children[parentToken]
			for i := range nodes {
				if nodes[i].ParentNodeID == "" {
					nodes[i].ParentNodeID = parentToken
				}
				if nodes[i].SpaceID == "" {
					nodes[i].SpaceID = "space1"
				}
			}
		}
		writeJSON(w, core.WikiNodeListResponse{ApiResponse: core.ApiResponse{Code: 0}, Data: core.WikiNodeListData{Items: nodes}})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		for _, n := range f.top {
			if n.NodeToken == token {
				writeJSON(w, core.WikiNodeInfoResponse{ApiResponse: core.ApiResponse{Code: 0}, Data: core.WikiNodeInfoData{Node: n}})
				return
			}
		}
		for _, group := range f.children {
			for _, n := range group {
				if n.NodeToken == token {
					writeJSON(w, core.WikiNodeInfoResponse{ApiResponse: core.ApiResponse{Code: 0}, Data: core.WikiNodeInfoData{Node: n}})
					return
				}
			}
		}
		writeJSON(w, core.WikiNodeInfoResponse{ApiResponse: core.ApiResponse{Code: 1663, Msg: "node not found"}})
	})
	// Drive download for "file" type nodes.
	mux.HandleFunc("/open-apis/drive/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("fake-file-content"))
			return
		}
		http.NotFound(w, r)
	})

	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	f.cfg = &core.Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: f.ts.URL}
	return f
}

func fileNode(token, title string, opts ...func(*core.WikiNode)) core.WikiNode {
	n := core.WikiNode{
		NodeToken:    token,
		ObjToken:     "obj-" + token,
		ObjType:      "file",
		NodeType:     "origin",
		Title:        title,
		NodeEditTime: "100",
	}
	for _, opt := range opts {
		opt(&n)
	}
	return n
}

// TestWikiFetchAll_PathQualifiedMultiLevel anchors the multi-level directory
// mapping: <space名>/<目录A>/<目录B>/<文档名>, Chinese directory names kept as-is
// and "/" inside a directory name sanitised to "_" so it cannot fake an extra
// nesting level.
func TestWikiFetchAll_PathQualifiedMultiLevel(t *testing.T) {
	f := newFakeWikiTree(t,
		[]core.WikiNode{
			fileNode("nt-dirA", "产品文档", func(n *core.WikiNode) { n.HasChild = true }),
			fileNode("nt-top", "顶层.pdf"),
		},
		map[string][]core.WikiNode{
			"nt-dirA": {
				fileNode("nt-dirB", "子/目录", func(n *core.WikiNode) { n.HasChild = true }),
			},
			"nt-dirB": {
				fileNode("nt-leaf", "规格说明.pdf"),
			},
		},
	)

	c := NewConnector(core.RegionFeishu)
	items, err := c.FetchAll(context.Background(), makeConfig(f.cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	want := map[string]string{
		// The selected space IS the KB root: a top-level item's prefix is
		// empty (bare file name), deeper items carry only the directory path.
		// Top-level directory node keeps its raw title as base name (existing
		// file-node naming rule, unchanged).
		"nt-dirA": "产品文档",
		"nt-top":  "顶层.pdf",
		// Its own base name keeps the raw title; the "/" sanitisation of the
		// DIRECTORY segment shows up in nt-leaf's path below.
		"nt-dirB": "产品文档/子/目录",
		"nt-leaf": "产品文档/子_目录/规格说明.pdf",
	}
	got := make(map[string]string, len(items))
	for _, item := range items {
		got[item.ExternalID] = item.FileName
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items (%v), want %d", len(got), got, len(want))
	}
	for id, wantPath := range want {
		if got[id] != wantPath {
			t.Errorf("FileName for %s = %q, want %q", id, got[id], wantPath)
		}
	}
}

// TestWikiFetchAll_ShortcutSubtreeDeduped anchors the shortcut dedup: a
// shortcut node and its whole subtree are skipped — the origin entity is
// enumerated at its real location, so the shortcut would duplicate it.
func TestWikiFetchAll_ShortcutSubtreeDeduped(t *testing.T) {
	origin := fileNode("nt-origin", "origin.pdf")
	shortcut := fileNode("nt-shortcut", "origin.pdf", func(n *core.WikiNode) {
		n.NodeType = "shortcut"
		n.OriginNodeID = "nt-origin"
		n.HasChild = true
	})
	f := newFakeWikiTree(t,
		[]core.WikiNode{origin, shortcut},
		map[string][]core.WikiNode{
			// The client's recursive walk descends into the shortcut (HasChild),
			// so the listing contains the origin's subtree a second time.
			"nt-shortcut": {fileNode("nt-under-shortcut", "duplicate.pdf")},
		},
	)

	c := NewConnector(core.RegionFeishu)
	items, cursor, err := c.FetchIncremental(context.Background(), makeConfig(f.cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected exactly 1 item (the origin), got %d: %+v", len(items), items)
	}
	if items[0].ExternalID != "nt-origin" {
		t.Errorf("item ExternalID = %q, want nt-origin", items[0].ExternalID)
	}

	// The cursor must only record the kept node, so the filtered shortcut and
	// its subtree do not linger as ghosts.
	var fc core.FeishuCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	times := fc.SpaceNodeTimes["space1"]
	if _, ok := times["nt-shortcut"]; ok {
		t.Error("cursor records the skipped shortcut node")
	}
	if _, ok := times["nt-under-shortcut"]; ok {
		t.Error("cursor records a node under the skipped shortcut subtree")
	}
	if _, ok := times["nt-origin"]; !ok {
		t.Error("cursor missing the origin node")
	}
}

// TestWikiFetchIncremental_MoveFollowsNewPath anchors the move/rename story:
// the external_id is unchanged, and once the node's edit time moves the next
// sync re-fetches it under the NEW directory path. Ingestion's
// update=delete+recreate then lands the knowledge row in the new folder_path.
func TestWikiFetchIncremental_MoveFollowsNewPath(t *testing.T) {
	doc := fileNode("nt-doc", "文档.pdf", func(n *core.WikiNode) { n.NodeEditTime = "100" })
	f := newFakeWikiTree(t,
		[]core.WikiNode{
			fileNode("nt-dirA", "目录A", func(n *core.WikiNode) { n.HasChild = true }),
		},
		map[string][]core.WikiNode{"nt-dirA": {doc}},
	)

	c := NewConnector(core.RegionFeishu)
	cfg := makeConfig(f.cfg, []string{"space1"})

	items1, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if len(items1) != 2 {
		t.Fatalf("first sync: expected 2 items (dir + doc), got %d: %+v", len(items1), items1)
	}
	var docItem *types.FetchedItem
	for i := range items1 {
		if items1[i].ExternalID == "nt-doc" {
			docItem = &items1[i]
		}
	}
	if docItem == nil || docItem.FileName != "目录A/文档.pdf" {
		t.Fatalf("first sync: doc item missing or wrong path: %+v", items1)
	}

	// Move the doc from 目录A to a new top-level 目录B; the edit time moves so
	// the incremental fast-path does not skip it.
	f.top = append(f.top, fileNode("nt-dirB", "目录B", func(n *core.WikiNode) { n.HasChild = true }))
	f.children = map[string][]core.WikiNode{
		"nt-dirB": {fileNode("nt-doc", "文档.pdf", func(n *core.WikiNode) { n.NodeEditTime = "200" })},
	}

	items2, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}

	var moved *types.FetchedItem
	for i := range items2 {
		if items2[i].ExternalID == "nt-doc" && !items2[i].IsDeleted {
			moved = &items2[i]
		}
	}
	if moved == nil {
		t.Fatalf("moved doc not re-fetched on second sync: %+v", items2)
	}
	if moved.FileName != "目录B/文档.pdf" {
		t.Errorf("moved doc FileName = %q, want %q (same external_id, new folder)", moved.FileName, "目录B/文档.pdf")
	}
}
