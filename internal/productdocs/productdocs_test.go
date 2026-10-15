package productdocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	websitedocs "github.com/Tencent/WeKnora/website-docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const samplePage = "# Wiki 能力（Wiki）\n" +
	"\n" +
	"Wiki 将资料整理为页面，详见[观测](16-observability.md)与[本节](#抽取粒度)。\n" +
	"\n" +
	"<Screenshot\n" +
	"  src=\"/screenshots/wiki.png\"\n" +
	"  caption=\"Wiki 浏览器\"\n" +
	"  hint=\"不应出现\" />\n" +
	"\n" +
	"## 开启并浏览 Wiki {#怎么开启}\n" +
	"\n" +
	"1. 编辑知识库 → 打开 **Wiki**；见[配置](../01-getting-started/04-configuration.md#模型与推理)。\n" +
	"\n" +
	"```mermaid\n" +
	"graph TD\n" +
	"  A --> B\n" +
	"```\n" +
	"\n" +
	"```bash\n" +
	"# 不是标题\n" +
	"export WIKI_ENABLED=true\n" +
	"```\n" +
	"\n" +
	"### 4.2 队列（Asynq）：Worker 池\n" +
	"\n" +
	"队列内容。![拓扑](/screenshots/topology.png)\n" +
	"\n" +
	"#### 抽取粒度\n" +
	"\n" +
	"选择 `focused` 或 `exhaustive`。\n" +
	"\n" +
	"##### 更深的标题\n" +
	"\n" +
	"仍属于抽取粒度。\n" +
	"\n" +
	"## 常见问题\n" +
	"\n" +
	"## 常见问题\n" +
	"\n" +
	"第二个同名标题。\n"

func parseSample(t *testing.T) *Doc {
	t.Helper()
	doc, err := parseDoc("03-features/14-wiki.md", "功能模块", samplePage)
	require.NoError(t, err)
	return doc
}

func TestParseDocSplitsSectionsWithSiteAnchors(t *testing.T) {
	doc := parseSample(t)
	assert.Equal(t, "03-features/14-wiki", doc.Path)
	assert.Equal(t, "Wiki 能力（Wiki）", doc.Title)

	var anchors []string
	for _, s := range doc.Sections {
		anchors = append(anchors, s.Anchor)
	}
	// The lead section, an explicit {#id}, a slug whose full-width
	// punctuation folds to dashes and gains "_" before a digit, a "####"
	// section, and a repeated heading suffixed like the site does.
	assert.Equal(t, []string{"", "怎么开启", "_4-2-队列-asynq-worker-池", "抽取粒度", "常见问题", "常见问题-1"}, anchors)

	deep := doc.Sections[3]
	assert.Equal(t, 4, deep.Level)
	assert.Equal(t, []string{"开启并浏览 Wiki", "4.2 队列（Asynq）：Worker 池"}, deep.Parents)
	assert.Equal(t, "Wiki 能力（Wiki） › 开启并浏览 Wiki › 4.2 队列（Asynq）：Worker 池 › 抽取粒度", deep.Trail())
	assert.Contains(t, deep.Content, "##### 更深的标题", "headings below level 4 stay in their section")
	assert.Equal(t, PublicBaseURL+"03-features/14-wiki#抽取粒度", deep.URL())
}

func TestParseDocCleansSiteMarkup(t *testing.T) {
	doc := parseSample(t)
	lead := doc.Sections[0].Content
	assert.Contains(t, lead, "（截图：Wiki 浏览器）")
	assert.NotContains(t, lead, "不应出现")
	assert.Contains(t, lead, "[观测]("+PublicBaseURL+"03-features/16-observability)")
	assert.Contains(t, lead, "[本节]("+PublicBaseURL+"03-features/14-wiki#抽取粒度)")

	enable := doc.Sections[1].Content
	assert.Contains(t, enable, "[配置]("+PublicBaseURL+"01-getting-started/04-configuration#模型与推理)")
	assert.NotContains(t, enable, "graph TD", "Mermaid diagrams are dropped")
	assert.Contains(t, enable, "（此处为流程图，略）")
	assert.Contains(t, enable, "# 不是标题", "a # line inside a code fence is not a heading")
	assert.Contains(t, enable, "export WIKI_ENABLED=true")

	assert.Contains(t, doc.Sections[2].Content, "（图：拓扑）")
	assert.NotContains(t, doc.Sections[2].Content, "topology.png")
}

func TestParseDocRejectsBrokenPages(t *testing.T) {
	_, err := parseDoc("x/a.md", "", "no title here\n")
	assert.Error(t, err)
	_, err = parseDoc("x/a.md", "", "# T\n\n```go\nunterminated\n")
	assert.Error(t, err)
}

func TestSpanIncludesSubsections(t *testing.T) {
	doc := parseSample(t)
	span, ok := doc.Span("怎么开启")
	require.True(t, ok)
	require.Len(t, span, 3)
	rendered := Render(span)
	assert.True(t, strings.HasPrefix(rendered, "## 开启并浏览 Wiki"))
	assert.Contains(t, rendered, "#### 抽取粒度")
	assert.NotContains(t, rendered, "常见问题")

	whole, ok := doc.Span("")
	require.True(t, ok)
	assert.Len(t, whole, len(doc.Sections))

	_, ok = doc.Span("missing")
	assert.False(t, ok)
}

func TestTokenizeKeepsIdentifiersAndPairsHan(t *testing.T) {
	assert.Equal(t, []string{"ollama_base_url", "ollama", "base", "url"}, tokenize("OLLAMA_BASE_URL"))
	assert.Equal(t, []string{"v0.8.3", "v0"}, tokenize("v0.8.3."))
	assert.Equal(t, []string{"抽取", "取粒", "粒度"}, tokenize("抽取粒度"))
	assert.Equal(t, []string{"wiki", "页"}, tokenize("Wiki 页"))
}

func TestNormalizeRefAcceptsSiteForms(t *testing.T) {
	for _, ref := range []string{
		"03-features/14-wiki",
		"03-features/14-wiki.md",
		"/docs/03-features/14-wiki",
		PublicBaseURL + "03-features/14-wiki/",
		"docs/03-features/14-wiki.html",
	} {
		p, anchor := normalizeRef(ref)
		assert.Equal(t, "03-features/14-wiki", p, ref)
		assert.Empty(t, anchor, ref)
	}
	for _, ref := range []string{
		PublicBaseURL + "03-features/14-wiki#抽取粒度",
		PublicBaseURL + "03-features/14-wiki#%E6%8A%BD%E5%8F%96%E7%B2%92%E5%BA%A6",
		"http://weknora.weixin.qq.com/docs/03-features/14-wiki#抽取粒度",
	} {
		p, anchor := normalizeRef(ref)
		assert.Equal(t, "03-features/14-wiki", p, ref)
		assert.Equal(t, "抽取粒度", anchor, ref)
	}
}

func TestLoadLeavesOutBrokenPages(t *testing.T) {
	lib, err := Load(fstest.MapFS{
		"03-features/14-wiki.md":   {Data: []byte(samplePage)},
		"03-features/99-broken.md": {Data: []byte("# 坏页\n\n```go\nunterminated\n")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "99-broken.md")
	require.NotNil(t, lib, "the pages that parse stay available")
	assert.Len(t, lib.Docs(""), 1)
	assert.NotEmpty(t, lib.Search("抽取粒度", "", 1))

	lib, err = Load(fstest.MapFS{"03-features/99-broken.md": {Data: []byte("no title\n")}})
	assert.Error(t, err)
	assert.Nil(t, lib)
}

func TestLoadAndSearch(t *testing.T) {
	lib, err := Load(fstest.MapFS{
		"03-features/14-wiki.md":     {Data: []byte(samplePage)},
		"03-features/13-embed.md":    {Data: []byte("# 网页嵌入\n\n## 直接 iframe 接入\n\n复制 iframe 代码到页面。\n")},
		"01-getting-started/x.md":    {Data: []byte("# 配置详解\n\n## 模型与推理\n\nOLLAMA_BASE_URL 指向 Ollama。\n")},
		"homepage/not-a-category.md": {Data: []byte("# 首页\n\n抽取粒度\n")},
	})
	require.NoError(t, err)
	assert.Len(t, lib.Docs(""), 3, "only category directories are loaded")
	assert.Len(t, lib.Docs("功能模块"), 2)
	assert.Len(t, lib.Docs("03-features"), 2)

	hits := lib.Search("Wiki 抽取粒度", "", 3)
	require.NotEmpty(t, hits)
	assert.Equal(t, "抽取粒度", hits[0].Section.Anchor)
	assert.Contains(t, hits[0].Snippet, "focused")

	hits = lib.Search("iframe", "01-getting-started", 5)
	assert.Empty(t, hits, "the category filter applies")

	hits = lib.Search("ollama", "", 5)
	require.Len(t, hits, 1)
	assert.Equal(t, "01-getting-started/x", hits[0].Section.Doc.Path)

	assert.Empty(t, lib.Search("  ", "", 5))

	doc, anchor, ok := lib.Doc(PublicBaseURL + "03-features/14-wiki#怎么开启")
	require.True(t, ok)
	assert.Equal(t, "Wiki 能力（Wiki）", doc.Title)
	assert.Equal(t, "怎么开启", anchor)
	_, _, ok = lib.Doc("03-features/../../etc/passwd")
	assert.False(t, ok)
}

// The pages shipped in the binary must all parse, and every link between
// them must point at a page and anchor that exist, so a link the assistant
// hands the user opens the section it named.
func TestEmbeddedDocsParseAndLinksResolve(t *testing.T) {
	lib, err := Default()
	require.NoError(t, err)
	require.Greater(t, len(lib.Docs("")), 50)
	for _, cat := range Categories {
		assert.NotEmpty(t, lib.Docs(cat.Dir), cat.Dir)
	}

	anchors := map[string]map[string]bool{}
	for _, doc := range lib.Docs("") {
		set := map[string]bool{"": true}
		for _, s := range doc.Sections {
			assert.False(t, set[s.Anchor] && s.Anchor != "", "%s: duplicate anchor %q", doc.Path, s.Anchor)
			set[s.Anchor] = true
		}
		anchors[doc.Path] = set
	}
	for _, doc := range lib.Docs("") {
		for _, s := range doc.Sections {
			for _, m := range linkRE.FindAllStringSubmatch(s.Content, -1) {
				target := m[2]
				if !strings.HasPrefix(target, PublicBaseURL) {
					continue
				}
				p, anchor := normalizeRef(target)
				set, ok := anchors[p]
				if !ok {
					// Screenshots and other site assets live outside the pages.
					assert.NotContains(t, p, ".md", "%s links to a missing page %s", doc.Path, target)
					continue
				}
				if anchor != "" && !set[anchor] && !headingBelowSections(lib, p, anchor) {
					t.Errorf("%s links to a missing anchor %s", doc.Path, target)
				}
			}
		}
	}
}

// Categories must match the site's sidebar groups, and every embedded
// directory must be one of them, so a new section is neither left out of the
// index nor labelled differently from the site.
func TestCategoriesMatchTheSiteAndTheEmbed(t *testing.T) {
	config, err := os.ReadFile(filepath.Join("..", "..", "website-docs", ".vitepress", "config.mts"))
	require.NoError(t, err)
	var site []Category
	groupRE := regexp.MustCompile(`\{ dir: '([^']+)', label: '([^']+)'`)
	for _, m := range groupRE.FindAllStringSubmatch(string(config), -1) {
		site = append(site, Category{Dir: m[1], Label: m[2]})
	}
	assert.Equal(t, site, Categories)

	known := map[string]bool{}
	for _, c := range Categories {
		known[c.Dir] = true
	}
	entries, err := fs.ReadDir(websitedocs.Pages, ".")
	require.NoError(t, err)
	for _, e := range entries {
		assert.True(t, known[e.Name()], "embedded directory %s has no category", e.Name())
	}
}

// headingBelowSections reports whether anchor names a heading deeper than
// maxSectionLevel, which the site anchors but this index keeps inside text.
func headingBelowSections(lib *Library, docPath, anchor string) bool {
	doc, _, ok := lib.Doc(docPath)
	if !ok {
		return false
	}
	for _, s := range doc.Sections {
		for _, line := range strings.Split(s.Content, "\n") {
			if m := headingRE.FindStringSubmatch(line); m != nil && len(m[1]) > maxSectionLevel {
				text, id := splitCustomID(m[2])
				if id == anchor || slugify(plainText(text)) == anchor {
					return true
				}
			}
		}
	}
	return false
}
