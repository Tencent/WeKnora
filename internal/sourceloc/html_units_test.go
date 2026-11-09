package sourceloc

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// The html fixture mirrors what the docreader html converter receives:
// headings, paragraphs, a GFM-converted table, nested lists and script /
// style / navigation chrome.
const htmlFixture = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>季报</title>
<style>.nav{font-size:14px}</style>
<script>var user={id:1};function track(e){}</script></head>
<body>
<div class="nav">首页 | 产品 | 关于我们 | 联系我们</div>
<h1>华信科技三季度经营综述</h1>
<p>2026年第三季度，华信科技实现营业收入42.7亿元，同比增长18.3%，环比增长6.2%。</p>
<h2>盈利预测</h2>
<p>下表为关键财务指标预测，金额单位为人民币百万元。</p>
<table border="1">
<tr><th>指标</th><th>2026E</th><th>2027E</th></tr>
<tr><td>营业收入</td><td>15,500</td><td>18,910</td></tr>
<tr><td>EPS（元）</td><td>2.42</td><td>3.07</td></tr>
</table>
<ul>
<li>身份信息加密存储级别为L4，访问需双人审批。</li>
<li>行为数据脱敏后保留180天。</li>
</ul>
<div class="footer">版权所有 © 2016-2026 沪ICP备00000000号</div>
</body></html>`

func TestHTMLUnitsStructure(t *testing.T) {
	units := htmlUnits([]byte(htmlFixture))
	if len(units) < 8 {
		t.Fatalf("expected at least 8 units, got %d: %+v", len(units), units)
	}
	var (
		foundHeading bool
		foundRow     bool
		foundChrome  bool
	)
	for _, u := range units {
		if u.Text == "华信科技三季度经营综述" && u.Locator.Section == 1 {
			foundHeading = true
		}
		if strings.Contains(u.Text, "营业收入") && strings.Contains(u.Text, "15,500") &&
			strings.Contains(u.Text, "18,910") {
			foundRow = true
		}
		if strings.Contains(u.Text, "function track") || strings.Contains(u.Text, ".nav{") {
			foundChrome = true
		}
	}
	if !foundHeading {
		t.Error("heading unit with its own section ordinal missing")
	}
	if !foundRow {
		t.Error("table row should arrive as one joined unit (cells + text)")
	}
	if foundChrome {
		t.Error("script/style content must never become a unit")
	}
	// Units after the first heading carry the heading's section ordinal.
	last := units[len(units)-1]
	if last.Locator.Section == 0 {
		t.Errorf("trailing unit should inherit a section ordinal, got %+v", last.Locator)
	}
}

func TestHTMLAlignProducesBlocksWithOriginalQuotes(t *testing.T) {
	// The markdown shape the html converter emits: ATX headings, GFM table.
	markdown := "# 华信科技三季度经营综述\n\n" +
		"2026年第三季度，华信科技实现营业收入42.7亿元，同比增长18.3%，环比增长6.2%。\n\n" +
		"## 盈利预测\n\n下表为关键财务指标预测，金额单位为人民币百万元。\n\n" +
		"| 指标 | 2026E | 2027E |\n| --- | --- | --- |\n" +
		"| 营业收入 | 15,500 | 18,910 |\n| EPS（元） | 2.42 | 3.07 |\n\n" +
		"- 身份信息加密存储级别为L4，访问需双人审批。\n- 行为数据脱敏后保留180天。\n"
	blocks, _ := AlignStructure("html", []byte(htmlFixture), markdown)
	if len(blocks) < 5 {
		t.Fatalf("expected at least 5 aligned blocks, got %d", len(blocks))
	}
	if blocks[0].Locator.Type != types.SourceLocatorSection || blocks[0].Locator.Section == 0 {
		t.Fatalf("blocks must be section-anchored, got %+v", blocks[0].Locator)
	}

	// Quotes are minted per chunk range from the Index, exactly as the
	// ingest pipeline does (buildSourceIndex → Locators(chunk range)).
	idx := NewIndex(markdown, blocks)
	if idx == nil {
		t.Fatal("NewIndex returned nil")
	}
	runeAt := func(byteAt int) int { return utf8.RuneCountInString(markdown[:byteAt]) }
	paraAt := runeAt(strings.Index(markdown, "2026年第三季度"))
	paraEnd := runeAt(strings.Index(markdown, "盈利预测"))
	locs := idx.Locators(paraAt, paraEnd)
	if len(locs) == 0 {
		t.Fatal("no locators for the revenue paragraph chunk")
	}
	if !strings.Contains(locs[0].Quote, "华信科技实现营业收入42.7亿元") {
		t.Errorf("quote must be original HTML text, got %q", locs[0].Quote)
	}
	if locs[0].Section != 1 {
		t.Errorf("revenue paragraph sits in section 1, got %+v", locs[0])
	}
	rowAt := runeAt(strings.Index(markdown, "| 营业收入 |"))
	rowEnd := runeAt(strings.Index(markdown, "EPS（元）") + len("EPS（元）"))
	rowLocs := idx.Locators(rowAt, rowEnd)
	if len(rowLocs) == 0 || !strings.Contains(rowLocs[0].Quote, "15,500") {
		t.Errorf("table row chunk should quote joined cell text, got %+v", rowLocs)
	}
}

func TestHTMLAlignStrayMarkdownYieldsNoBlocks(t *testing.T) {
	blocks, _ := AlignStructure("htm", []byte(htmlFixture), "# 完全无关的内容\n\n这份markdown在原文中不存在。\n")
	if len(blocks) != 0 {
		t.Fatalf("unrelated markdown must not align, got %d blocks", len(blocks))
	}
}
