package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

var databaseQueryTool = BaseTool{
	name: ToolDatabaseQuery,
	description: `Execute SQL queries to retrieve information from the database.

## Security Features
- Automatic tenant_id injection: All queries are automatically filtered by the logged-in user's tenant_id
- Read-only queries: Only SELECT statements are allowed
- Safe tables: Only allow queries on authorized tables (knowledge_bases, knowledges, chunks)

## Available Tables and Columns

### knowledge_bases
- id (VARCHAR): Knowledge base ID
- name (VARCHAR): Knowledge base name
- description (TEXT): Description
- tenant_id (INTEGER): Owner tenant ID
- embedding_model_id, summary_model_id, rerank_model_id (VARCHAR): Model IDs
- vlm_config (JSON): Includes VLM settings such as enabled flag and model_id
- created_at, updated_at (TIMESTAMP)

### knowledges (documents)
- id (VARCHAR): Document ID
- tenant_id (INTEGER): Owner tenant ID
- knowledge_base_id (VARCHAR): Parent knowledge base ID
- type (VARCHAR): Document type
- title (VARCHAR): Document title
- description (TEXT): Description
- source (VARCHAR): Source location
- parse_status (VARCHAR): Processing status (unprocessed/processing/completed/failed)
- enable_status (VARCHAR): Enable status (enabled/disabled)
- file_name, file_type (VARCHAR): File information
- file_size, storage_size (BIGINT): Size in bytes
- created_at, updated_at, processed_at (TIMESTAMP)



### chunks
- id (VARCHAR): Chunk ID
- tenant_id (INTEGER): Owner tenant ID
- knowledge_base_id (VARCHAR): Parent knowledge base ID
- knowledge_id (VARCHAR): Parent document ID
- content (TEXT): Chunk content
- chunk_index (INTEGER): Index in document
- is_enabled (BOOLEAN): Enable status
- chunk_type (VARCHAR): Type (text/image/table)
- created_at, updated_at (TIMESTAMP)

## Usage Examples

Query knowledge base information:
{
  "sql": "SELECT id, name, description FROM knowledge_bases ORDER BY created_at DESC LIMIT 10"
}

Count documents by status:
{
  "sql": "SELECT parse_status, COUNT(*) as count FROM knowledges GROUP BY parse_status"
}

Find recent sessions:
{
  "sql": "SELECT id, title, created_at FROM sessions ORDER BY created_at DESC LIMIT 5"
}

Get storage usage:
{
  "sql": "SELECT SUM(storage_size) as total_storage FROM knowledges"
}

Join knowledge bases and documents:
{
  "sql": "SELECT kb.name as kb_name, COUNT(k.id) as doc_count FROM knowledge_bases kb LEFT JOIN knowledges k ON kb.id = k.knowledge_base_id GROUP BY kb.id, kb.name"
}

## Important Notes
- DO NOT include tenant_id in WHERE clause - it's automatically added
- Only SELECT queries are allowed
- Limit results with LIMIT clause for better performance
- Use appropriate JOINs when querying across tables
- All timestamps are in UTC with time zone`,
	schema: utils.GenerateSchema[DatabaseQueryInput](),
}

type DatabaseQueryInput struct {
	SQL string `json:"sql" jsonschema:"The SELECT SQL query to execute. DO NOT include tenant_id condition - it will be automatically added for security."`
}

// DatabaseQueryTool allows AI to query the database with auto-injected tenant_id for security
type DatabaseQueryTool struct {
	BaseTool
	db *gorm.DB
}

// NewDatabaseQueryTool creates a new database query tool
func NewDatabaseQueryTool(db *gorm.DB) *DatabaseQueryTool {
	return &DatabaseQueryTool{
		BaseTool: databaseQueryTool,
		db:       db,
	}
}

// Execute executes the database query tool
func (t *DatabaseQueryTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][DatabaseQuery] Execute started")

	tenantID := uint64(0)
	if tid, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
		tenantID = tid
	}

	// Parse args from json.RawMessage
	var input DatabaseQueryInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][DatabaseQuery] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Extract SQL from input
	if input.SQL == "" {
		logger.Errorf(ctx, "[Tool][DatabaseQuery] Missing or invalid SQL parameter")
		return &types.ToolResult{
			Success: false,
			Error:   "Missing or invalid 'sql' parameter",
		}, fmt.Errorf("missing sql parameter")
	}

	logger.Infof(ctx, "[Tool][DatabaseQuery] Original SQL query:\n%s", input.SQL)
	logger.Infof(ctx, "[Tool][DatabaseQuery] Tenant ID: %d", tenantID)

	// Validate and secure the SQL query
	logger.Debugf(ctx, "[Tool][DatabaseQuery] Validating and securing SQL...")
	securedSQL, err := t.validateAndSecureSQL(input.SQL, tenantID)
	if err != nil {
		logger.Errorf(ctx, "[Tool][DatabaseQuery] SQL validation failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("SQL validation failed: %v", err),
		}, err
	}

	logger.Infof(ctx, "[Tool][DatabaseQuery] Secured SQL query:\n%s", securedSQL)
	logger.Infof(ctx, "Executing secured SQL query - original: %s, secured: %s, tenant_id: %d",
		input.SQL, securedSQL, tenantID)

	// Execute the query
	logger.Infof(ctx, "[Tool][DatabaseQuery] Executing query against database...")
	rows, err := t.db.WithContext(ctx).Raw(securedSQL).Rows()
	if err != nil {
		logger.Errorf(ctx, "[Tool][DatabaseQuery] Query execution failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Query execution failed: %v", err),
		}, err
	}
	defer rows.Close()

	logger.Debugf(ctx, "[Tool][DatabaseQuery] Query executed successfully, processing rows...")

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to get columns: %v", err),
		}, err
	}

	// Process results
	results := make([]map[string]interface{}, 0)
	for rows.Next() {
		// Create a slice of interface{} to hold each column value
		columnValues := make([]interface{}, len(columns))
		columnPointers := make([]interface{}, len(columns))
		for i := range columnValues {
			columnPointers[i] = &columnValues[i]
		}

		// Scan the row
		if err := rows.Scan(columnPointers...); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Failed to scan row: %v", err),
			}, err
		}

		// Create a map for this row
		rowMap := make(map[string]interface{})
		for i, colName := range columns {
			val := columnValues[i]
			// Convert []byte to string for better readability
			if b, ok := val.([]byte); ok {
				rowMap[colName] = string(b)
			} else {
				rowMap[colName] = val
			}
		}
		results = append(results, rowMap)
	}

	if err := rows.Err(); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Error iterating rows: %v", err),
		}, err
	}

	logger.Infof(ctx, "[Tool][DatabaseQuery] Retrieved %d rows with %d columns", len(results), len(columns))
	logger.Debugf(ctx, "[Tool][DatabaseQuery] Columns: %v", columns)

	// Log first few rows for debugging
	if len(results) > 0 {
		logger.Debugf(ctx, "[Tool][DatabaseQuery] First row sample:")
		for key, value := range results[0] {
			logger.Debugf(ctx, "[Tool][DatabaseQuery]   %s: %v", key, value)
		}
	}

	// Format output
	logger.Debugf(ctx, "[Tool][DatabaseQuery] Formatting query results...")
	output := t.formatQueryResults(columns, results, securedSQL)

	logger.Infof(ctx, "[Tool][DatabaseQuery] Execute completed successfully: %d rows returned", len(results))
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"columns":      columns,
			"rows":         results,
			"row_count":    len(results),
			"query":        securedSQL,
			"tenant_id":    tenantID,
			"display_type": "database_query",
		},
	}, nil
}

// validateAndSecureSQL validates the SQL query and injects tenant_id conditions
func (t *DatabaseQueryTool) validateAndSecureSQL(sqlQuery string, tenantID uint64) (string, error) {
	// Use the new ValidateAndSecureSQL with comprehensive security options
	securedSQL, validationResult, err := utils.ValidateAndSecureSQL(
		sqlQuery,
		utils.WithSecurityDefaults(tenantID),
		utils.WithInjectionRiskCheck(),
	)
	if err != nil {
		return "", err
	}

	if !validationResult.Valid {
		// Build error message from validation errors
		var errMsgs []string
		for _, valErr := range validationResult.Errors {
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %s", valErr.Type, valErr.Message))
		}
		return "", fmt.Errorf("validation failed: %s", strings.Join(errMsgs, "; "))
	}

	return securedSQL, nil
}

// formatQueryResults formats query results into readable text
func (t *DatabaseQueryTool) formatQueryResults(
	columns []string,
	results []map[string]interface{},
	query string,
) string {
	output := "=== 查询结果 ===\n\n"
	output += fmt.Sprintf("执行的SQL: %s\n\n", query)
	output += fmt.Sprintf("返回 %d 行数据\n\n", len(results))

	if len(results) == 0 {
		output += "未找到匹配的记录。\n"
		return output
	}

	output += "=== 数据详情 ===\n\n"

	// Format each row
	for i, row := range results {
		output += fmt.Sprintf("--- 记录 #%d ---\n", i+1)
		for _, col := range columns {
			value := row[col]
			// Format the value
			var formattedValue string
			if value == nil {
				formattedValue = "<NULL>"
			} else if jsonData, err := json.Marshal(value); err == nil {
				// Check if it's a complex type
				switch v := value.(type) {
				case string:
					formattedValue = v
				case []byte:
					formattedValue = string(v)
				default:
					formattedValue = string(jsonData)
				}
			} else {
				formattedValue = fmt.Sprintf("%v", value)
			}

			output += fmt.Sprintf("  %s: %s\n", col, formattedValue)
		}
		output += "\n"
	}

	// Add summary statistics if applicable
	if len(results) > 10 {
		output += fmt.Sprintf("注意: 显示了前 %d 条记录，共 %d 条。建议使用 LIMIT 子句限制结果数量。\n", len(results), len(results))
	}

	return output
}
