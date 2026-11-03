package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiDriveShortcutsRemainInListing(t *testing.T) {
	// 复现 #5021：云盘文档的快捷方式使用空间 "0"，没有原 Wiki 节点。
	shortcuts := []WikiNode{
		{NodeToken: "shortcut-a", ObjToken: "doc-a", ObjType: "docx", NodeType: "shortcut", OriginSpaceID: "0"},
		{NodeToken: "shortcut-b", ObjToken: "doc-b", ObjType: "docx", NodeType: "shortcut", OriginSpaceID: "0"},
		{NodeToken: "shortcut-c", ObjToken: "doc-c", ObjType: "docx", NodeType: "shortcut", OriginSpaceID: "0"},
	}
	for _, root := range []string{"", "shortcut-a"} {
		t.Run("root="+root, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					writeJSON(w, TokenResponse{TenantAccessToken: "test-token", Expire: 7200})
				case "/open-apis/wiki/v2/spaces/space1/nodes":
					assert.Empty(t, r.URL.Query().Get("parent_node_token"), "云盘文档没有 Wiki 子节点")
					writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: shortcuts}})
				case "/open-apis/wiki/v2/spaces/get_node":
					assert.Equal(t, root, r.URL.Query().Get("token"), "仅选择单个快捷方式时读取根节点")
					writeJSON(w, WikiNodeInfoResponse{Data: WikiNodeInfoData{Node: shortcuts[0]}})
				default:
					t.Errorf("unexpected API request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			client := NewClient(&Config{AppID: "test-app", AppSecret: "test-secret", BaseURL: server.URL})
			nodes, err := client.ListWikiNodesWithShortcuts(context.Background(), "space1", root)
			require.NoError(t, err, "云盘快捷方式不能计入子文档展开失败")
			want := shortcuts
			if root != "" {
				want = shortcuts[:1]
			}
			require.Len(t, nodes, len(want))
			for index, node := range nodes {
				require.Equal(t, want[index].NodeToken, node.NodeToken)
				require.Equal(t, want[index].ObjToken, node.ObjToken, "正文 token 必须保留以供后续获取")
			}
		})
	}
}

func TestResolveWikiShortcutRejectsIncompleteWikiOrigins(t *testing.T) {
	cases := []struct {
		name string
		node WikiNode
	}{
		{"missing node", WikiNode{OriginSpaceID: "space2"}},
		{"missing space", WikiNode{OriginNodeID: "origin"}},
		{"missing both", WikiNode{}},
		{"drive space with children", WikiNode{OriginSpaceID: "0", HasChild: true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			test.node.NodeType = "shortcut"
			test.node.NodeToken = "shortcut"
			_, err := (&Client{}).resolveWikiShortcut(context.Background(), test.node, nil)
			require.ErrorContains(t, err, "is missing its original node or space")
		})
	}
}

func TestWikiShortcutChainCanEndAtDriveDocument(t *testing.T) {
	drive := WikiNode{
		SpaceID: "space2", NodeToken: "drive-shortcut", ObjToken: "doc", ObjType: "docx",
		NodeType: "shortcut", OriginSpaceID: "0",
	}
	alias := WikiNode{
		SpaceID: "space1", NodeToken: "alias", NodeType: "shortcut",
		OriginSpaceID: drive.SpaceID, OriginNodeID: drive.NodeToken,
	}
	resolved, err := (&Client{}).resolveWikiShortcut(context.Background(), alias, map[string]WikiNode{
		"space2:drive-shortcut": drive,
	})
	require.NoError(t, err)
	require.Equal(t, drive, resolved, "间接引用云盘文档也应结束遍历并保留正文")
}

func TestWikiShortcutExpansionPreservesWikiFailuresAndChildren(t *testing.T) {
	for _, mode := range []string{"children", "permission", "cycle", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			shortcut := WikiNode{
				SpaceID: "space1", NodeToken: "shortcut", NodeType: "shortcut",
				OriginSpaceID: "space2", OriginNodeID: "origin",
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					writeJSON(w, TokenResponse{TenantAccessToken: "test-token", Expire: 7200})
				case "/open-apis/wiki/v2/spaces/get_node":
					assert.Equal(t, "origin", r.URL.Query().Get("token"))
					if mode == "permission" {
						w.WriteHeader(http.StatusForbidden)
						writeJSON(w, ApiResponse{Code: 131006, Msg: "permission denied"})
						return
					}
					origin := WikiNode{SpaceID: "space2", NodeToken: "origin", NodeType: "origin", HasChild: true}
					if mode == "cycle" {
						origin.NodeType = "shortcut"
						origin.OriginSpaceID = shortcut.SpaceID
						origin.OriginNodeID = shortcut.NodeToken
					}
					writeJSON(w, WikiNodeInfoResponse{Data: WikiNodeInfoData{Node: origin}})
				case "/open-apis/wiki/v2/spaces/space2/nodes":
					assert.Equal(t, "origin", r.URL.Query().Get("parent_node_token"))
					writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{Items: []WikiNode{
						{NodeToken: "child", NodeType: "origin", ObjToken: "child-doc", ObjType: "docx"},
					}}})
				default:
					t.Errorf("unexpected API request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			client := NewClient(&Config{AppID: "test-app", AppSecret: "test-secret", BaseURL: server.URL})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			nodes, err := client.expandWikiShortcuts(ctx, "space1", []WikiNode{shortcut}, nil)
			if mode == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			if mode != "children" {
				var partial *PartialWikiNodeListError
				require.ErrorAs(t, err, &partial, "真正的 Wiki 解析失败必须继续报告")
				require.Len(t, partial.Failures, 1)
				require.Equal(t, shortcut.NodeToken, partial.Failures[0].Node.NodeToken)
				require.ErrorContains(t, err, map[string]string{"permission": "403", "cycle": "cycle"}[mode])
				return
			}
			require.NoError(t, err)
			require.Len(t, nodes, 2)
			require.Equal(t, "child", nodes[1].NodeToken)
			require.Equal(t, "space2", nodes[1].SpaceID)
			require.Equal(t, "shortcut", nodes[1].ParentNodeID, "跨空间子节点仍映射到所选快捷方式下")
		})
	}
}
