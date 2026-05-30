package searchutil

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAppendWithOverlap_ContiguousNoTrim(t *testing.T) {
	// 首尾相接（无重叠），且 next 含 HTML 实体使其字符数 > EndAt-StartAt。
	// 旧的按位置公式会多切掉开头，这里应当整段保留。
	acc := "## 第二节\n\n"
	next := "| 列A | 列B |\n| 值1 | 含实体&#34;引号&#34;的内容 |\n"
	got := AppendWithOverlap(acc, next, 0)
	want := acc + next
	if got != want {
		t.Fatalf("contiguous merge mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAppendWithOverlap_PrependedTableHeaderSkipped(t *testing.T) {
	// 模拟 chunker 给拆分表格补写表头：next 开头多了一份表头（零宽，位置不可见），
	// 真正的重叠行在表头之后。按位置裁剪会错位，按文本匹配应正确去重。
	header := "| 列1 | 列2 | 列3 |\n|:---|:---|:---|\n"
	overlapRows := "| 第5行 | 内容5A | 内容5B |\n| 第6行 | 内容6A | 内容6B |\n"
	accTail := "| 第4行 | 内容4A | 内容4B |\n" + overlapRows
	acc := header + "| 第1行 | x | — |\n" + accTail

	newRows := "| 第7行 | 内容7A | 内容7B |\n| 第8行 | 内容8A | 内容8B |\n"
	next := header + overlapRows + newRows

	// 位置重叠量大约是两行的长度（这里给个近似值即可，仅用于窗口估算）。
	got := AppendWithOverlap(acc, next, len([]rune(overlapRows)))
	want := acc + newRows
	if got != want {
		t.Fatalf("prepended-header merge mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAppendWithOverlap_PlainOverlap(t *testing.T) {
	acc := "abcdefghijklmnopqrstuvwxyz0123"
	// next 与 acc 末尾 "klmnopqrstuvwxyz0123" 重叠，再接新内容
	next := "klmnopqrstuvwxyz0123ABCDEFG"
	got := AppendWithOverlap(acc, next, 20)
	want := "abcdefghijklmnopqrstuvwxyz0123ABCDEFG"
	if got != want {
		t.Fatalf("plain overlap mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAppendWithOverlap_NoOverlap(t *testing.T) {
	acc := "hello world"
	next := "completely different"
	got := AppendWithOverlap(acc, next, 0)
	want := acc + next
	if got != want {
		t.Fatalf("no overlap mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestMergeTextChunks_OrdersFiltersAndStitches(t *testing.T) {
	header := "| a | b |\n|:--|:--|\n"
	chunks := []*types.Chunk{
		{
			Content: header + "| r1 | x |\n| r2 | y |\n", ChunkType: types.ChunkTypeText,
			StartAt: 0, EndAt: 20, ChunkIndex: 0,
		},
		{
			// 补写表头 + 与上一段 r2 重叠 + 新行 r3
			Content: header + "| r2 | y |\n| r3 | z |\n", ChunkType: types.ChunkTypeText,
			StartAt: 10, EndAt: 40, ChunkIndex: 1,
		},
	}
	got := MergeTextChunks(chunks, "\n")
	want := header + "| r1 | x |\n| r2 | y |\n" + "| r3 | z |\n"
	if got != want {
		t.Fatalf("MergeTextChunks mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestMergeTextChunks_GapSeparator(t *testing.T) {
	chunks := []*types.Chunk{
		{Content: "first", ChunkType: types.ChunkTypeText, StartAt: 0, EndAt: 5, ChunkIndex: 0},
		{Content: "second", ChunkType: types.ChunkTypeText, StartAt: 100, EndAt: 106, ChunkIndex: 1},
	}
	got := MergeTextChunks(chunks, "\n")
	want := "first\nsecond"
	if got != want {
		t.Fatalf("gap separator mismatch:\n got=%q\nwant=%q", got, want)
	}
}
