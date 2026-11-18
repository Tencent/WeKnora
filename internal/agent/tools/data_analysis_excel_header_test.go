package tools

import (
	"strings"
	"testing"
)

func TestBuildExcelCreateTableSQLWithoutSkipKeepsLegacyForm(t *testing.T) {
	plan := excelHeaderPlan{}
	got := buildExcelCreateTableSQLWithHeaderPlan(`tbl`, `/tmp/data.xlsx`, []string{`Sheet1`}, plan)
	want := buildExcelCreateTableSQL(`tbl`, `/tmp/data.xlsx`, []string{`Sheet1`})
	if got != want {
		t.Fatalf(`legacy form changed: got=%s want=%s`, got, want)
	}
}

func TestBuildExcelCreateTableSQLSkipsMergedTitleRow(t *testing.T) {
	plan := excelHeaderPlan{Skip: 1, Headers: []string{`月份`, `地区`, `销售额`}}
	got := buildExcelCreateTableSQLWithHeaderPlan(`tbl`, `/tmp/data.xlsx`, []string{`Sheet1`}, plan)
	dq := string(rune(34))
	if !strings.Contains(got, `header=false`) {
		t.Fatalf(`merged-title skip must read with header=false, got: %s`, got)
	}
	if !strings.Contains(got, `col0 AS `+dq+`月份`+dq) || !strings.Contains(got, `col2 AS `+dq+`销售额`+dq) {
		t.Fatalf(`header row values must become quoted column aliases, got: %s`, got)
	}
	if !strings.Contains(got, `WHERE __excel_row > 1`) {
		t.Fatalf(`expected row filter skipping the title row, got: %s`, got)
	}
	if strings.Contains(got, `header=true`) {
		t.Fatalf(`skip path must not double-trust the first row, got: %s`, got)
	}
}

func TestBuildExcelCreateTableSQLNoSheetSkipUsesNumberedColumns(t *testing.T) {
	plan := excelHeaderPlan{Skip: 1, Headers: []string{`a`, `b`}}
	got := buildExcelCreateTableSQLWithHeaderPlan(`tbl`, `/tmp/data.xlsx`, nil, plan)
	dq := string(rune(34))
	if !strings.Contains(got, `header=false`) || !strings.Contains(got, `col1 AS `+dq+`b`+dq) {
		t.Fatalf(`no-sheet skip path must promote numbered columns, got: %s`, got)
	}
}

func TestDetectExcelHeaderPlanMergedTitle(t *testing.T) {
	plan := detectExcelHeaderPlan([][]string{
		{`2026 年度销售统计`, ``, ``, ``},
		{`月份`, `地区`, `销售额`, `占比`},
		{`1月`, `华东`, `100`, `0.2`},
	})
	if plan.Skip != 1 || len(plan.Headers) != 4 || plan.Headers[0] != `月份` {
		t.Fatalf(`merged title row must be skipped, got %+v`, plan)
	}
}

func TestDetectExcelHeaderPlanPlainHeader(t *testing.T) {
	plan := detectExcelHeaderPlan([][]string{
		{`月份`, `地区`, `销售额`},
		{`1月`, `华东`, `100`},
	})
	if plan.Skip != 0 || plan.Headers != nil {
		t.Fatalf(`plain header must keep the legacy path, got %+v`, plan)
	}
}
