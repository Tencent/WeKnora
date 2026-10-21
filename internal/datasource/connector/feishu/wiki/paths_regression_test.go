package wiki

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiShortcutOutsideSelectionRemainsSyncable(t *testing.T) {
	for _, resource := range []string{"space1", "space1:shortcut"} {
		t.Run(resource, func(t *testing.T) {
			shortcut := fileNode("shortcut", "reference.pdf", func(n *core.WikiNode) {
				n.NodeType = "shortcut"
				n.OriginNodeID = "outside"
				n.OriginSpaceID = "space2"
				n.HasChild = true
			})
			f := newFakeWikiTree(t, []core.WikiNode{shortcut}, map[string][]core.WikiNode{
				"shortcut": {fileNode("child", "child.pdf")},
			})
			conn := NewConnector(core.RegionFeishu)
			cfg := makeConfig(f.cfg, []string{resource})
			items, cursor, err := conn.FetchIncremental(context.Background(), cfg, nil)
			require.NoError(t, err)
			require.Len(t, items, 2)
			require.Equal(t, "shortcut", items[0].ExternalID)
			require.Equal(t, "Test Space/reference.pdf/child.pdf", items[1].FileName)
			items, _, err = conn.FetchIncremental(context.Background(), cfg, cursor)
			require.NoError(t, err)
			require.Empty(t, items, "unchanged shortcuts must not be deleted or fetched again")
		})
	}
}

func TestWikiShortcutDedupRequiresIndependentOrigin(t *testing.T) {
	cases := []struct {
		name  string
		nodes []core.WikiNode
		want  []string
	}{
		{"original in same listing", []core.WikiNode{
			{NodeToken: "original", NodeType: "origin"},
			{NodeToken: "shortcut", NodeType: "shortcut", OriginNodeID: "original"},
			{NodeToken: "child", ParentNodeID: "shortcut"},
		}, []string{"original"}},
		{"original outside selected subtree", []core.WikiNode{
			{NodeToken: "shortcut", NodeType: "shortcut", OriginNodeID: "outside"},
			{NodeToken: "child", ParentNodeID: "shortcut"},
		}, []string{"shortcut", "child"}},
		{"origin exists only below shortcut", []core.WikiNode{
			{NodeToken: "shortcut", NodeType: "shortcut", OriginNodeID: "original"},
			{NodeToken: "original", ParentNodeID: "shortcut", NodeType: "origin"},
		}, []string{"shortcut", "original"}},
		{"mismatched origin space", []core.WikiNode{
			{NodeToken: "original", NodeType: "origin", SpaceID: "space1"},
			{NodeToken: "shortcut", NodeType: "shortcut", OriginNodeID: "original", OriginSpaceID: "space2"},
		}, []string{"original", "shortcut"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var ids []string
			for _, n := range filterShortcutSubtrees(tt.nodes) {
				ids = append(ids, n.NodeToken)
			}
			require.Equal(t, tt.want, ids)
		})
	}
}

func TestWikiAncestorChangesRefreshDescendants(t *testing.T) {
	for _, change := range []string{"rename", "move"} {
		t.Run(change, func(t *testing.T) {
			parent := fileNode("parent", "Old", func(n *core.WikiNode) { n.HasChild = true })
			f := newFakeWikiTree(t, []core.WikiNode{parent}, map[string][]core.WikiNode{
				"parent": {fileNode("child", "child.pdf")},
			})
			conn := NewConnector(core.RegionFeishu)
			cfg := makeConfig(f.cfg, []string{"space1"})
			_, cursor, err := conn.FetchIncremental(context.Background(), cfg, nil)
			require.NoError(t, err)
			want := "Test Space/New/child.pdf"
			if change == "rename" {
				f.top[0].Title = "New"
				f.top[0].NodeEditTime = "200"
			} else {
				parent.NodeEditTime = "200"
				f.top = []core.WikiNode{fileNode("new-parent", "New", func(n *core.WikiNode) { n.HasChild = true })}
				f.children["new-parent"] = []core.WikiNode{parent}
				want = "Test Space/New/Old/child.pdf"
			}
			items, next, err := conn.FetchIncremental(context.Background(), cfg, cursor)
			require.NoError(t, err)
			var child *types.FetchedItem
			for i := range items {
				if items[i].ExternalID == "child" {
					child = &items[i]
				}
			}
			require.NotNil(t, child, "unchanged child must follow its ancestor's path")
			require.Equal(t, want, child.FileName)
			require.EqualValues(t, 100, child.UpdatedAt.Unix(), "source timestamp must remain the document's own")
			items, _, err = conn.FetchIncremental(context.Background(), cfg, next)
			require.NoError(t, err)
			require.Empty(t, items)
		})
	}
}

func TestWikiLegacyTimestampCursorRefreshesOnce(t *testing.T) {
	f := newFakeWikiTree(t, []core.WikiNode{fileNode("doc", "doc.pdf")}, nil)
	conn := NewConnector(core.RegionFeishu)
	cfg := makeConfig(f.cfg, []string{"space1"})
	legacy := makeStreamCursor(t, map[string]map[string]string{"space1": {"doc": "100"}})
	items, cursor, err := conn.FetchIncremental(context.Background(), cfg, legacy)
	require.NoError(t, err)
	require.Len(t, items, 1)
	items, _, err = conn.FetchIncremental(context.Background(), cfg, cursor)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestWikiPartialListingKeepsAccessibleShortcut(t *testing.T) {
	origin := fileNode("origin", "origin.pdf", func(n *core.WikiNode) { n.HasChild = true })
	shortcut := fileNode("shortcut", "reference.pdf", func(n *core.WikiNode) {
		n.NodeType = "shortcut"
		n.OriginNodeID = "origin"
		n.HasChild = true
	})
	server, cfg := fakeFeishuHierarchy([]core.WikiNode{origin, shortcut}, map[string][]core.WikiNode{
		"shortcut": {fileNode("child", "child.pdf")},
	}, "origin")
	defer server.Close()
	conn := NewConnector(core.RegionFeishu)
	items, _, err := conn.FetchIncremental(context.Background(), makeConfig(cfg, []string{"space1"}), nil)
	require.NoError(t, err)
	var fetched []string
	for _, item := range items {
		if item.Metadata["error"] == "" {
			fetched = append(fetched, item.ExternalID)
		}
	}
	require.Contains(t, fetched, "shortcut")
	require.Contains(t, fetched, "child", "a failed origin subtree must not suppress the accessible copy")
}
