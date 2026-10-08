package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconcileSQLPreservesNonIdentifiers(t *testing.T) {
	schema := &TableSchema{Columns: []ColumnInfo{{Name: "Order Status"}}}
	for _, literal := range []string{
		`'"orderstatus"'`,
		`'customer''s "orderstatus"'`,
		`$$"orderstatus"$$`,
		`$value$"orderstatus"$value$`,
		`E'customer\'s "orderstatus"'`,
	} {
		t.Run(literal, func(t *testing.T) {
			query := `SELECT "orderstatus" FROM dataset WHERE "orderstatus" = ` + literal
			want := `SELECT "Order Status" FROM dataset WHERE "Order Status" = ` + literal
			got, fixes := reconcileSQLColumnsWithSchema(query, schema)
			require.Equal(t, want, got)
			require.Len(t, fixes, 2)
		})
	}
	for _, comment := range []string{
		"-- \"orderstatus\"\n",
		`/* "orderstatus" */`,
		`/* outer /* "orderstatus" */ end */`,
	} {
		t.Run(comment, func(t *testing.T) {
			query := `SELECT ` + comment + ` "orderstatus" FROM dataset`
			got, fixes := reconcileSQLColumnsWithSchema(query, schema)
			require.Equal(t, `SELECT `+comment+` "Order Status" FROM dataset`, got)
			require.Len(t, fixes, 1)
		})
	}
}

func TestReconcileSQLPreservesQuotedIdentifierEscapes(t *testing.T) {
	schema := &TableSchema{Columns: []ColumnInfo{{Name: `Order "Status"`}, {Name: "订单 状态"}}}
	query := `SELECT "order""status""","订单状态" FROM dataset`
	got, fixes := reconcileSQLColumnsWithSchema(query, schema)
	require.Equal(t, `SELECT "Order ""Status""","订单 状态" FROM dataset`, got)
	require.Len(t, fixes, 2)
}

func TestReconcileSQLLeavesUnscannableInputUnchanged(t *testing.T) {
	schema := &TableSchema{Columns: []ColumnInfo{{Name: "Order Status"}}}
	for _, query := range []string{
		`SELECT "orderstatus" FROM dataset WHERE note = 'unfinished`,
		`SELECT "orderstatus`,
	} {
		got, fixes := reconcileSQLColumnsWithSchema(query, schema)
		require.Equal(t, query, got)
		require.Empty(t, fixes)
	}
}

func TestReconcileSQLPreservesDuckDBFilterResults(t *testing.T) {
	db := newPlainDuckDB(t)
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: db, sessionID: "literal-filter"}
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })
	path := writeCSV(t, "orders.csv", "Order Status,note",
		`paid,"""orderstatus"""`, `unpaid,"""Order Status"""`)
	schema, err := tool.LoadFromCSV(ctx, path, "k_literal_filter")
	require.NoError(t, err)
	query := `SELECT "orderstatus" AS status FROM dataset WHERE note = '"orderstatus"'`
	rewritten, _ := reconcileSQLColumnsWithSchema(query, schema)
	require.NoError(t, validateDataAnalysisSQL(rewritten, schema))
	rows, err := tool.executeSingleQuery(ctx, rewritten, schema.TableName)
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{"status": "paid"}}, rows)
}
