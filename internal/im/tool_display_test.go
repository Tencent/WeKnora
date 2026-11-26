package im

import (
	"strings"
	"testing"
)

func TestFormatIMToolLine_pendingWithQuery(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName:  "knowledge_search",
		Pending:   true,
		Arguments: map[string]any{"query": "文明6"},
	})
	if line != "正在调用 知识库检索..." {
		t.Fatalf("pending line = %q", line)
	}
}

func TestFormatIMToolLine_searchDoneWithQueryAndSummary(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "knowledge_search",
		Success:  true,
		Arguments: map[string]any{
			"query": "文明6",
		},
		Data: map[string]interface{}{
			// search_knowledge emits this slice type. The IM event keeps it;
			// there is no JSON round-trip. count is a Go int, kb_counts a map[string]int.
			"results": []map[string]interface{}{
				{"knowledge_id": "doc-a", "knowledge_title": "A.pdf"},
				{"knowledge_id": "doc-b", "knowledge_title": "B.pdf"},
				{"knowledge_id": "doc-b", "knowledge_title": "B.pdf"},
			},
			"count": 3,
			// One knowledge base. The caption must count the two documents.
			"kb_counts": map[string]int{"kb-1": 3},
		},
	})
	if !strings.Contains(line, "检索知识库：「文明6」") {
		t.Fatalf("title missing query: %q", line)
	}
	if !strings.Contains(line, "找到 3 个结果，来自 2 个文件") {
		t.Fatalf("summary missing: %q", line)
	}
}

func TestFormatIMToolLine_searchFileCountIgnoresKnowledgeBaseKeys(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "search_knowledge",
		Success:  true,
		Data: map[string]interface{}{
			"results": []map[string]interface{}{
				{"knowledge_id": "a", "knowledge_title": "步步高-数学-讲义.pdf"},
				{"knowledge_id": "b", "knowledge_title": "53高中知识清单.pdf"},
				{"knowledge_id": "c", "knowledge_title": "精讲册.pdf"},
			},
			"count":     3,
			"kb_counts": map[string]int{"8d2ce368": 3},
		},
	})
	if !strings.Contains(line, "找到 3 个结果，来自 3 个文件") {
		t.Fatalf("summary = %q", line)
	}
}

func TestFormatIMToolLine_searchFileCountFallsBackToTitle(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "knowledge_search",
		Success:  true,
		Data: map[string]interface{}{
			"results": []map[string]interface{}{
				{"knowledge_id": "  ", "knowledge_title": "A.pdf"},
				{"knowledge_title": "B.pdf"},
			},
			"count":     2,
			"kb_counts": map[string]int{"only": 2},
		},
	})
	if !strings.Contains(line, "找到 2 个结果，来自 2 个文件") {
		t.Fatalf("summary = %q", line)
	}
}

func TestFormatIMToolLine_searchWithoutDocumentIdentityOmitsFileCount(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "knowledge_search",
		Success:  true,
		Data: map[string]interface{}{
			"results":   []map[string]interface{}{{}, {}, {}},
			"count":     3,
			"kb_counts": map[string]int{"a": 1, "b": 1},
		},
	})
	if strings.Contains(line, "个文件") {
		t.Fatalf("kb_counts must not be reported as files: %q", line)
	}
	if !strings.Contains(line, "找到 3 个结果") {
		t.Fatalf("summary = %q", line)
	}
}

func TestFormatIMToolLine_searchJSONResultsStillCountFiles(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "search_knowledge",
		Success:  true,
		Data: map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{"knowledge_id": "doc-a", "knowledge_title": "A.pdf"},
				map[string]interface{}{"knowledge_id": "doc-b", "knowledge_title": "B.pdf"},
			},
			"kb_counts": map[string]interface{}{"kb-1": 2},
		},
	})
	if !strings.Contains(line, "找到 2 个结果，来自 2 个文件") {
		t.Fatalf("summary = %q", line)
	}
}

func TestFormatIMToolLine_searchFileCountSkipsWebPages(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "knowledge_search",
		Success:  true,
		Data: map[string]interface{}{
			"search_source": "mixed",
			"doc_count":     2,
			"web_count":     1,
			"count":         3,
			"results": []map[string]interface{}{
				{"knowledge_id": "doc-a", "knowledge_title": "A.pdf", "chunk_type": "text"},
				{"knowledge_id": "doc-a", "knowledge_title": "A.pdf", "chunk_type": "text"},
				{"knowledge_id": "https://example.com/page", "knowledge_title": "A page", "chunk_type": "web_search"},
			},
			"kb_counts": map[string]int{"kb-1": 2},
		},
	})
	if !strings.Contains(line, "找到 3 个结果，来自 1 个文件") {
		t.Fatalf("summary = %q", line)
	}
}

func TestFormatIMToolLine_grepPatterns(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "grep_chunks",
		Success:  true,
		Arguments: map[string]any{
			"patterns": []any{"文明", "策略"},
		},
		Data: map[string]interface{}{
			"total_matches":  float64(5),
			"document_count": float64(2),
		},
	})
	if line != "搜索关键词：「文明、策略」 · 找到 5 个匹配片段，来自 2 个文档" {
		t.Fatalf("grep line = %q", line)
	}
}

func TestFormatIMRagPipelineLine_queryUnderstand(t *testing.T) {
	pending := FormatIMRagPipelineLine(IMToolStep{
		ToolName: "query_understand",
		Pending:  true,
	})
	if pending != "正在理解问题..." {
		t.Fatalf("pending = %q", pending)
	}
	done := FormatIMRagPipelineLine(IMToolStep{
		ToolName: "query_understand",
		Success:  true,
	})
	if done != "已完成问题理解" {
		t.Fatalf("done = %q", done)
	}
}

func TestFormatIMRagPipelineLine_searchWithQuery(t *testing.T) {
	line := FormatIMRagPipelineLine(IMToolStep{
		ToolName:  "knowledge_search",
		Pending:   true,
		Arguments: map[string]any{"query": "讯飞开放平台"},
	})
	if line != "正在检索知识库：「讯飞开放平台」" {
		t.Fatalf("line = %q", line)
	}
}

func TestFormatIMRagPipelineLine_webSearchWithQuery(t *testing.T) {
	line := FormatIMRagPipelineLine(IMToolStep{
		ToolName:  "knowledge_search",
		Pending:   true,
		Arguments: map[string]any{"query": "任素汐演唱会", "search_source": "web"},
	})
	if line != "正在检索网络：「任素汐演唱会」" {
		t.Fatalf("line = %q", line)
	}
}

func TestIMGetQueryText_joinsUniqueQueries(t *testing.T) {
	got := imGetQueryText(map[string]any{
		"query":   "foo",
		"queries": []any{"foo", "bar"},
	})
	if got != "foo，bar" {
		t.Fatalf("query text = %q", got)
	}
}

func TestFormatIMToolLine_writeSandboxPendingShowsDiffStat(t *testing.T) {
	line := FormatIMToolLine(IMToolStep{
		ToolName: "write_sandbox_file",
		Pending:  true,
		Arguments: map[string]any{
			"path":          "/workspace/output/a.py",
			"added_lines":   12,
			"removed_lines": 0,
		},
	})
	if line != "写入沙箱文件：「/workspace/output/a.py」... +12" {
		t.Fatalf("pending write line = %q", line)
	}
}

func TestFormatMCPToolName_capitalizesWholeFirstRune(t *testing.T) {
	cases := map[string]string{
		"mcp_search_docs":  "Search Docs",
		"mcp_查询_search":    "查询 Search",
		"mcp_élan_vital":   "Élan Vital",
		"mcp__leading_gap": " Leading Gap",
	}
	for raw, want := range cases {
		if got := formatMCPToolName(raw); got != want {
			t.Errorf("formatMCPToolName(%q) = %q, want %q", raw, got, want)
		}
	}
}
