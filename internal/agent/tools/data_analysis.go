package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

var dataAnalysisTool = BaseTool{
	name:        ToolDataAnalysis,
	description: "Use this tool when the knowledge is CSV or Excel files. It loads the data into memory and executes SQL for data analysis. If the user's question requires data statistics, convert the question into SQL and execute it.",
	schema:      utils.GenerateSchema[DataAnalysisInput](),
}

type DataAnalysisInput struct {
	KnowledgeID string `json:"knowledge_id" jsonschema:"id of the knowledge to query"`
	Sql         string `json:"sql" jsonschema:"SQL to be executed on knowledge"`
}

type DataAnalysisTool struct {
	BaseTool
	knowledgeService interfaces.KnowledgeService
	db               *sql.DB
	sessionID        string
	createdTables    []string // Track tables created in this session
}

func NewDataAnalysisTool(knowledgeService interfaces.KnowledgeService, db *sql.DB, sessionID string) *DataAnalysisTool {
	return &DataAnalysisTool{
		BaseTool:         dataAnalysisTool,
		knowledgeService: knowledgeService,
		db:               db,
		sessionID:        sessionID,
	}
}

// recordCreatedTable records a table name for cleanup, ensuring uniqueness
// Returns true if the table was newly recorded, false if it already existed
func (t *DataAnalysisTool) recordCreatedTable(tableName string) bool {
	for _, name := range t.createdTables {
		if name == tableName {
			return false
		}
	}
	t.createdTables = append(t.createdTables, tableName)
	return true
}

// Cleanup cleans up the session-specific schema
func (t *DataAnalysisTool) Cleanup(ctx context.Context) {
	if len(t.createdTables) == 0 {
		logger.Infof(ctx, "[Tool][DataAnalysis] No tables to clean up for session: %s", t.sessionID)
		return
	}

	logger.Infof(ctx, "[Tool][DataAnalysis] Cleaning up %d tables for session: %s", len(t.createdTables), t.sessionID)

	for _, tableName := range t.createdTables {
		dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS \"%s\"", tableName)
		if _, err := t.db.ExecContext(ctx, dropSQL); err != nil {
			logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to drop table '%s': %v", tableName, err)
			// Continue to drop other tables even if one fails
			continue
		}
		logger.Infof(ctx, "[Tool][DataAnalysis] Successfully dropped table '%s'", tableName)
	}

	// Clear the list after cleanup
	t.createdTables = nil
}

// Execute executes the SQL query on DuckDB (only read-only queries are allowed)
func (t *DataAnalysisTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][DataAnalysis] Execute started for session: %s", t.sessionID)
	var input DataAnalysisInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to parse input args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse input args: %v", err),
		}, err
	}

	schema, err := t.LoadFromKnowledgeID(ctx, input.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to load knowledge ID '%s': %v", input.KnowledgeID, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to load knowledge ID '%s': %v", input.KnowledgeID, err),
		}, err
	}

	// Replace knowledge ID with table name
	input.Sql = strings.ReplaceAll(input.Sql, input.KnowledgeID, schema.TableName)

	// Check if this is a read-only query
	normalizedSQL := strings.TrimSpace(strings.ToLower(input.Sql))
	isReadOnly := strings.HasPrefix(normalizedSQL, "select") ||
		strings.HasPrefix(normalizedSQL, "show") ||
		strings.HasPrefix(normalizedSQL, "describe") ||
		strings.HasPrefix(normalizedSQL, "explain") ||
		strings.HasPrefix(normalizedSQL, "pragma")

	if !isReadOnly {
		// Reject modification queries
		logger.Warnf(ctx, "[Tool][DataAnalysis] Modification query rejected for session %s: %s", t.sessionID, input.Sql)
		return &types.ToolResult{
			Success: false,
			Error:   "DuckDB tool only supports read-only queries (SELECT, SHOW, DESCRIBE, EXPLAIN, PRAGMA). Modification operations (INSERT, UPDATE, DELETE, CREATE, DROP, etc.) are not allowed.",
		}, fmt.Errorf("modification queries are not allowed")
	}

	// Validate SQL with comprehensive security checks
	// IMPORTANT: Must enable validateSelectStmt to block RangeFunction attacks
	_, validation := utils.ValidateSQL(input.Sql,
		utils.WithAllowedTables(schema.TableName),
		utils.WithSingleStatement(),      // Block multiple statements
		utils.WithNoDangerousFunctions(), // Block dangerous functions
	)
	if !validation.Valid {
		logger.Warnf(ctx, "[Tool][DataAnalysis] SQL validation failed for session %s: %v", t.sessionID, validation.Errors)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("SQL validation failed: %v", validation.Errors),
		}, fmt.Errorf("SQL validation failed: %v", validation.Errors)
	}

	logger.Infof(ctx, "[Tool][DataAnalysis] Received SQL query for session %s: %s", t.sessionID, input.Sql)
	// Execute single query and get results
	results, err := t.executeSingleQuery(ctx, input.Sql)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Query execution failed: %v", err),
		}, err
	}

	queryOutput := t.formatQueryResults(results, input.Sql)
	logger.Infof(ctx, "[Tool][DataAnalysis] Completed execution query, total %d rows for session %s", len(results), t.sessionID)
	return &types.ToolResult{
		Success: true,
		Output:  queryOutput,
		Data: map[string]interface{}{
			"rows":         results,
			"row_count":    len(results),
			"query":        input.Sql,
			"display_type": ToolDataAnalysis,
			"session_id":   t.sessionID,
		},
	}, nil
}

// executeSingleQuery executes a single SQL query and returns columns and results
// Parameters:
//   - ctx: context for cancellation and timeout
//   - sqlQuery: the SQL query to execute
//   - existingColumns: existing column names to merge with (can be nil or empty)
//
// Returns:
//   - []string: merged column names (existing + new columns, deduplicated)
//   - []map[string]string: query results
//   - error: any error that occurred during execution
func (t *DataAnalysisTool) executeSingleQuery(ctx context.Context, sqlQuery string) ([]map[string]string, error) {
	rows, err := t.db.QueryContext(ctx, sqlQuery)
	if err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Query execution failed: %v", err)
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to get columns: %v", err)
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Process results
	results := make([]map[string]string, 0)
	for rows.Next() {
		columnValues := make([]interface{}, len(columns))
		columnPointers := make([]interface{}, len(columns))
		for i := range columnValues {
			columnPointers[i] = &columnValues[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to scan row: %v", err)
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		rowMap := make(map[string]string)
		for i, colName := range columns {
			val := columnValues[i]
			// Convert []byte to string for better readability
			if b, ok := val.([]byte); ok {
				rowMap[colName] = string(b)
			} else {
				rowMap[colName] = fmt.Sprintf("%v", val)
			}
		}
		results = append(results, rowMap)
	}

	if err := rows.Err(); err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Error iterating rows: %v", err)
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// formatQueryResults formats query results into JSONL format (one JSON object per line)
func (t *DataAnalysisTool) formatQueryResults(results []map[string]string, query string) string {
	var output strings.Builder

	output.WriteString("=== DuckDB 查询结果 ===\n\n")
	output.WriteString(fmt.Sprintf("执行的SQL: %s\n\n", query))
	output.WriteString(fmt.Sprintf("返回 %d 行数据\n\n", len(results)))

	if len(results) == 0 {
		output.WriteString("未找到匹配的记录。\n")
		return output.String()
	}

	output.WriteString("=== 数据详情 ===\n\n")
	if len(results) > 10 {
		output.WriteString(fmt.Sprintf("显示了所有 %d 条记录。建议使用 LIMIT 子句限制结果数量以提高性能。\n\n", len(results)))
	}

	// Write each record as a separate JSON line
	for i, record := range results {
		recordBytes, _ := json.Marshal(record)

		// Remove the trailing newline added by Encode
		recordStr := strings.Trim(string(recordBytes), "\n")
		output.WriteString(fmt.Sprintf("record %d: %s\n", i+1, recordStr))
	}

	return output.String()
}

// TableSchema represents the schema information of a table
type TableSchema struct {
	TableName string                 `json:"table_name"`
	Columns   []ColumnInfo           `json:"columns"`
	RowCount  int64                  `json:"row_count"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ColumnInfo represents information about a single column
type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
}

// LoadFromCSV loads data from a CSV file into a DuckDB table and returns the table schema
// Parameters:
//   - ctx: context for cancellation and timeout
//   - filename: path to the CSV file
//   - tableName: name of the table to create
//
// Returns:
//   - *TableSchema: schema information of the created table
//   - error: any error that occurred during the operation
func (t *DataAnalysisTool) LoadFromCSV(ctx context.Context, filename string, tableName string) (*TableSchema, error) {
	logger.Infof(ctx, "[Tool][DataAnalysis] Loading CSV file '%s' into table '%s' for session %s", filename, tableName, t.sessionID)

	// Record the created table for cleanup. If already exists, skip creation
	if t.recordCreatedTable(tableName) {
		// Create table from CSV using DuckDB's read_csv_auto function
		// Table will be created in the session schema
		createTableSQL := fmt.Sprintf("CREATE TABLE \"%s\" AS SELECT * FROM read_csv_auto('%s')", tableName, filename)

		_, err := t.db.ExecContext(ctx, createTableSQL)
		if err != nil {
			logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to create table from CSV: %v", err)
			return nil, fmt.Errorf("failed to create table from CSV: %w", err)
		}

		logger.Infof(ctx, "[Tool][DataAnalysis] Successfully created table '%s' from CSV file in session %s", tableName, t.sessionID)
	}

	// Get and return the table schema
	return t.LoadFromTable(ctx, tableName)
}

// LoadFromExcel loads data from an Excel file into a DuckDB table and returns the table schema
// Parameters:
//   - ctx: context for cancellation and timeout
//   - filename: path to the Excel file
//   - tableName: name of the table to create
//
// Returns:
//   - *TableSchema: schema information of the created table
//   - error: any error that occurred during the operation
//
// Note: This function requires the spatial extension to be installed in DuckDB
func (t *DataAnalysisTool) LoadFromExcel(ctx context.Context, filename string, tableName string) (*TableSchema, error) {
	logger.Infof(ctx, "[Tool][DataAnalysis] Loading Excel file '%s' into table '%s' for session %s", filename, tableName, t.sessionID)

	// Record the created table for cleanup. If already exists, skip creation
	if t.recordCreatedTable(tableName) {
		// Try to read Excel file using st_read (from spatial extension)
		// If spatial extension doesn't support Excel, we'll need to convert to CSV first
		createTableSQL := fmt.Sprintf("CREATE TABLE \"%s\" AS SELECT * FROM st_read('%s')", tableName, filename)

		_, err := t.db.ExecContext(ctx, createTableSQL)
		if err != nil {
			logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to create table from Excel: %v", err)
			return nil, fmt.Errorf("failed to create table from Excel file. Consider converting to CSV first: %w", err)
		}

		logger.Infof(ctx, "[Tool][DataAnalysis] Successfully created table '%s' from Excel file in session %s", tableName, t.sessionID)
	}

	// Get and return the table schema
	return t.LoadFromTable(ctx, tableName)
}

// LoadFromKnowledge loads data from a Knowledge entity into a DuckDB table and returns the table schema
// It automatically determines the file type and calls the appropriate loading method
// Parameters:
//   - ctx: context for cancellation and timeout
//   - knowledge: the Knowledge entity containing file information
//
// Returns:
//   - *TableSchema: schema information of the created table
//   - error: any error that occurred during the operation
func (t *DataAnalysisTool) LoadFromKnowledge(ctx context.Context, knowledge *types.Knowledge) (*TableSchema, error) {
	if knowledge == nil {
		return nil, fmt.Errorf("knowledge cannot be nil")
	}
	tableName := t.TableName(knowledge)

	// Normalize file type to lowercase for comparison
	fileType := strings.ToLower(knowledge.FileType)

	logger.Infof(ctx, "[Tool][DataAnalysis] Loading knowledge '%s' (type: %s) into table '%s' for session %s",
		knowledge.ID, fileType, tableName, t.sessionID)

	switch fileType {
	case "csv":
		return t.LoadFromCSV(ctx, knowledge.FilePath, tableName)
	case "xlsx", "xls":
		return t.LoadFromExcel(ctx, knowledge.FilePath, tableName)
	default:
		logger.Warnf(ctx, "[Tool][DataAnalysis] Unsupported file type '%s' for knowledge '%s' in session %s",
			fileType, knowledge.ID, t.sessionID)
		return nil, fmt.Errorf("unsupported file type: %s (supported types: csv, xlsx, xls)", fileType)
	}
}

// LoadFromKnowledgeID loads data from a Knowledge ID into a DuckDB table and returns the table schema
// Parameters:
//   - ctx: context for cancellation and timeout
//   - knowledgeID: the ID of the Knowledge entity
//
// Returns:
//   - string: the name of the created table
//   - *TableSchema: schema information of the created table
//   - error: any error that occurred during the operation
func (t *DataAnalysisTool) LoadFromKnowledgeID(ctx context.Context, knowledgeID string) (*TableSchema, error) {
	knowledge, err := t.knowledgeService.GetKnowledgeByID(ctx, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to get knowledge by ID '%s': %v", knowledgeID, err)
		return nil, fmt.Errorf("failed to get knowledge by ID: %w", err)
	}

	return t.LoadFromKnowledge(ctx, knowledge)
}

// LoadFromTable retrieves the schema information of an existing table
// Parameters:
//   - ctx: context for cancellation and timeout
//   - tableName: name of the table to query
//
// Returns:
//   - *TableSchema: schema information of the table
//   - error: any error that occurred during the operation
//
// Note: This function does NOT create the table, it only retrieves schema information
func (t *DataAnalysisTool) LoadFromTable(ctx context.Context, tableName string) (*TableSchema, error) {
	logger.Infof(ctx, "[Tool][DataAnalysis] Getting schema for table '%s' in session %s", tableName, t.sessionID)

	// Query to get column information using PRAGMA table_info or DESCRIBE
	schemaSQL := fmt.Sprintf("DESCRIBE \"%s\"", tableName)

	rows, err := t.db.QueryContext(ctx, schemaSQL)
	if err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to get table schema: %v", err)
		return nil, fmt.Errorf("failed to get table schema: %w", err)
	}
	defer rows.Close()

	// Parse column information
	columns := make([]ColumnInfo, 0)
	for rows.Next() {
		var colName, colType, nullable string
		var extra1, extra2, extra3 interface{} // DuckDB DESCRIBE may return additional columns

		// Try to scan with different column counts
		err := rows.Scan(&colName, &colType, &nullable, &extra1, &extra2, &extra3)
		if err != nil {
			// Try with fewer columns
			err = rows.Scan(&colName, &colType, &nullable)
			if err != nil {
				logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to scan column info: %v", err)
				return nil, fmt.Errorf("failed to scan column info: %w", err)
			}
		}

		columns = append(columns, ColumnInfo{
			Name:     colName,
			Type:     colType,
			Nullable: nullable,
		})
	}

	if err := rows.Err(); err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Error iterating schema rows: %v", err)
		return nil, fmt.Errorf("error iterating schema rows: %w", err)
	}

	// Get row count
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", tableName)
	var rowCount int64
	if err := t.db.QueryRowContext(ctx, countSQL).Scan(&rowCount); err != nil {
		logger.Errorf(ctx, "[Tool][DataAnalysis] Failed to get row count: %v", err)
		return nil, fmt.Errorf("failed to get row count: %w", err)
	}

	schema := &TableSchema{
		TableName: tableName,
		Columns:   columns,
		RowCount:  rowCount,
		Metadata: map[string]interface{}{
			"column_count": len(columns),
			"session_id":   t.sessionID,
		},
	}

	logger.Infof(ctx, "[Tool][DataAnalysis] Retrieved schema for table '%s' in session %s: %d columns, %d rows",
		tableName, t.sessionID, len(columns), rowCount)

	return schema, nil
}

func (t *DataAnalysisTool) TableName(knowledge *types.Knowledge) string {
	return "k_" + strings.ReplaceAll(knowledge.ID, "-", "_")
}

// buildSchemaDescription builds a formatted schema description
func (t *TableSchema) Description() string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("表名: %s\n", t.TableName))
	builder.WriteString(fmt.Sprintf("列数: %d\n", len(t.Columns)))
	builder.WriteString(fmt.Sprintf("行数: %d\n\n", t.RowCount))
	builder.WriteString("列信息:\n")

	for _, col := range t.Columns {
		builder.WriteString(fmt.Sprintf("- %s (%s)\n", col.Name, col.Type))
	}

	return builder.String()
}
