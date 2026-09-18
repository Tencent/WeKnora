package service

import (
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
)

// Regression guard for the central "normalize inline HTML tables before
// chunking" wiring. The bug this guards: normalization was only wired into the
// paddleocr-vl converters, so tables emitted by other engines (e.g. MinerU)
// reached the chunker as raw single-line <table> blocks and were force-cut at
// the absolute size limit.
//
// Coverage level: the two call sites live inline inside side-effectful
// unexported methods (processKnowledge / triggerManualProcessing), so the
// runtime behavior below asserts the docparser.NormalizeHTMLTables contract that
// those call sites rely on, and TestHTMLEmbeddedTableWiring_CallSitesPresent
// statically asserts the call sites exist at the chunking boundary.
func TestHTMLEmbeddedTableWiring_ProducesChunkableOutput(t *testing.T) {
	input := strings.Join([]string{
		"# 检测报告",
		"",
		"以下是正文段落，用于确认归一化不会破坏普通 markdown。",
		"",
		// colspan=1: redundant spans must still be convertible to GFM.
		`<table><tr><td colspan="1" style="text-align:left">项目</td><td colspan="1">结果</td></tr><tr><td>拉伸强度</td><td>合格</td></tr></table>`,
		"",
		// colspan=6: a real merge -> cannot be GFM, but must stay splittable.
		`<table><tr><td colspan="6" style="text-align:center">汇总</td></tr><tr><td colspan="6">备注</td></tr></table>`,
		"",
		"收尾段落。",
	}, "\n")

	got := docparser.NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		// Property (2): any remaining HTML table must have every <tr> preceded by
		// a newline so the chunker can split it.
		for i := 0; ; {
			idx := strings.Index(got[i:], "<tr")
			if idx < 0 {
				break
			}
			abs := i + idx
			if abs == 0 || got[abs-1] != '\n' {
				t.Fatalf("remaining <tr> at offset %d is not preceded by a newline:\n%s", abs, got)
			}
			i = abs + 1
		}
	} else {
		// Property (1): fully converted to GFM with a separator row.
		if !strings.Contains(got, "|") || !strings.Contains(got, "-") {
			t.Fatalf("expected GFM table output, got:\n%s", got)
		}
	}

	// The colspan=1 table must not survive as HTML.
	if strings.Contains(got, `colspan="1"`) {
		t.Fatalf("redundant colspan=1 table was not normalized:\n%s", got)
	}
}

// TestHTMLEmbeddedTableWiring_CallSitesPresent is a lightweight static guard:
// it fails if the central normalization call is removed from either pre-chunk
// location (the exact regression this change fixes).
func TestHTMLEmbeddedTableWiring_CallSitesPresent(t *testing.T) {
	call := "docparser.NormalizeHTMLTables("

	processSrc, err := os.ReadFile("knowledge_process.go")
	if err != nil {
		t.Fatalf("read knowledge_process.go: %v", err)
	}
	process := string(processSrc)
	processCall := strings.Index(process, call)
	if processCall < 0 {
		t.Fatalf("knowledge_process.go is missing the pre-chunk %s call", call)
	}
	// The chunker config is built immediately before chunking; the normalization
	// call must precede it. (Anchored on the chunking call rather than a comment,
	// so upstream comment reshuffles don't break this guard.)
	if cfg := strings.Index(process, "chunkCfg := buildSplitterConfigFromChunking"); cfg < 0 || processCall > cfg {
		t.Fatalf("knowledge_process.go: %s is not before the chunking step", call)
	}

	createSrc, err := os.ReadFile("knowledge_create.go")
	if err != nil {
		t.Fatalf("read knowledge_create.go: %v", err)
	}
	create := string(createSrc)
	createCall := strings.Index(create, call)
	if createCall < 0 {
		t.Fatalf("knowledge_create.go is missing the pre-chunk %s call", call)
	}
	// The splitter config is built immediately before chunking; the call must precede it.
	if cfg := strings.Index(create, "chunkCfg := buildSplitterConfigFromChunking"); cfg < 0 || createCall > cfg {
		t.Fatalf("knowledge_create.go: %s is not before buildSplitterConfigFromChunking", call)
	}
}
