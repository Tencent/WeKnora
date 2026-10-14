package chunker

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
)

// buildMergedCellHTMLTable renders a MinerU-style merged-cell engineering
// table (rowspan/colspan > 1 forces NormalizeHTMLTables to keep raw HTML,
// one <tr> per line). rows controls the number of data rows so the table
// spans multiple chunks at the given chunk size.
func buildMergedCellHTMLTable(rows int) string {
	var b strings.Builder
	b.WriteString("<table>\n")
	b.WriteString("<tr><th>构件名称</th><th>规格</th><th>单位</th><th>跨号1</th><th>跨号2</th><th>跨号3</th><th>合计</th></tr>\n")
	b.WriteString("<tr><td rowspan=\"2\">小石子混凝土</td><td>C20</td><td>m3</td>" +
		"<td>21.6</td><td>31.6</td><td></td><td>53.2</td></tr>\n")
	for i := 0; i < rows; i++ {
		b.WriteString("<tr><td>现浇混凝土</td><td colspan=\"2\">C40</td>" +
			"<td>493.7</td><td>1181.6</td><td>59.4</td><td>1734.7</td></tr>\n")
	}
	b.WriteString("</table>\n")
	return b.String()
}

var htmlTableSplitCfg = SplitterConfig{
	ChunkSize:    500,
	ChunkOverlap: 80,
	Separators:   []string{"\n\n", "\n", "。"},
}

// assertWellFormedTableChunks verifies the issue #4272 fix: every chunk that
// carries table data rows also carries the header row and a balanced
// <table>…</table> fragment the frontend can render.
func assertWellFormedTableChunks(t *testing.T, label string, contents []string) {
	t.Helper()
	dataChunks := 0
	for i, c := range contents {
		if !strings.Contains(c, "<td>") {
			continue
		}
		dataChunks++
		if !strings.Contains(c, "构件名称") {
			t.Errorf("%s chunk %d lost the header row (issue #4272):\n%.200s", label, i, c)
		}
		if !strings.Contains(c, "<table") {
			t.Errorf("%s chunk %d lost the <table> container:\n%.200s", label, i, c)
		}
		if strings.Count(c, "<table") != strings.Count(c, "</table>") {
			t.Errorf("%s chunk %d has unbalanced <table> tags:\n%.200s", label, i, c)
		}
	}
	if dataChunks < 3 {
		t.Fatalf("%s: expected the table to span multiple chunks, got %d data chunks in %d",
			label, dataChunks, len(contents))
	}
}

// TestHTMLTableChunksCarryHeader covers issue #4272: merged-cell HTML tables
// split across chunks must repeat the table header and a closed container in
// every data chunk, so retrieved slices stay readable for the LLM and the
// frontend.
func TestHTMLTableChunksCarryHeader(t *testing.T) {
	ingested := docparser.NormalizeHTMLTables(buildMergedCellHTMLTable(20))
	if !strings.Contains(ingested, "<tr") {
		t.Fatalf("expected NormalizeHTMLTables fallback to keep raw HTML rows, got:\n%s", ingested)
	}

	chunks := SplitText(ingested, htmlTableSplitCfg)
	contents := make([]string, len(chunks))
	for i, c := range chunks {
		contents[i] = c.Content
	}
	assertWellFormedTableChunks(t, "chunk", contents)
}

// TestHTMLTableParentChildCarryHeader covers the parent-child breakage from
// issue #4272: parents covering later table rows must also carry the header.
func TestHTMLTableParentChildCarryHeader(t *testing.T) {
	ingested := docparser.NormalizeHTMLTables(buildMergedCellHTMLTable(20))

	parentCfg := htmlTableSplitCfg
	parentCfg.ChunkSize = 900
	childCfg := htmlTableSplitCfg
	childCfg.ChunkSize = 300
	childCfg.ChunkOverlap = 40

	res := SplitTextParentChild(ingested, parentCfg, childCfg)
	parents := make([]string, 0, len(res.Parents))
	for _, p := range res.Parents {
		parents = append(parents, p.Content)
	}
	assertWellFormedTableChunks(t, "parent", parents)
}

// TestHTMLTableWithTheadCarryHeader checks that a <thead>-wrapped header block
// is closed with </thead></table> when prepended to later chunks.
func TestHTMLTableWithTheadCarryHeader(t *testing.T) {
	var b strings.Builder
	b.WriteString("<table>\n<thead>\n<tr><th>构件名称</th><th>规格</th><th>单位</th>" +
		"<th>跨号1</th><th>跨号2</th><th>跨号3</th><th>合计</th></tr>\n</thead>\n")
	for i := 0; i < 25; i++ {
		b.WriteString("<tr><td>现浇混凝土</td><td>C40</td><td>m3</td><td>493.7</td>" +
			"<td>1181.6</td><td>59.4</td><td>1734.7</td></tr>\n")
	}
	b.WriteString("</table>\n")

	chunks := SplitText(b.String(), htmlTableSplitCfg)
	for i, c := range chunks {
		if !strings.Contains(c.Content, "<td>") {
			continue
		}
		if !strings.Contains(c.Content, "构件名称") {
			t.Errorf("chunk %d lost the thead header row:\n%.200s", i, c.Content)
		}
		if strings.Count(c.Content, "<thead") != strings.Count(c.Content, "</thead>") ||
			strings.Count(c.Content, "<table") != strings.Count(c.Content, "</table>") {
			t.Errorf("chunk %d has unbalanced thead/table tags:\n%.200s", i, c.Content)
		}
	}
}

// TestHTMLTableEndsBeforeNextSection ensures the HTML header deactivates at
// </table> and does not leak into following prose chunks.
func TestHTMLTableEndsBeforeNextSection(t *testing.T) {
	md := buildMergedCellHTMLTable(8) + "\n\n## 附件说明\n\n本表为示例数据，不含实际工程量。\n"

	chunks := SplitText(md, htmlTableSplitCfg)
	for i, c := range chunks {
		content := c.Content
		if strings.Contains(content, "附件说明") && strings.Contains(content, "<td>") &&
			!strings.Contains(content, "构件名称") {
			t.Errorf("chunk %d mixes prose with header-less table rows:\n%s", i, content)
		}
	}
}

// TestHTMLTableWideHeader30Columns reproduces the reporter's exact shape from
// issue #4272: a 30-column merged-cell table whose header row alone exceeds
// chunkSize. Every data chunk, parent and child must still carry the header.
func TestHTMLTableWideHeader30Columns(t *testing.T) {
	var b strings.Builder
	b.WriteString("<table>\n")
	b.WriteString("<tr><th>构件名称</th><th>规格</th><th>单位</th>")
	for i := 0; i < 30; i++ {
		b.WriteString("<th>跨号" + string(rune('0'+i%10)) + "</th>")
	}
	b.WriteString("<th>合计</th></tr>\n")
	for r := 0; r < 12; r++ {
		b.WriteString("<tr><td>现浇混凝土</td><td colspan=\"2\">C40</td>")
		for i := 0; i < 30; i++ {
			b.WriteString("<td>1.6</td>")
		}
		b.WriteString("<td>1734.7</td></tr>\n")
	}
	b.WriteString("</table>\n")
	ingested := docparser.NormalizeHTMLTables(b.String())

	chunks := SplitText(ingested, htmlTableSplitCfg)
	dataChunks := 0
	for i, c := range chunks {
		if !strings.Contains(c.Content, "<td>") {
			continue
		}
		dataChunks++
		if !strings.Contains(c.Content, "构件名称") {
			t.Errorf("chunk %d: data rows without the wide header (issue #4272)", i)
		}
	}
	if dataChunks < 5 {
		t.Fatalf("expected the wide table to span many chunks, got %d", dataChunks)
	}

	parentCfg := htmlTableSplitCfg
	parentCfg.ChunkSize = 4096
	childCfg := htmlTableSplitCfg
	childCfg.ChunkSize = 384
	childCfg.ChunkOverlap = 40
	res := SplitTextParentChild(ingested, parentCfg, childCfg)
	for _, p := range res.Parents {
		if strings.Contains(p.Content, "<td>") && !strings.Contains(p.Content, "构件名称") {
			t.Errorf("parent lost the wide header:\n%.120s", p.Content)
		}
	}
	for _, ch := range res.Children {
		if strings.Contains(ch.Content, "<td>") && !strings.Contains(ch.Content, "构件名称") {
			t.Errorf("child lost the wide header:\n%.120s", ch.Content)
		}
	}
}
