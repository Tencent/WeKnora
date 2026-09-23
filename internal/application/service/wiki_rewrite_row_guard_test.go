package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ledgerPageBody renders a markdown page whose table carries one data row per
// holder — the certificate-ledger shape that lost rows in production.
func ledgerPageBody(holders ...string) string {
	var b strings.Builder
	b.WriteString("# 持证人台账\n\n| 持证人 | 证书编号 | 有效期 |\n| --- | --- | --- |\n")
	for i, holder := range holders {
		fmt.Fprintf(&b, "| %s | A-%03d | 2020-01-01 |\n", holder, i+1)
	}
	return b.String()
}

// TestApplyRewriteToPageKeepsPageWhenModelDropsLedgerRows is the regression test
// for cause 2 of Tencent/WeKnora#3468: the rewrite is a COMPLETE answer (no
// completion-budget truncation, so the continuation path never sees it) that
// simply leaves out three of the five ledger rows. Before the guard the page was
// overwritten with the two-row fragment and nobody noticed.
func TestApplyRewriteToPageKeepsPageWhenModelDropsLedgerRows(t *testing.T) {
	page := &types.WikiPage{
		Slug:    "entity/cert-ledger",
		Content: ledgerPageBody("张三", "李四", "王五", "赵六", "钱七"),
		Summary: "五位持证人的证书台账",
	}
	wantContent, wantSummary := page.Content, page.Summary
	// The rewrite also carries a new SUMMARY: line, so a refused write must leave
	// the summary alone too.
	rewrite := "SUMMARY: 两位持证人的证书台账\n" + ledgerPageBody("张三", "李四")

	applied, dropped := applyRewriteToPage(page, rewrite, false)

	if applied {
		t.Fatal("applyRewriteToPage() applied a rewrite that dropped 3 of 5 table rows")
	}
	if page.Content != wantContent {
		t.Errorf("refused rewrite still changed page.Content:\n got: %q\nwant: %q", page.Content, wantContent)
	}
	if page.Summary != wantSummary {
		t.Errorf("refused rewrite changed page.Summary = %q, want %q", page.Summary, wantSummary)
	}
	want := []string{"王五", "赵六", "钱七"}
	if len(dropped) != len(want) {
		t.Fatalf("dropped = %v, want %v", dropped, want)
	}
	for i := range want {
		if dropped[i] != want[i] {
			t.Errorf("dropped[%d] = %q, want %q", i, dropped[i], want[i])
		}
	}
}

// A batch that carries retractions removes content on purpose, so the same
// dropped rows must not block the write there.
func TestApplyRewriteToPageAllowsRowLossWhenTheBatchRetracts(t *testing.T) {
	page := &types.WikiPage{
		Slug:    "entity/cert-ledger",
		Content: ledgerPageBody("张三", "李四", "王五", "赵六", "钱七"),
		Summary: "旧摘要",
	}
	rewrite := "SUMMARY: 新摘要\n" + ledgerPageBody("张三", "李四")

	applied, dropped := applyRewriteToPage(page, rewrite, true)

	if !applied {
		t.Fatalf("applyRewriteToPage() refused a retract round (dropped = %v)", dropped)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none on an applied rewrite", dropped)
	}
	// splitSummaryLine trims the body, so the stored content has no trailing
	// newline — the same normalization the pre-guard code applied.
	if page.Content != strings.TrimSpace(ledgerPageBody("张三", "李四")) {
		t.Errorf("page.Content = %q, want the shortened rewrite", page.Content)
	}
	if page.Summary != "新摘要" {
		t.Errorf("page.Summary = %q, want %q", page.Summary, "新摘要")
	}
}

// Reformatting is not data loss: bold markers, padding and an updated date
// column all keep the row, because the row's identity is its first cell.
func TestApplyRewriteToPageTreatsReformattedRowsAsPresent(t *testing.T) {
	page := &types.WikiPage{
		Slug:    "entity/zhang-san",
		Content: "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 2024-01-01 |\n",
	}
	rewrite := "| 姓名 | 日期 |\n| --- | --- |\n| **张三** |   2024-01-02   |\n"

	applied, dropped := applyRewriteToPage(page, rewrite, false)

	if !applied {
		t.Fatalf("applyRewriteToPage() refused a reformatted row (dropped = %v)", dropped)
	}
	if page.Content != strings.TrimSpace(rewrite) {
		t.Errorf("page.Content = %q, want the reformatted rewrite", page.Content)
	}
}

// Header and delimiter rows name no entity, so renaming or re-spacing them can
// never look like a dropped row.
func TestApplyRewriteToPageIgnoresHeaderAndDelimiterRows(t *testing.T) {
	const existing = "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 2024-01-01 |\n"
	rewrite := "| 名称 | 日期 |\n|:--|--:|\n| 张三 | 2024-01-01 |\n"
	page := &types.WikiPage{Slug: "entity/zhang-san", Content: existing}

	applied, dropped := applyRewriteToPage(page, rewrite, false)

	if !applied {
		t.Fatalf("applyRewriteToPage() refused a rewrite with a renamed header (dropped = %v)", dropped)
	}
	if got := rewriteDroppedRowKeys(existing, rewrite); len(got) != 0 {
		t.Errorf("rewriteDroppedRowKeys() = %v, want none: header/delimiter are not identities", got)
	}
}

// Without tables — or without an old body at all — the guard is a no-op, so
// ordinary prose rewrites and brand-new pages keep going through.
func TestApplyRewriteToPageIsNoopWithoutTables(t *testing.T) {
	t.Run("prose on both sides", func(t *testing.T) {
		page := &types.WikiPage{Slug: "entity/prose", Content: "# 标题\n\n第一段。\n", Summary: "旧摘要"}
		rewrite := "SUMMARY: 新摘要\n# 标题\n\n另一段更短的散文。\n"

		applied, dropped := applyRewriteToPage(page, rewrite, false)

		if !applied {
			t.Fatalf("applyRewriteToPage() refused a table-free rewrite (dropped = %v)", dropped)
		}
		if page.Content != "# 标题\n\n另一段更短的散文。" {
			t.Errorf("page.Content = %q", page.Content)
		}
		if page.Summary != "新摘要" {
			t.Errorf("page.Summary = %q, want %q", page.Summary, "新摘要")
		}
	})

	t.Run("new page", func(t *testing.T) {
		page := &types.WikiPage{Slug: "entity/new"}

		applied, dropped := applyRewriteToPage(page, ledgerPageBody("张三"), false)

		if !applied {
			t.Fatalf("applyRewriteToPage() refused a brand-new page (dropped = %v)", dropped)
		}
		if page.Content != strings.TrimSpace(ledgerPageBody("张三")) {
			t.Errorf("page.Content = %q, want the first body", page.Content)
		}
	})
}

func TestRewriteDroppedRowKeys(t *testing.T) {
	cases := []struct {
		name      string
		existing  string
		rewritten string
		want      []string
	}{
		{
			name:      "surviving rows are not dropped",
			existing:  "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 2024-01-01 |\n| 李四 | 2024-02-02 |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 2024-03-03 |\n| 李四 | 2024-02-02 |\n",
			want:      nil,
		},
		{
			name: "emphasis, code markers, case and spacing do not change identity",
			existing: "| 姓名 | 日期 |\n| --- | --- |\n" +
				"| 张三 | 1 |\n| 李四 | 2 |\n| ACME Corp | 3 |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n" +
				"| _张三_ | 1 |\n| `李四` | 2 |\n|   acme   corp | 3 |\n",
			want: nil,
		},
		{
			name:      "a row with an empty first cell is identified by the next cell",
			existing:  "| 姓名 | 日期 |\n| --- | --- |\n|  | 张三 |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n|  | 李四 |\n",
			want:      []string{"张三"},
		},
		{
			name:      "a repeated identity is reported once",
			existing:  "| 姓名 | 部门 |\n| --- | --- |\n| 张三 | 研发 |\n| 张三 | 售前 |\n",
			rewritten: "| 姓名 | 部门 |\n| --- | --- |\n",
			want:      []string{"张三"},
		},
		{
			name:      "a table of only a delimiter row has no identities",
			existing:  "| --- | --- |\n",
			rewritten: "",
			want:      nil,
		},
		{
			name:      "rows whose cells are all empty carry no identity",
			existing:  "| 姓名 | 日期 |\n| --- | --- |\n|  |  |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n",
			want:      nil,
		},
		{
			name:      "prose carries no rows",
			existing:  "# 标题\n\n一段散文。\n",
			rewritten: "# 标题\n",
			want:      nil,
		},
		{
			name:      "dropped rows are reported in old-body order",
			existing:  "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 1 |\n| 李四 | 2 |\n| 王五 | 3 |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 1 |\n",
			want:      []string{"李四", "王五"},
		},
		{
			name:      "internal chunk citations are not part of an identity",
			existing:  "| 姓名 | 日期 |\n| --- | --- |\n| 张三 [c001] | 1 |\n",
			rewritten: "| 姓名 | 日期 |\n| --- | --- |\n| 张三 | 1 |\n",
			want:      nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteDroppedRowKeys(tc.existing, tc.rewritten)
			if len(got) != len(tc.want) {
				t.Fatalf("rewriteDroppedRowKeys() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("rewriteDroppedRowKeys()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
