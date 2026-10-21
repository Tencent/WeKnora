package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecursiveLinkDiscovery(t *testing.T) {
	for _, raw := range []string{
		"http://example.feishu.cn/base/a", "https://evil.test/base/a", "https://example.feishu.cn.evil.test/base/a",
		"https://user@example.feishu.cn/base/a", "https://example.feishu.cn:444/base/a",
	} {
		_, ok := ParseLinkedResourceURL(raw)
		require.False(t, ok, raw)
	}
	const baseURL = "https://example.feishu.cn/base/base-a?table=table-a&view=view-a"
	links := DiscoverLinkedResources([]map[string]any{
		{"link": map[string]any{"url": baseURL}},
		{"text": baseURL},
		{"link": "https://example.larksuite.com/wiki/node-a"},
	})
	require.Len(t, links, 2)
	require.Equal(t, "table-a", links[0].TableID)
	require.Equal(t, "view-a", links[0].ViewID)
	require.Equal(t, "wiki", links[1].Type)
}

func TestRecursiveDepthCycleAndPermissionFailure(t *testing.T) {
	calls := 0
	denied := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"code": 0}
		switch {
		case strings.Contains(r.URL.Path, "/auth/"):
			response["tenant_access_token"] = "fixture-only-token"
			response["expire"] = 7200
		case strings.Contains(r.URL.Path, "/wiki/v2/spaces/get_node"):
			response["data"] = map[string]any{"node": map[string]string{
				"obj_type": "bitable", "obj_token": "base-a", "title": "resolved table",
			}}
		case strings.HasSuffix(r.URL.Path, "/tables"):
			if denied {
				response["code"] = 99991672
				response["msg"] = "fixture permission denied"
				break
			}
			response["data"] = map[string]any{"items": []map[string]string{{"table_id": "table-a"}}, "has_more": false}
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			target := "base-b"
			if strings.Contains(r.URL.Path, "base-b") {
				target = "base-a"
			}
			response["data"] = map[string]any{
				"items": []any{map[string]any{
					"fields": map[string]any{"link": "https://example.feishu.cn/base/" + target},
				}}, "has_more": false,
			}
		default:
			response["code"] = 106999
			response["msg"] = "export deliberately unavailable in this fixture"
		}
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()
	client := &Client{
		baseURL: server.URL, appID: "fixture-app", appSecret: "fixture-secret", httpClient: server.Client(),
	}
	root := LinkedResource{Type: "bitable", Token: "base-a", URL: "https://example.feishu.cn/base/base-a"}
	for _, depth := range []int{1, 2, 3} {
		calls = 0
		state := NewRecursiveState()
		_, err := FetchRecursiveLinkedResources(context.Background(), client, []LinkedResource{root},
			RecursiveFetchOptions{MaxDepth: depth, State: state})
		require.NoError(t, err)
		require.Contains(t, state.Present, "recursive:bitable:base-a")
		if depth == 1 {
			require.Len(t, state.Present, 1)
		} else {
			require.Len(t, state.Present, 2)
		}
		require.Less(t, calls, 15, "cycles must terminate")
	}
	canonical := NewRecursiveState()
	_, err := FetchRecursiveLinkedResources(context.Background(), client, []LinkedResource{
		{Type: "wiki", Token: "wrapper", URL: "https://example.feishu.cn/wiki/wrapper"}, root,
	}, RecursiveFetchOptions{MaxDepth: 1, State: canonical})
	require.NoError(t, err)
	require.Len(t, canonical.Present, 1, "Wiki wrappers and direct URLs share the canonical identity")
	denied = true
	state := NewRecursiveState()
	state.LoadPrevious([]string{"recursive:docx:keep-me"})
	_, err = FetchRecursiveLinkedResources(context.Background(), client, []LinkedResource{root},
		RecursiveFetchOptions{MaxDepth: 3, State: state})
	require.NoError(t, err)
	require.True(t, state.Incomplete)
	state.PreservePreviousAndPresent()
	require.Contains(t, state.CursorIDs(), "recursive:docx:keep-me")
}

func TestRecursivePaginationRejectsRepeatedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/auth/") {
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "fixture-pagination-token", "expire": 7200,
			}))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "data": map[string]any{"has_more": true, "page_token": "repeated", "items": []any{}},
		}))
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	_, err := client.ListBitableTables(context.Background(), "fixture-base")
	require.ErrorContains(t, err, "pagination repeated")
	_, err = client.ReadBitableRawRecords(context.Background(), "fixture-base", "fixture-table", "")
	require.ErrorContains(t, err, "pagination repeated")
}

func TestRecursiveConfiguration(t *testing.T) {
	t.Setenv("FEISHU_RECURSIVE_LINKS", "")
	require.False(t, RecursiveLinksEnabled())
	t.Setenv("FEISHU_RECURSIVE_LINKS", "true")
	require.True(t, RecursiveLinksEnabled())
	t.Setenv("FEISHU_RECURSIVE_MAX_DEPTH", "999")
	require.Equal(t, 10, RecursiveMaxDepth())
	t.Setenv("FEISHU_RECURSIVE_MAX_DEPTH", "invalid")
	require.Equal(t, 3, RecursiveMaxDepth())
}
