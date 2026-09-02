package weknora

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

func TestGetPageByIDResolvesUUIDThroughSlugRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			require.Equal(t, "100", request.URL.Query().Get("page_size"))
			_ = json.NewEncoder(writer).Encode(ListPagesResp{
				Pages:      []WikiPage{{ID: "page-1", Slug: "outline/video-1"}},
				Total:      1,
				Page:       1,
				PageSize:   100,
				TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(WikiPage{
				ID: "page-1", Slug: "outline/video-1", Content: "# Outline",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	page, err := client.GetPageByID(t.Context(), "kb-1", "page-1")
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Equal(t, "page-1", page.ID)
	require.Equal(t, "outline/video-1", page.Slug)
	require.Equal(t, "# Outline", page.Content)
}

func TestGetPageByIDFindsUUIDOnLaterPage(t *testing.T) {
	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			listCalls++
			page, err := strconv.Atoi(request.URL.Query().Get("page"))
			require.NoError(t, err)
			if page == 1 {
				pages := make([]WikiPage, maxWikiPageSize)
				for i := range pages {
					pages[i] = WikiPage{ID: "other-" + strconv.Itoa(i), Slug: "other/" + strconv.Itoa(i)}
				}
				_ = json.NewEncoder(writer).Encode(ListPagesResp{
					Pages: pages, Total: maxWikiPageSize + 1, Page: 1, PageSize: maxWikiPageSize, TotalPages: 2,
				})
				return
			}
			_ = json.NewEncoder(writer).Encode(ListPagesResp{
				Pages: []WikiPage{{ID: "page-2", Slug: "outline/video-2"}},
				Total: maxWikiPageSize + 1, Page: 2, PageSize: maxWikiPageSize, TotalPages: 2,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-2":
			_ = json.NewEncoder(writer).Encode(WikiPage{ID: "page-2", Slug: "outline/video-2", Content: "# Outline"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	page, err := client.GetPageByID(t.Context(), "kb-1", "page-2")
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Equal(t, "page-2", page.ID)
	require.Equal(t, 2, listCalls)
}

func TestGetPageByIDStopsForLegacyResponseWithoutPaginationMetadata(t *testing.T) {
	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		listCalls++
		_ = json.NewEncoder(writer).Encode(ListPagesResp{
			Pages: []WikiPage{{ID: "other", Slug: "other/page"}},
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client := NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	page, err := client.GetPageByID(ctx, "kb-1", "missing")
	require.NoError(t, err)
	require.Nil(t, page)
	require.Equal(t, 1, listCalls)
}

func TestParsedFrontmatterAcceptsMarkdownFence(t *testing.T) {
	page := WikiPage{Content: "```markdown\n---\ntype: person\nsource_video_id: video-1\n---\n# 张三"}
	require.Equal(t, "person", page.ParsedFrontmatter()["type"])
	require.Equal(t, "video-1", page.ParsedFrontmatter()["source_video_id"])
}

func TestListByVideoOwnedUsesExplicitOwnershipAndKnowledgeBaseLinks(t *testing.T) {
	videoID := "video-1"
	knowledgeBasePage := &WikiPage{
		Slug:    "video/" + videoID,
		Content: "知识索引：[[entity/linked]]",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "entity,concept,index", request.URL.Query().Get("page_type"))
		_ = json.NewEncoder(writer).Encode(ListPagesResp{
			Pages: []WikiPage{
				{ID: "owned", Slug: "entity/owned", PageType: "entity", Content: "---\nsource_video_id: " + videoID + "\n---\nowned"},
				{ID: "linked", Slug: "entity/linked", PageType: "entity", Content: "linked without frontmatter"},
				{ID: "content-owned", Slug: "concept/content-owned", PageType: "concept", Content: "来源视频 ID：" + videoID},
				{ID: "foreign", Slug: "summary/foreign", PageType: "summary", Content: "正文提及 " + videoID},
				{ID: "legacy", Slug: "video/" + videoID, PageType: "index", Content: "历史索引 " + videoID},
			},
			TotalPages: 1,
		})
	}))
	defer server.Close()

	client := NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	pages, err := client.ListByVideoOwned(t.Context(), "kb-1", videoID, "entity,concept,index", knowledgeBasePage)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"owned", "linked", "content-owned", "legacy"}, []string{pages[0].ID, pages[1].ID, pages[2].ID, pages[3].ID})
}
