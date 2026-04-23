package chunker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Fixtures modelled on real medical / regulatory markdown shapes.
// ---------------------------------------------------------------------------

const nmpaAnnouncementSample = `# 国家药监局关于修订头孢曲松钠注射剂说明书的公告

## 一、适应症

用于敏感致病菌所致的下呼吸道感染、尿路和生殖系统感染、腹腔感染等。

## 二、用法用量

### （一）成人

常用剂量为一日 1～2g，一次或分两次静脉滴注。

### （二）儿童

根据体重调整剂量：

1. 新生儿（≤14 天）：按体重一日 20～50mg/kg。
2. 婴幼儿：按体重一日 20～80mg/kg。

## 三、禁忌

### （一）与钙剂合用

本品不得与含钙静脉输液（包括持续性含钙输液）在同一输液管路中给药。
对于 28 天以下的新生儿，禁止使用含钙输液，即使是在不同的输液管路中给药。

### （二）对β-内酰胺类药物过敏者禁用
`

const pureTableSample = `## 表 1 给药剂量

| 年龄分组 | 体重 | 日剂量 |
| --- | --- | --- |
| 新生儿 | <4kg | 20mg/kg |
| 婴幼儿 | 4-10kg | 50mg/kg |
| 儿童 | >10kg | 80mg/kg |
`

var longSectionSample = `# 长节测试

## 一、背景

` + strings.Repeat("这是一段关于药物机制的详细说明。", 400) + `

## 二、结论

综上所述，该药物具有广谱抗菌活性。
`

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSplitMarkdown_EmptyInput(t *testing.T) {
	got := SplitMarkdown("", DefaultMarkdownConfig())
	if got != nil {
		t.Fatalf("expected nil for empty input, got %d chunks", len(got))
	}
	got = SplitMarkdown("   \n\n   ", DefaultMarkdownConfig())
	if got != nil {
		t.Fatalf("expected nil for whitespace-only input, got %d chunks", len(got))
	}
}

func TestSplitMarkdown_NoHeadings_FallsBackToSingleChunk(t *testing.T) {
	text := "这是一段没有任何标题的纯文本内容。"
	got := SplitMarkdown(text, DefaultMarkdownConfig())
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, text) {
		t.Errorf("chunk content missing original text")
	}
	if len(got[0].HeadingPath) != 0 {
		t.Errorf("expected empty heading path for headingless doc, got %v", got[0].HeadingPath)
	}
}

func TestSplitMarkdown_HeadingBoundariesAreHard(t *testing.T) {
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())
	if len(got) == 0 {
		t.Fatal("expected chunks for NMPA sample, got 0")
	}

	// Every chunk should belong to exactly one leaf section. Crucially,
	// no single chunk should mix text from "适应症" AND "禁忌" — that is
	// the medical-safety-critical invariant.
	for i, c := range got {
		body := c.Content
		mixes := strings.Contains(body, "用于敏感致病菌") &&
			strings.Contains(body, "本品不得与含钙静脉输液")
		if mixes {
			t.Errorf("chunk %d mixes content across hard heading boundaries: %q", i, body)
		}
	}
}

func TestSplitMarkdown_BreadcrumbInjected(t *testing.T) {
	cfg := DefaultMarkdownConfig()
	cfg.InjectBreadcrumbs = true
	got := SplitMarkdown(nmpaAnnouncementSample, cfg)

	// Find a chunk expected to be deep (禁忌 / 与钙剂合用).
	var target *MarkdownChunk
	for i := range got {
		if strings.Contains(got[i].Content, "含钙静脉输液") {
			target = &got[i]
			break
		}
	}
	if target == nil {
		t.Fatal("did not find the 含钙静脉输液 chunk")
	}
	if !strings.HasPrefix(strings.TrimSpace(target.Content), "> ") {
		t.Errorf("breadcrumb not prepended, content starts with: %q",
			firstNRunes(target.Content, 40))
	}
	// Breadcrumb must include the deepest ancestor headings so retrieval has context.
	wantSegments := []string{"修订头孢曲松钠注射剂说明书", "三、禁忌", "（一）与钙剂合用"}
	for _, seg := range wantSegments {
		if !strings.Contains(target.Content, seg) {
			t.Errorf("breadcrumb missing segment %q in: %q", seg,
				firstNRunes(target.Content, 120))
		}
	}
}

func TestSplitMarkdown_BreadcrumbDisabled(t *testing.T) {
	cfg := DefaultMarkdownConfig()
	cfg.InjectBreadcrumbs = false
	got := SplitMarkdown(nmpaAnnouncementSample, cfg)
	for i, c := range got {
		if strings.HasPrefix(strings.TrimSpace(c.Content), "> ") {
			t.Errorf("chunk %d has breadcrumb despite InjectBreadcrumbs=false: %q", i,
				firstNRunes(c.Content, 40))
		}
	}
}

func TestSplitMarkdown_HeadingPathBuildsCorrectly(t *testing.T) {
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())

	// Find the "（一）成人" chunk and assert its full path.
	var chunk *MarkdownChunk
	for i := range got {
		if strings.Contains(got[i].Content, "常用剂量为一日") {
			chunk = &got[i]
			break
		}
	}
	if chunk == nil {
		t.Fatal("did not find the 成人用量 chunk")
	}
	want := []string{
		"国家药监局关于修订头孢曲松钠注射剂说明书的公告",
		"二、用法用量",
		"（一）成人",
	}
	if len(chunk.HeadingPath) != len(want) {
		t.Fatalf("heading path length mismatch: got %v, want %v", chunk.HeadingPath, want)
	}
	for i, w := range want {
		if chunk.HeadingPath[i] != w {
			t.Errorf("heading path[%d]: got %q, want %q", i, chunk.HeadingPath[i], w)
		}
	}
	if chunk.HeadingLevel != 3 {
		t.Errorf("heading level: got %d, want 3", chunk.HeadingLevel)
	}
}

func TestSplitMarkdown_SectionCodeExtracted(t *testing.T) {
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())

	// Look for the chunk whose innermost heading is "（一）与钙剂合用".
	var hit bool
	for _, c := range got {
		if len(c.HeadingPath) == 0 {
			continue
		}
		leaf := c.HeadingPath[len(c.HeadingPath)-1]
		if strings.Contains(leaf, "与钙剂合用") {
			hit = true
			if c.SectionCode != "一" {
				t.Errorf("expected section code 一, got %q", c.SectionCode)
			}
		}
	}
	if !hit {
		t.Fatal("did not encounter the 与钙剂合用 section")
	}
}

func TestSplitMarkdown_TableClassifiedAsTable(t *testing.T) {
	got := SplitMarkdown(pureTableSample, DefaultMarkdownConfig())
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk for table-only section, got %d", len(got))
	}
	if got[0].Category != types.ChunkCategoryTable {
		t.Errorf("expected category %q, got %q", types.ChunkCategoryTable, got[0].Category)
	}
}

func TestSplitMarkdown_ListClassifiedAsList(t *testing.T) {
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())
	var foundListChunk bool
	for _, c := range got {
		leaf := ""
		if len(c.HeadingPath) > 0 {
			leaf = c.HeadingPath[len(c.HeadingPath)-1]
		}
		if strings.Contains(leaf, "儿童") {
			foundListChunk = true
			if c.Category != types.ChunkCategoryList {
				t.Errorf("儿童 section expected list category, got %q. Body: %q",
					c.Category, firstNRunes(c.Content, 80))
			}
		}
	}
	if !foundListChunk {
		t.Fatal("expected to find the 儿童 list section")
	}
}

func TestSplitMarkdown_OversizedSectionSubSplits(t *testing.T) {
	cfg := DefaultMarkdownConfig()
	cfg.SoftMaxChars = 200
	cfg.ChildChunkSize = 100
	cfg.ChildChunkOverlap = 10
	got := SplitMarkdown(longSectionSample, cfg)

	// Find all sub-chunks belonging to 背景.
	var bgChunks []MarkdownChunk
	for _, c := range got {
		if len(c.HeadingPath) == 0 {
			continue
		}
		leaf := c.HeadingPath[len(c.HeadingPath)-1]
		if strings.Contains(leaf, "背景") {
			bgChunks = append(bgChunks, c)
		}
	}
	if len(bgChunks) < 2 {
		t.Fatalf("oversized 背景 section should split into >1 chunks, got %d", len(bgChunks))
	}
	// Every sub-chunk must inherit the same heading path so downstream
	// retrieval treats them as belonging to the same section.
	first := bgChunks[0].HeadingPath
	for i, c := range bgChunks {
		if len(c.HeadingPath) != len(first) {
			t.Errorf("bg chunk %d heading path length drift: %v vs %v", i, c.HeadingPath, first)
			continue
		}
		for j := range first {
			if c.HeadingPath[j] != first[j] {
				t.Errorf("bg chunk %d heading path drift at depth %d: %q vs %q",
					i, j, c.HeadingPath[j], first[j])
			}
		}
	}
}

func TestSplitMarkdown_MetadataShapeRoundTrip(t *testing.T) {
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())
	if len(got) == 0 {
		t.Fatal("no chunks produced")
	}
	for i, c := range got {
		meta := c.ToMetadata()
		if len(c.HeadingPath) > 0 {
			v, ok := meta[types.MetaKeyHeadingPath]
			if !ok {
				t.Errorf("chunk %d missing heading_path in metadata", i)
				continue
			}
			// stored as []string to survive JSON round-trip without widening.
			paths, ok := v.([]string)
			if !ok {
				t.Errorf("chunk %d heading_path wrong type: %T", i, v)
				continue
			}
			if len(paths) != len(c.HeadingPath) {
				t.Errorf("chunk %d heading_path length mismatch: %v vs %v", i, paths, c.HeadingPath)
			}
		}
		if c.Category != "" && meta[types.MetaKeyCategory] != c.Category {
			t.Errorf("chunk %d category mismatch: meta=%v field=%q",
				i, meta[types.MetaKeyCategory], c.Category)
		}
	}
}

func TestSplitMarkdown_MineruVLMTwoColumnArtefacts(t *testing.T) {
	// Real-world shape produced by MinerU VLM on two-column medical PDFs.
	// The reference sample is distilled from the "二甲双胍...铁死亡" paper
	// actually ingested during dev (see DB dump).
	text := "# 二甲双胍通过上调Nrf2表达抑制糖尿病肾小管上皮细胞铁死亡\n\n" +
		"吴子瑜, 俞婷, 郑谋炜, 郭太林\n\n" +
		"福建医科大学省立临床医学院\n\n" +
		"摘要 目的：探究二甲双胍抑制糖尿病肾小管上皮细胞铁死亡的作用机制。\n\n" +
		// <<< The two-column layout artefact — heading is just "1" >>>
		"# 1\n\n" +
		"材料与方法\n\n" +
		// Line-start subsections with Han-only titles, no ### markers:
		"1.1 实验动物和细胞系 8周龄雄性DBA/2J小鼠,无特定病原体级,体质量22-26g,购自北京华阜康生物科技股份有限公司。\n\n" +
		"1.2 主要试剂和仪器 血清尿素氮(BUN)和血清肌酐(Scr)检测试剂盒购自中国凡科维公司。\n\n" +
		"1.3 动物实验分组及给药 24只DBA/2J小鼠被随机分为正常对照组、二甲双胍治疗组。\n\n" +
		"1.4 肾组织病理检查 取小鼠肾组织,用4%多聚甲醛固定24h。\n\n" +
		"1.6 免疫组化染色 石蜡切片常规脱蜡,磷酸盐缓冲溶液洗1次。\n\n" +
		"1.7 细胞培养及分组 HK-2细胞置于含10%胎牛血清的DMEM培养基中。\n"

	got := SplitMarkdown(text, DefaultMarkdownConfig())

	// Every subsection must have a distinct heading_path ending with its own
	// subsection title (not just "1"). This is the whole point of the fix.
	wantSubsectionTitles := []string{
		"实验动物和细胞系",
		"主要试剂和仪器",
		"动物实验分组及给药",
		"肾组织病理检查",
		"免疫组化染色",
		"细胞培养及分组",
	}

	matched := make(map[string]bool)
	for _, chunk := range got {
		if len(chunk.HeadingPath) == 0 {
			continue
		}
		leaf := chunk.HeadingPath[len(chunk.HeadingPath)-1]
		for _, want := range wantSubsectionTitles {
			if strings.Contains(leaf, want) {
				matched[want] = true
			}
		}
	}
	// Diagnostic: always dump what we produced in a legible shape.
	dump := func() string {
		var b strings.Builder
		b.WriteString("\n=== Chunks produced ===\n")
		for i, c := range got {
			fmt.Fprintf(&b, "[%d] depth=%d path=%q\n    preview=%s\n",
				i, len(c.HeadingPath), c.HeadingPath, firstNRunes(c.Content, 60))
		}
		return b.String()
	}

	for _, want := range wantSubsectionTitles {
		if !matched[want] {
			t.Errorf("subsection title %q was not promoted to its own heading_path leaf.%s",
				want, dump())
			break // no point repeating dump for every missing one
		}
	}

	// The orphan-number heading "# 1" must have been merged with "材料与方法"
	// so no chunk should have breadcrumb of just "> 1".
	for _, c := range got {
		if len(c.HeadingPath) >= 2 && strings.TrimSpace(c.HeadingPath[1]) == "1" {
			t.Errorf("orphan numeric heading '1' was not merged with its title row. "+
				"heading_path=%v", c.HeadingPath)
		}
	}
}

func TestSplitMarkdown_NumberedSubsectionsBreakParagraphs(t *testing.T) {
	// MinerU pipeline mode fails to emit H3 headings for "1.1 / 1.2 / 1.3"
	// subsections in medical papers. Our paragraph-break normaliser must
	// at least break them apart so an oversize "## 1材料与方法" section
	// splits at subsection boundaries instead of mid-sentence.
	text := "# 二甲双胍抗铁死亡\n\n## 1材料与方法\n\n" +
		"引言段落结束。 1.1实验动物和细胞系8周龄雄性 DBA/2J小鼠,体质量22～26g,购自北京华阜康生物科技。 " +
		"1.2主要试剂和仪器血清尿素氮(BUN)和血清肌酐(Scr)检测试剂盒购自中国凡科维公司。 " +
		"1.3动物实验分组及给药24只DBA/2J小鼠被随机分为正常对照组、二甲双胍治疗组。 " +
		"1.4肾组织病理检查取小鼠肾组织,用4%多聚甲醛固定24h。 " +
		"1.5BUN 和 Scr检测小鼠麻醉后取全血,3000r/min 离心15min。"

	// Force a tight SoftMaxChars so the section MUST be sub-split.
	cfg := DefaultMarkdownConfig()
	cfg.SoftMaxChars = 150
	cfg.ChildChunkSize = 150
	got := SplitMarkdown(text, cfg)

	if len(got) < 3 {
		t.Fatalf("expected multiple sub-chunks from breakable paragraphs, got %d. Chunks: %+v",
			len(got), got)
	}

	// Each sub-chunk should still carry the H2 breadcrumb.
	for i, c := range got {
		if len(c.HeadingPath) == 0 {
			continue // preamble chunks OK
		}
		wantLeaf := "1材料与方法"
		if !strings.Contains(c.HeadingPath[len(c.HeadingPath)-1], wantLeaf) {
			t.Errorf("chunk %d leaf heading drift: got %q, want contains %q",
				i, c.HeadingPath[len(c.HeadingPath)-1], wantLeaf)
		}
	}

	// At least one sub-chunk should contain "1.1实验动物" and NOT "1.5BUN"
	// (i.e. the paragraphs actually got split apart).
	var sawIsolated bool
	for _, c := range got {
		if strings.Contains(c.Content, "1.1实验动物") && !strings.Contains(c.Content, "1.5BUN") {
			sawIsolated = true
			break
		}
	}
	if !sawIsolated {
		t.Errorf("expected 1.1 and 1.5 to land in different chunks, but nothing got isolated. " +
			"Paragraph-break normaliser may be failing.")
	}
}

func TestSplitMarkdown_ShortPreambleDropped(t *testing.T) {
	// MinerU frequently leaks the journal column label ("基础研究", "论著") on
	// the line above the title heading. That 2-3 char preamble must NOT
	// become its own chunk.
	text := "基础研究 # 二甲双胍通过上调Nrf2表达抑制糖尿病肾小管上皮细胞铁死亡\n\n" +
		"摘要目的:探究二甲双胍通过促进核因子 E2相关因子 2 表达抑制糖尿病肾小管上皮细胞铁死亡的作用机制。"

	got := SplitMarkdown(text, DefaultMarkdownConfig())
	for _, c := range got {
		if strings.TrimSpace(c.Content) == "基础研究" {
			t.Fatalf("short preamble '基础研究' was kept as a chunk; expected it dropped")
		}
		// Also check the breadcrumb-injected form.
		if strings.Contains(c.Content, "基础研究") && runeLen(c.Content) < 40 {
			t.Fatalf("short preamble surfaced as tiny chunk: %q", c.Content)
		}
	}
	// But the real content should still be present somewhere.
	combined := ""
	for _, c := range got {
		combined += c.Content
	}
	if !strings.Contains(combined, "摘要目的") {
		t.Errorf("real content was lost when dropping preamble")
	}
}

func TestSplitMarkdown_MineruInlineHeadingsRecovered(t *testing.T) {
	// MinerU on two-column medical PDFs emits "正文 ## 标题 正文" streams
	// without line breaks. Our normaliser must recover the heading into its
	// own line so the structure splitter can see it.
	text := "摘要内容，研究目的探究二甲双胍的作用。 ## 1材料与方法 1.1实验动物 使用 DBA/2J 小鼠 24 只。 ## 2结果 与对照组相比，治疗组明显改善（P<0.05）。"
	got := SplitMarkdown(text, DefaultMarkdownConfig())
	if len(got) < 2 {
		t.Fatalf("expected the inline headings to produce multiple sections, got %d chunks. Chunks: %+v", len(got), got)
	}

	// At least one chunk must carry heading_path containing "1材料与方法".
	found := false
	for _, c := range got {
		for _, h := range c.HeadingPath {
			if strings.Contains(h, "1材料与方法") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("inline heading '## 1材料与方法' was not promoted to a section header. Chunks: %+v", got)
	}
}

func TestSplitMarkdown_HeadingInsideCodeFenceIgnored(t *testing.T) {
	// A '#' inside a fenced code block must NOT create a section.
	text := "# 正文标题\n\n一些内容。\n\n```\n# 这不是标题\n```\n\n## 后续\n正文后续。\n"
	got := SplitMarkdown(text, DefaultMarkdownConfig())
	for _, c := range got {
		for _, h := range c.HeadingPath {
			if strings.Contains(h, "这不是标题") {
				t.Fatalf("heading inside code fence was incorrectly parsed: %q", h)
			}
		}
	}
}

func TestSplitMarkdown_PositionsWithinDocument(t *testing.T) {
	// Offsets reported by the splitter must be rune offsets into the original
	// document (not into the breadcrumb-injected content) so UIs can highlight
	// source spans.
	got := SplitMarkdown(nmpaAnnouncementSample, DefaultMarkdownConfig())
	totalRunes := runeLen(nmpaAnnouncementSample)
	for i, c := range got {
		if c.Start < 0 || c.End > totalRunes || c.Start >= c.End {
			t.Errorf("chunk %d has out-of-range positions [%d, %d] for doc of %d runes",
				i, c.Start, c.End, totalRunes)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func firstNRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
