package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// handleFor builds a syntactically valid 22-character resource handle for
// artifact i, so tests exercise the same ParseResourcePath path as production.
func handleFor(i int) string {
	base := fmt.Sprintf("art%d", i)
	return base + strings.Repeat("x", types.ResourceHandleLength-len(base))
}

func refFor(i int) string {
	return types.BuildResourcePath(handleFor(i))
}

// artifactsFixture builds artifacts whose storage URL is a catalog handle —
// the normal deployment. Use artifactsWithoutCatalog for the degraded case.
func artifactsFixture(names ...string) types.MessageArtifacts {
	list := make(types.MessageArtifacts, 0, len(names))
	for i, name := range names {
		list = append(list, types.MessageArtifact{FileName: name, URL: refFor(i)})
	}
	return list
}

func artifactsWithoutCatalog(names ...string) types.MessageArtifacts {
	list := make(types.MessageArtifacts, 0, len(names))
	for _, name := range names {
		list = append(list, types.MessageArtifact{FileName: name, URL: "local://7/exports/" + name})
	}
	return list
}

func TestRewriteArtifactReferences(t *testing.T) {
	artifacts := artifactsFixture(
		"市场画像评分_e7edba.html",
		"concept_ranking.csv",
		"trend.png",
		"腾讯控股(00700) 成交量_838ccc.html",
	)

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "file name with spaces and parentheses",
			content: "![成交量](sandbox:腾讯控股(00700) 成交量_838ccc.html)",
			want:    "![成交量](" + refFor(3) + ")",
		},
		{
			name:    "file name with spaces and parentheses, no prefix",
			content: "![成交量](腾讯控股(00700) 成交量_838ccc.html)",
			want:    "![成交量](" + refFor(3) + ")",
		},
		{
			name:    "bare file name in image",
			content: "![市场画像评分](市场画像评分_e7edba.html)",
			want:    "![市场画像评分](" + refFor(0) + ")",
		},
		{
			name:    "sandbox prefix",
			content: "![评分](sandbox:市场画像评分_e7edba.html)",
			want:    "![评分](" + refFor(0) + ")",
		},
		{
			name:    "sandbox scheme with slashes",
			content: "![评分](sandbox://trend.png)",
			want:    "![评分](" + refFor(2) + ")",
		},
		{
			name:    "directory prefix is dropped",
			content: "[榜单](/workspace/output/concept_ranking.csv)",
			want:    "[榜单](" + refFor(1) + ")",
		},
		{
			name:    "percent-encoded name",
			content: "![评分](%E5%B8%82%E5%9C%BA%E7%94%BB%E5%83%8F%E8%AF%84%E5%88%86_e7edba.html)",
			want:    "![评分](" + refFor(0) + ")",
		},
		{
			name:    "title is preserved",
			content: `![评分](trend.png "走势")`,
			want:    `![评分](` + refFor(2) + ` "走势")`,
		},
		{
			name:    "ordinary link with a colliding name is not rewritten",
			content: "见 [说明](trend.png)",
			want:    "见 [说明](trend.png)",
		},
		{
			name:    "sandbox-prefixed link is rewritten even when not an image",
			content: "数据见 [表格](sandbox:concept_ranking.csv)",
			want:    "数据见 [表格](" + refFor(1) + ")",
		},
		{
			name:    "already-rewritten reference is left alone",
			content: "![评分](" + refFor(0) + ")",
			want:    "![评分](" + refFor(0) + ")",
		},
		{
			name:    "prose parentheses are not link destinations",
			content: "腾讯控股(00700) 的成交量见下图。",
			want:    "腾讯控股(00700) 的成交量见下图。",
		},
		{
			name:    "unknown file name untouched",
			content: "![别的](missing.html)",
			want:    "![别的](missing.html)",
		},
		{
			name:    "http url untouched",
			content: "![远程](https://example.com/trend.png)",
			want:    "![远程](https://example.com/trend.png)",
		},
		{
			name:    "knowledge base image untouched",
			content: "![资源](resource://abcdefghijklmnopqrstuv)",
			want:    "![资源](resource://abcdefghijklmnopqrstuv)",
		},
		{
			name:    "fenced code untouched",
			content: "```\n![评分](trend.png)\n```",
			want:    "```\n![评分](trend.png)\n```",
		},
		{
			name:    "inline code untouched",
			content: "写成 `![评分](trend.png)` 即可",
			want:    "写成 `![评分](trend.png)` 即可",
		},
		{
			name:    "plain prose untouched",
			content: "生成了 trend.png 和 concept_ranking.csv 两个文件。",
			want:    "生成了 trend.png 和 concept_ranking.csv 两个文件。",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteArtifactReferences(tc.content, artifacts); got != tc.want {
				t.Fatalf("rewriteArtifactReferences() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRewriteArtifactReferencesMixedContent(t *testing.T) {
	artifacts := artifactsFixture("chart.html", "data.csv")
	content := "## 图表\n\n![图表](chart.html)\n\n数据见 [表格](sandbox:data.csv)，" +
		"外链 [文档](https://example.com/chart.html) 不受影响。"
	want := "## 图表\n\n![图表](" + refFor(0) + ")\n\n数据见 [表格](" + refFor(1) + ")，" +
		"外链 [文档](https://example.com/chart.html) 不受影响。"

	if got := rewriteArtifactReferences(content, artifacts); got != want {
		t.Fatalf("rewriteArtifactReferences() = %q, want %q", got, want)
	}
}

// A knowledge-base image and a skill artifact routinely appear in the same
// answer. Only the artifact is rebound; the existing reference must survive
// byte for byte, since it already is the canonical form.
func TestRewriteArtifactReferencesKeepsExistingResourceImages(t *testing.T) {
	artifacts := artifactsFixture("chart.html")
	kbImage := types.BuildResourcePath(strings.Repeat("Z", types.ResourceHandleLength))
	content := "![检索图](" + kbImage + ")\n\n![图表](chart.html)"
	want := "![检索图](" + kbImage + ")\n\n![图表](" + refFor(0) + ")"

	if got := rewriteArtifactReferences(content, artifacts); got != want {
		t.Fatalf("rewriteArtifactReferences() = %q, want %q", got, want)
	}
}

// Without a resource catalog there is no durable handle, so references are
// normalized to the chat-only sandbox form rather than leaking a storage path.
func TestRewriteArtifactReferencesWithoutCatalog(t *testing.T) {
	artifacts := artifactsWithoutCatalog("chart.html")
	got := rewriteArtifactReferences("![图表](chart.html)", artifacts)
	if want := "![图表](sandbox:chart.html)"; got != want {
		t.Fatalf("rewriteArtifactReferences() = %q, want %q", got, want)
	}
	if strings.Contains(got, "local://") {
		t.Fatalf("storage path leaked into content: %q", got)
	}
}

func TestRewriteArtifactReferencesNoArtifacts(t *testing.T) {
	content := "![图表](chart.html)"
	if got := rewriteArtifactReferences(content, nil); got != content {
		t.Fatalf("rewriteArtifactReferences() = %q, want unchanged", got)
	}
}

func TestArtifactRefByNameKeepsFirstDuplicate(t *testing.T) {
	byName := artifactRefByName(artifactsFixture("a.html", "a.html", "b.html"))
	if byName["a.html"] != refFor(0) {
		t.Fatalf("duplicate name resolved to %q, want %q", byName["a.html"], refFor(0))
	}
	if byName["b.html"] != refFor(2) {
		t.Fatalf("b.html resolved to %q, want %q", byName["b.html"], refFor(2))
	}
}
