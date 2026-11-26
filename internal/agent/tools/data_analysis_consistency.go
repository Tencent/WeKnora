package tools

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// checkQueryResultConsistency inspects SQL query results for common statistical
// self-consistency anomalies (grand-total row vs detail sum mismatch, or a
// share/proportion column that does not sum to ~1 / ~100%). Warnings are purely
// advisory and never fail a query; filtered/limited queries are skipped to
// avoid false alarms on legitimate subsets.
func checkQueryResultConsistency(sqlQuery string, results []map[string]string) []string {
	if len(results) < 2 {
		return nil
	}
	hasLimit, hasWhere, ok := inspectQueryResultScope(sqlQuery)
	if !ok || hasLimit {
		return nil
	}

	cols := sortedResultColumns(results[0])
	if len(cols) == 0 {
		return nil
	}

	totalRows, totalLabel := findGrandTotalRows(results, cols)

	var warnings []string

	// 1. Reconcile a single grand-total row against the sum of detail rows.
	if len(results) >= 3 && len(totalRows) == 1 {
		totalIdx := totalRows[0]
		warnings = append(warnings, checkGrandTotalRow(results, cols, totalIdx, totalLabel)...)
	}

	// 2. Check full-partition share/proportion columns (unfiltered queries only).
	if !hasWhere && len(totalRows) <= 1 {
		detailRows := results
		if len(totalRows) == 1 {
			totalIdx := totalRows[0]
			detailRows = make([]map[string]string, 0, len(results)-1)
			for i, row := range results {
				if i != totalIdx {
					detailRows = append(detailRows, row)
				}
			}
		}
		if len(detailRows) >= 2 {
			warnings = append(warnings, checkShareColumnSums(detailRows, cols)...)
		}
	}

	return warnings
}

func inspectQueryResultScope(sqlQuery string) (hasLimit, hasWhere, ok bool) {
	tree, err := pg_query.Parse(sqlQuery)
	if err != nil || tree == nil || len(tree.Stmts) != 1 {
		return false, false, false
	}
	selectStmt := tree.Stmts[0].GetStmt().GetSelectStmt()
	if selectStmt == nil {
		return false, false, false
	}
	walkSelectScope(selectStmt, &hasLimit, &hasWhere)
	return hasLimit, hasWhere, true
}

func walkSelectScope(stmt *pg_query.SelectStmt, hasLimit, hasWhere *bool) {
	if stmt == nil {
		return
	}
	if stmt.LimitCount != nil || stmt.LimitOffset != nil {
		*hasLimit = true
	}
	if stmt.WhereClause != nil {
		*hasWhere = true
	}
	if stmt.WithClause != nil {
		for _, cteNode := range stmt.WithClause.Ctes {
			if cte := cteNode.GetCommonTableExpr(); cte != nil {
				if cteSelect := cte.GetCtequery().GetSelectStmt(); cteSelect != nil {
					var cteLimit bool
					walkSelectScope(cteSelect, &cteLimit, hasWhere)
					if cteLimit {
						*hasLimit = true
					}
				}
			}
		}
	}
	for _, fromNode := range stmt.FromClause {
		if sub := fromNode.GetRangeSubselect(); sub != nil {
			if subSelect := sub.GetSubquery().GetSelectStmt(); subSelect != nil {
				walkSelectScope(subSelect, hasLimit, hasWhere)
			}
		}
	}
	if stmt.Larg != nil {
		walkSelectScope(stmt.Larg, hasLimit, hasWhere)
	}
	if stmt.Rarg != nil {
		walkSelectScope(stmt.Rarg, hasLimit, hasWhere)
	}
}

func sortedResultColumns(firstRow map[string]string) []string {
	cols := make([]string, 0, len(firstRow))
	for col := range firstRow {
		if col == excelSheetNameColumn {
			continue
		}
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols
}

func findGrandTotalRows(results []map[string]string, cols []string) ([]int, string) {
	var indices []int
	label := ""
	for i, row := range results {
		for _, col := range cols {
			val := strings.TrimSpace(row[col])
			if isGrandTotalLabel(val) {
				indices = append(indices, i)
				if label == "" {
					label = val
				}
				break
			}
		}
	}
	return indices, label
}

func isGrandTotalLabel(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "合计", "总计", "总合计", "汇总", "total", "grand total":
		return true
	default:
		return false
	}
}

func checkGrandTotalRow(
	results []map[string]string, cols []string, totalIdx int, totalLabel string,
) []string {
	var warnings []string
	for _, col := range cols {
		if isNonAdditiveColumnName(col) {
			continue
		}
		totalVal, totalIsPct, ok := parseConsistencyNumber(results[totalIdx][col])
		if !ok {
			continue
		}
		detailSum := 0.0
		valid := true
		for i, row := range results {
			if i == totalIdx {
				continue
			}
			v, isPct, parsed := parseConsistencyNumber(row[col])
			if !parsed || isPct != totalIsPct {
				valid = false
				break
			}
			detailSum += v
		}
		if !valid {
			continue
		}

		tol := math.Max(0.01, 1e-4*math.Abs(totalVal))
		if totalIsPct {
			tol = math.Max(0.1, 1e-3*math.Abs(totalVal))
		}
		if math.Abs(detailSum-totalVal) > tol {
			suffix := ""
			if totalIsPct {
				suffix = "%"
			}
			warnings = append(warnings, fmt.Sprintf(
				"Column %q: sum of detail rows (%.4g%s) does not match the %q row (%.4g%s); "+
					"check for missing rows, subtotals, or double counting.",
				col, detailSum, suffix, totalLabel, totalVal, suffix,
			))
		}
	}
	return warnings
}

func checkShareColumnSums(detailRows []map[string]string, cols []string) []string {
	var warnings []string
	for _, col := range cols {
		if !isShareColumnName(col) {
			continue
		}
		sum := 0.0
		maxVal := 0.0
		count := 0
		pctMode := false
		pctKnown := false
		valid := true

		for _, row := range detailRows {
			raw := strings.TrimSpace(row[col])
			if raw == "" || strings.EqualFold(raw, "<nil>") || strings.EqualFold(raw, "null") {
				continue
			}
			v, isPct, ok := parseConsistencyNumber(raw)
			if !ok || v < 0 {
				valid = false
				break
			}
			if !pctKnown {
				pctMode = isPct
				pctKnown = true
			} else if isPct != pctMode {
				valid = false
				break
			}
			sum += v
			if v > maxVal {
				maxVal = v
			}
			count++
		}
		if !valid || count < 2 || maxVal == 0 {
			continue
		}

		target := 1.0
		tol := 0.01
		expectLabel := "~1.00"
		suffix := ""
		if pctMode {
			target = 100.0
			tol = 1.0
			expectLabel = "~100%"
			suffix = "%"
		} else if maxVal > 1.0+1e-6 {
			target = 100.0
			tol = 1.0
			expectLabel = "~100"
		}

		if math.Abs(sum-target) > tol {
			warnings = append(warnings, fmt.Sprintf(
				"Column %q: values sum to %.4g%s (expected %s for a complete share breakdown); "+
					"verify whether rows span multiple periods, are filtered, or include subtotals/totals.",
				col, sum, suffix, expectLabel,
			))
		}
	}
	return warnings
}

func parseConsistencyNumber(raw string) (val float64, isPct, ok bool) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" || strings.EqualFold(cleaned, "<nil>") || strings.EqualFold(cleaned, "null") {
		return 0, false, false
	}
	if strings.HasSuffix(cleaned, "%") {
		isPct = true
		cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "%"))
	}
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false, false
	}
	return v, isPct, true
}

func isNonAdditiveColumnName(col string) bool {
	lower := strings.ToLower(strings.TrimSpace(col))
	if lower == "" {
		return false
	}
	// Share columns like "占比" / "百分比" ARE additive across a partition.
	if isShareColumnName(col) {
		return false
	}
	for _, token := range []string{
		"avg", "mean", "median", "rate", "ratio", "margin", "growth", "price",
		"min", "max", "yoy", "mom", "discount", "tax",
		"均", "单价", "最小", "最大", "增长", "同比", "环比",
		"利率", "税率", "折扣", "毛利", "净利",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return strings.HasSuffix(lower, "率")
}

func isShareColumnName(col string) bool {
	lower := strings.ToLower(strings.TrimSpace(col))
	if lower == "" {
		return false
	}
	for _, excluded := range []string{
		"rate", "ratio", "margin", "growth", "change", "yoy", "mom",
		"avg", "mean", "discount", "tax", "price", "count", "qty", "num",
		"holder", "stock", "link", "url", "code", "name",
		"interest", "fee", "error", "pass", "fail", "win", "hit",
		"completion", "utilization", "occupancy", "conversion", "retention", "churn",
		"同比", "环比", "增长", "利率", "税率", "折扣", "毛利", "净利",
		"均", "变化", "涨幅", "跌幅", "单价", "股价", "股数",
	} {
		if strings.Contains(lower, excluded) {
			return false
		}
	}
	for _, token := range []string{
		"占比", "比重", "份额", "百分比", "share", "proportion", "pct", "percent",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}
