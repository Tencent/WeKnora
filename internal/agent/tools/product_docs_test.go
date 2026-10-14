package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Tencent/WeKnora/internal/productdocs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDocsLibrary(t *testing.T) *productdocs.Library {
	t.Helper()
	long := "# 配置详解\n\n开头。\n\n## 很长的一节\n\n" + strings.Repeat("长内容。", maxDocReadRunes) +
		"\n\n### 子节\n\n子节内容。\n\n## 模型与推理\n\nOLLAMA_BASE_URL 指向 Ollama，例如 `http://a?x=1&y=<2>`。\n"
	lib, err := productdocs.Load(fstest.MapFS{
		"03-features/14-wiki.md": {Data: []byte("# Wiki 能力\n\n简介。\n\n## 开启 Wiki {#怎么开启}\n\n" +
			"编辑知识库打开 Wiki，见[配置](../01-getting-started/04-configuration.md#模型与推理)。\n\n" +
			"### 抽取粒度\n\n选择 focused。\n\n## 常见问题\n\n无。\n")},
		"01-getting-started/04-configuration.md": {Data: []byte(long)},
	})
	require.NoError(t, err)
	return lib
}

func decodeDocsOutput(t *testing.T, output string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &out))
	return out
}

func TestSearchWeKnoraDocsSchemaListsTheCategories(t *testing.T) {
	var schema struct {
		Properties struct {
			Category struct {
				Enum []string `json:"enum"`
			} `json:"category"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(searchWeKnoraDocsSchema(), &schema))
	require.Len(t, schema.Properties.Category.Enum, len(productdocs.Categories))
	for i, c := range productdocs.Categories {
		assert.Equal(t, c.Dir, schema.Properties.Category.Enum[i])
	}
}

func TestSearchWeKnoraDocsListsAndSearches(t *testing.T) {
	tool := NewSearchWeKnoraDocsTool(testDocsLibrary(t))
	ctx := context.Background()

	res, err := tool.Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	pages := decodeDocsOutput(t, res.Output)["pages"].([]any)
	assert.Len(t, pages, 2)

	res, err = tool.Execute(ctx, json.RawMessage(`{"category":"03-features"}`))
	require.NoError(t, err)
	pages = decodeDocsOutput(t, res.Output)["pages"].([]any)
	require.Len(t, pages, 1)
	assert.Equal(t, productdocs.PublicBaseURL+"03-features/14-wiki", pages[0].(map[string]any)["url"])

	res, err = tool.Execute(ctx, json.RawMessage(`{"query":"Wiki 抽取粒度","limit":1}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	results := decodeDocsOutput(t, res.Output)["results"].([]any)
	require.Len(t, results, 1)
	hit := results[0].(map[string]any)
	assert.Equal(t, "03-features/14-wiki", hit["path"])
	assert.Equal(t, "抽取粒度", hit["anchor"])
	assert.Equal(t, "Wiki 能力 › 开启 Wiki › 抽取粒度", hit["section"])
	assert.Equal(t, productdocs.PublicBaseURL+"03-features/14-wiki#抽取粒度", hit["url"])

	res, err = tool.Execute(ctx, json.RawMessage(`{"query":"不存在的词汇组合"}`))
	require.NoError(t, err)
	assert.Contains(t, decodeDocsOutput(t, res.Output), "hint")

	res, err = tool.Execute(ctx, json.RawMessage(`{"query":"wiki","category":"../secrets"}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
}

func TestReadWeKnoraDocReturnsSectionsWithPublicLinks(t *testing.T) {
	tool := NewReadWeKnoraDocTool(testDocsLibrary(t))
	ctx := context.Background()

	res, err := tool.Execute(ctx, json.RawMessage(`{"path":"03-features/14-wiki","anchor":"#怎么开启"}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	out := decodeDocsOutput(t, res.Output)
	content := out["content"].(string)
	assert.True(t, strings.HasPrefix(content, "## 开启 Wiki"))
	assert.Contains(t, content, "### 抽取粒度", "a section is read with its subsections")
	assert.NotContains(t, content, "常见问题")
	assert.Contains(t, content, "[配置]("+productdocs.PublicBaseURL+"01-getting-started/04-configuration#模型与推理)")
	assert.Equal(t, productdocs.PublicBaseURL+"03-features/14-wiki#怎么开启", out["url"])

	// A url with a fragment selects the section, as search results give it.
	res, err = tool.Execute(ctx,
		json.RawMessage(`{"path":"`+productdocs.PublicBaseURL+`01-getting-started/04-configuration#模型与推理"}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Contains(t, res.Output, "`http://a?x=1&y=<2>`", "Markdown is not HTML-escaped")

	res, err = tool.Execute(ctx, json.RawMessage(`{"path":"03-features/14-wiki"}`))
	require.NoError(t, err)
	out = decodeDocsOutput(t, res.Output)
	assert.Contains(t, out["content"], "## 常见问题", "a short page is returned whole")
	assert.NotContains(t, out, "outline")
}

func TestReadWeKnoraDocBoundsLongPagesAndSections(t *testing.T) {
	tool := NewReadWeKnoraDocTool(testDocsLibrary(t))
	ctx := context.Background()

	res, err := tool.Execute(ctx, json.RawMessage(`{"path":"01-getting-started/04-configuration"}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	out := decodeDocsOutput(t, res.Output)
	assert.Equal(t, "开头。", out["content"], "a long page returns its lead and outline only")
	outline := out["outline"].([]any)
	require.Len(t, outline, 3)
	assert.Equal(t, "很长的一节", outline[0].(map[string]any)["anchor"])

	res, err = tool.Execute(ctx, json.RawMessage(`{"path":"01-getting-started/04-configuration","anchor":"很长的一节"}`))
	require.NoError(t, err)
	out = decodeDocsOutput(t, res.Output)
	assert.Equal(t, true, out["truncated"])
	assert.LessOrEqual(t, len([]rune(out["content"].(string))), maxDocReadRunes)
	assert.Len(t, out["outline"].([]any), 2, "the outline lists the cut section's own subsections")
}

func TestReadWeKnoraDocRejectsUnknownTargets(t *testing.T) {
	tool := NewReadWeKnoraDocTool(testDocsLibrary(t))
	ctx := context.Background()

	for _, path := range []string{"03-features/missing", "../../etc/passwd", ""} {
		res, err := tool.Execute(ctx, json.RawMessage(`{"path":"`+path+`"}`))
		require.NoError(t, err)
		assert.False(t, res.Success, path)
	}

	res, err := tool.Execute(ctx, json.RawMessage(`{"path":"03-features/14-wiki","anchor":"nope"}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Output, "怎么开启", "an unknown anchor returns the outline to pick from")
}
