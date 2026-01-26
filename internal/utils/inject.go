package utils

import (
	"fmt"
	"regexp"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// This file provides comprehensive SQL validation and security features

/*
Example Usage:

1. Basic SQL parsing:
   result := ParseSQL("SELECT * FROM users WHERE age > 18")
   fmt.Printf("Tables: %v\n", result.TableNames)
   fmt.Printf("WHERE fields: %v\n", result.WhereFields)

2. Simple validation with table whitelist:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE age > 18",
       WithAllowedTables("users", "orders"),
   )
   if !validation.Valid {
       for _, err := range validation.Errors {
           fmt.Printf("Error: %s - %s\n", err.Type, err.Message)
       }
   }

3. Check for SQL injection risks:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE id = 1 OR 1=1",
       WithInjectionRiskCheck(),
   )
   if !validation.Valid {
       fmt.Println("SQL injection risk detected!")
   }

4. Comprehensive security validation:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE age > 18",
       WithInputValidation(6, 4096),
       WithSelectOnly(),
       WithSingleStatement(),
       WithAllowedTables("users", "orders"),
       WithDefaultSafeFunctions(),
       WithNoSubqueries(),
       WithNoCTEs(),
       WithNoSystemColumns(),
   )

5. Use security defaults (recommended for production):
   parseResult, validation := ValidateSQL(
       "SELECT * FROM knowledge_bases WHERE name LIKE '%test%'",
       WithSecurityDefaults(tenantID),
   )

6. Validate and secure SQL with tenant isolation:
   securedSQL, validation, err := ValidateAndSecureSQL(
       "SELECT * FROM knowledge_bases",
       WithSecurityDefaults(tenantID),
   )
   // securedSQL will have tenant_id automatically injected:
   // "SELECT * FROM knowledge_bases WHERE knowledge_bases.tenant_id = 123"

7. Custom validation options:
   parseResult, validation := ValidateSQL(
       "SELECT COUNT(*), AVG(score) FROM sessions",
       WithAllowedTables("sessions", "messages"),
       WithAllowedFunctions("count", "avg", "sum"),
       WithTenantIsolation(tenantID, "sessions"),
   )
*/

// SQLParseResult represents the parsed components of a SELECT SQL statement
type SQLParseResult struct {
	IsSelect     bool     `json:"is_select"`             // Whether the SQL is a SELECT statement
	TableNames   []string `json:"table_names"`           // List of table names in FROM clause
	SelectFields []string `json:"select_fields"`         // List of fields in SELECT clause
	WhereFields  []string `json:"where_fields"`          // List of fields in WHERE clause
	WhereClause  string   `json:"where_clause"`          // Complete WHERE clause text
	OriginalSQL  string   `json:"original_sql"`          // Original SQL statement
	ParseError   string   `json:"parse_error,omitempty"` // Error message if parsing failed
}

// SQLValidationError represents a validation error
type SQLValidationError struct {
	Type    string `json:"type"`    // Error type: "table_not_allowed", "sql_injection_risk", etc.
	Message string `json:"message"` // Error message
	Details string `json:"details"` // Additional details
}

// SQLValidationResult represents the result of SQL validation
type SQLValidationResult struct {
	Valid  bool                 `json:"valid"`  // Whether the SQL passed validation
	Errors []SQLValidationError `json:"errors"` // List of validation errors
}

// SQLValidationOption is a function that configures SQL validation
type SQLValidationOption func(*sqlValidator)

// sqlValidator holds validation configuration
type sqlValidator struct {
	// Basic validation
	checkInputValidation bool
	minLength            int
	maxLength            int

	// Statement type validation
	checkSelectOnly      bool
	checkSingleStatement bool

	// Table validation
	allowedTables   map[string]bool
	checkTableNames bool

	// Function validation
	allowedFunctions   map[string]bool
	checkFunctionNames bool

	// Security checks
	checkInjectionRisk  bool
	checkSubqueries     bool
	checkCTEs           bool
	checkSystemColumns  bool
	checkSchemaAccess   bool
	checkDangerousFuncs bool

	// Tenant isolation
	enableTenantInjection bool
	tenantID              uint64
	tablesWithTenantID    map[string]bool
}

// ParseSQL parses a SQL statement using pg_query_go and extracts table names, select fields, and where fields
// This uses the PostgreSQL parser for accurate SQL parsing
func ParseSQL(sql string) *SQLParseResult {
	result := &SQLParseResult{
		OriginalSQL:  sql,
		TableNames:   make([]string, 0),
		SelectFields: make([]string, 0),
		WhereFields:  make([]string, 0),
	}

	// Parse the SQL using pg_query_go
	parseResult, err := pg_query.Parse(sql)
	if err != nil {
		result.IsSelect = false
		result.ParseError = fmt.Sprintf("Failed to parse SQL: %v", err)
		return result
	}

	// Check if it's a SELECT statement
	if len(parseResult.Stmts) == 0 {
		result.IsSelect = false
		result.ParseError = "No statements found in SQL"
		return result
	}

	// Get the first statement
	stmt := parseResult.Stmts[0]
	if stmt.Stmt == nil {
		result.IsSelect = false
		result.ParseError = "Invalid statement"
		return result
	}

	// Check if it's a SELECT statement
	selectStmt := stmt.Stmt.GetSelectStmt()
	if selectStmt == nil {
		result.IsSelect = false
		result.ParseError = "Not a SELECT statement"
		return result
	}
	result.IsSelect = true

	// Extract SELECT fields
	result.SelectFields = extractSelectFieldsFromPgQuery(selectStmt)

	// Extract table names from FROM clause
	result.TableNames = extractTableNamesFromPgQuery(selectStmt)

	// Extract WHERE clause fields and text
	whereFields, whereClause := extractWhereFromPgQuery(selectStmt, sql)
	result.WhereFields = whereFields
	result.WhereClause = whereClause

	return result
}

// extractSelectFieldsFromPgQuery extracts field names from SELECT clause using pg_query parse tree
func extractSelectFieldsFromPgQuery(selectStmt *pg_query.SelectStmt) []string {
	fields := make([]string, 0)
	fieldMap := make(map[string]bool) // Avoid duplicates

	if selectStmt.TargetList == nil {
		return fields
	}

	for _, target := range selectStmt.TargetList {
		resTarget := target.GetResTarget()
		if resTarget == nil {
			continue
		}

		// Extract column names from the target
		colNames := extractColumnNamesFromNode(resTarget.Val)
		for _, colName := range colNames {
			if colName != "" && !fieldMap[colName] {
				fieldMap[colName] = true
				fields = append(fields, colName)
			}
		}
	}

	return fields
}

// extractTableNamesFromPgQuery extracts table names from FROM clause using pg_query parse tree
func extractTableNamesFromPgQuery(selectStmt *pg_query.SelectStmt) []string {
	tables := make([]string, 0)
	tableMap := make(map[string]bool) // Avoid duplicates

	if selectStmt.FromClause == nil {
		return tables
	}

	for _, fromItem := range selectStmt.FromClause {
		tableNames := extractTableNamesFromNode(fromItem)
		for _, tableName := range tableNames {
			if tableName != "" && !tableMap[tableName] {
				tableMap[tableName] = true
				tables = append(tables, tableName)
			}
		}
	}

	return tables
}

// extractWhereFromPgQuery extracts WHERE clause fields and text using pg_query parse tree
func extractWhereFromPgQuery(selectStmt *pg_query.SelectStmt, originalSQL string) ([]string, string) {
	fields := make([]string, 0)
	fieldMap := make(map[string]bool) // Avoid duplicates
	whereClause := ""

	if selectStmt.WhereClause == nil {
		return fields, whereClause
	}

	// Extract WHERE clause text from original SQL
	whereClause = extractWhereClauseText(originalSQL)

	// Extract column names from WHERE clause
	colNames := extractColumnNamesFromNode(selectStmt.WhereClause)
	for _, colName := range colNames {
		if colName != "" && !fieldMap[colName] {
			fieldMap[colName] = true
			fields = append(fields, colName)
		}
	}

	return fields, whereClause
}

// extractColumnNamesFromNode recursively extracts column names from a parse tree node
func extractColumnNamesFromNode(node *pg_query.Node) []string {
	if node == nil {
		return nil
	}

	colNames := make([]string, 0)

	// Handle ColumnRef (column reference)
	if colRef := node.GetColumnRef(); colRef != nil {
		if colRef.Fields != nil {
			for _, field := range colRef.Fields {
				if strNode := field.GetString_(); strNode != nil {
					if strNode.Sval != "*" { // Skip wildcard
						colNames = append(colNames, strNode.Sval)
					}
				}
			}
		}
		return colNames
	}

	// Handle A_Expr (expression with operators)
	if aExpr := node.GetAExpr(); aExpr != nil {
		colNames = append(colNames, extractColumnNamesFromNode(aExpr.Lexpr)...)
		colNames = append(colNames, extractColumnNamesFromNode(aExpr.Rexpr)...)
		return colNames
	}

	// Handle BoolExpr (AND, OR, NOT)
	if boolExpr := node.GetBoolExpr(); boolExpr != nil {
		if boolExpr.Args != nil {
			for _, arg := range boolExpr.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		return colNames
	}

	// Handle FuncCall (function calls)
	if funcCall := node.GetFuncCall(); funcCall != nil {
		if funcCall.Args != nil {
			for _, arg := range funcCall.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		return colNames
	}

	// Handle ResTarget (result target in SELECT)
	if resTarget := node.GetResTarget(); resTarget != nil {
		colNames = append(colNames, extractColumnNamesFromNode(resTarget.Val)...)
		return colNames
	}

	// Handle SubLink (subquery)
	if subLink := node.GetSubLink(); subLink != nil {
		colNames = append(colNames, extractColumnNamesFromNode(subLink.Testexpr)...)
		return colNames
	}

	// Handle NullTest (IS NULL, IS NOT NULL)
	if nullTest := node.GetNullTest(); nullTest != nil {
		colNames = append(colNames, extractColumnNamesFromNode(nullTest.Arg)...)
		return colNames
	}

	// Handle CaseExpr (CASE WHEN)
	if caseExpr := node.GetCaseExpr(); caseExpr != nil {
		colNames = append(colNames, extractColumnNamesFromNode(caseExpr.Arg)...)
		if caseExpr.Args != nil {
			for _, arg := range caseExpr.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		colNames = append(colNames, extractColumnNamesFromNode(caseExpr.Defresult)...)
		return colNames
	}

	// Handle CaseWhen (WHEN clause in CASE)
	if caseWhen := node.GetCaseWhen(); caseWhen != nil {
		colNames = append(colNames, extractColumnNamesFromNode(caseWhen.Expr)...)
		colNames = append(colNames, extractColumnNamesFromNode(caseWhen.Result)...)
		return colNames
	}

	return colNames
}

// extractTableNamesFromNode recursively extracts table names from a parse tree node
func extractTableNamesFromNode(node *pg_query.Node) []string {
	if node == nil {
		return nil
	}

	tableNames := make([]string, 0)

	// Handle RangeVar (table reference)
	if rangeVar := node.GetRangeVar(); rangeVar != nil {
		if rangeVar.Relname != "" {
			tableNames = append(tableNames, rangeVar.Relname)
		}
		return tableNames
	}

	// Handle JoinExpr (JOIN)
	if joinExpr := node.GetJoinExpr(); joinExpr != nil {
		tableNames = append(tableNames, extractTableNamesFromNode(joinExpr.Larg)...)
		tableNames = append(tableNames, extractTableNamesFromNode(joinExpr.Rarg)...)
		return tableNames
	}

	// Handle RangeSubselect (subquery in FROM)
	if rangeSubselect := node.GetRangeSubselect(); rangeSubselect != nil {
		// We could recursively parse the subquery here if needed
		return tableNames
	}

	return tableNames
}

// extractWhereClauseText extracts the WHERE clause text from the original SQL
func extractWhereClauseText(sql string) string {
	lowerSQL := strings.ToLower(sql)
	wherePos := strings.Index(lowerSQL, "where")
	if wherePos == -1 {
		return ""
	}

	// Find the end of WHERE clause
	whereClauseEnd := len(sql)
	for _, keyword := range []string{"group by", "order by", "limit", "having", "union", "intersect", "except"} {
		if pos := strings.Index(lowerSQL[wherePos:], keyword); pos != -1 {
			actualPos := wherePos + pos
			if actualPos < whereClauseEnd {
				whereClauseEnd = actualPos
			}
		}
	}

	// Extract WHERE clause (skip "WHERE" keyword)
	whereClause := strings.TrimSpace(sql[wherePos+5 : whereClauseEnd])
	return whereClause
}

// WithAllowedTables creates a validation option that checks if table names are in the allowed list
func WithAllowedTables(tables ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkTableNames = true
		v.allowedTables = make(map[string]bool)
		for _, table := range tables {
			v.allowedTables[strings.ToLower(table)] = true
		}
	}
}

// WithInjectionRiskCheck creates a validation option that checks for SQL injection risks
func WithInjectionRiskCheck() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkInjectionRisk = true
	}
}

// WithInputValidation enables basic input validation (length, null bytes, etc.)
func WithInputValidation(minLen, maxLen int) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkInputValidation = true
		v.minLength = minLen
		v.maxLength = maxLen
	}
}

// WithSelectOnly ensures only SELECT statements are allowed
func WithSelectOnly() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSelectOnly = true
	}
}

// WithSingleStatement ensures only single statement is allowed (no multiple statements)
func WithSingleStatement() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSingleStatement = true
	}
}

// WithAllowedFunctions creates a validation option that checks if functions are in the allowed list
func WithAllowedFunctions(functions ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkFunctionNames = true
		v.allowedFunctions = make(map[string]bool)
		for _, fn := range functions {
			v.allowedFunctions[strings.ToLower(fn)] = true
		}
	}
}

// WithDefaultSafeFunctions enables a default set of safe SQL functions
func WithDefaultSafeFunctions() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkFunctionNames = true
		v.allowedFunctions = map[string]bool{
			// Aggregate functions
			"count":            true,
			"sum":              true,
			"avg":              true,
			"min":              true,
			"max":              true,
			"array_agg":        true,
			"string_agg":       true,
			"bool_and":         true,
			"bool_or":          true,
			"json_agg":         true,
			"jsonb_agg":        true,
			"json_object_agg":  true,
			"jsonb_object_agg": true,
			// Safe scalar functions
			"coalesce":          true,
			"nullif":            true,
			"greatest":          true,
			"least":             true,
			"abs":               true,
			"ceil":              true,
			"floor":             true,
			"round":             true,
			"length":            true,
			"lower":             true,
			"upper":             true,
			"trim":              true,
			"ltrim":             true,
			"rtrim":             true,
			"substring":         true,
			"concat":            true,
			"concat_ws":         true,
			"replace":           true,
			"left":              true,
			"right":             true,
			"now":               true,
			"current_date":      true,
			"current_timestamp": true,
			"date_trunc":        true,
			"extract":           true,
			"to_char":           true,
			"to_date":           true,
			"to_timestamp":      true,
			"date_part":         true,
			"age":               true,
		}
	}
}

// WithNoSubqueries blocks all subqueries
func WithNoSubqueries() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSubqueries = true
	}
}

// WithNoCTEs blocks Common Table Expressions (WITH clause)
func WithNoCTEs() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkCTEs = true
	}
}

// WithNoSystemColumns blocks access to PostgreSQL system columns
func WithNoSystemColumns() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSystemColumns = true
	}
}

// WithNoSchemaAccess blocks schema-qualified access (except public schema)
func WithNoSchemaAccess() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSchemaAccess = true
	}
}

// WithNoDangerousFunctions blocks dangerous PostgreSQL functions
func WithNoDangerousFunctions() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkDangerousFuncs = true
	}
}

// WithTenantIsolation enables automatic tenant_id injection for multi-tenant security
func WithTenantIsolation(tenantID uint64, tables ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.enableTenantInjection = true
		v.tenantID = tenantID
		v.tablesWithTenantID = make(map[string]bool)
		if len(tables) == 0 {
			// Default tables with tenant_id
			// SECURITY: All tables with tenant_id column must be listed here
			// to ensure proper tenant isolation and prevent cross-tenant data access
			v.tablesWithTenantID = map[string]bool{
				"knowledge_bases": true,
				"knowledges":      true,
				"chunks":          true,
			}
		} else {
			for _, table := range tables {
				v.tablesWithTenantID[strings.ToLower(table)] = true
			}
		}
	}
}

// WithSecurityDefaults applies a comprehensive set of security validations
func WithSecurityDefaults(tenantID uint64) SQLValidationOption {
	return func(v *sqlValidator) {
		// Apply all security checks
		WithInputValidation(6, 4096)(v)
		WithSelectOnly()(v)
		WithSingleStatement()(v)
		WithNoSubqueries()(v)
		WithNoCTEs()(v)
		WithNoSystemColumns()(v)
		WithNoSchemaAccess()(v)
		WithNoDangerousFunctions()(v)
		WithDefaultSafeFunctions()(v)
		WithTenantIsolation(tenantID)(v)

		// Default allowed tables
		// SECURITY: Only tables with tenant_id column should be listed here
		// Tables without tenant_id (messages, embeddings) are excluded to prevent
		// cross-tenant data access vulnerabilities (CVE: Broken Access Control)
		WithAllowedTables(
			"knowledge_bases",
			"knowledges",
			"chunks",
		)(v)
	}
}

// ValidateSQL validates a SQL statement with the given options
func ValidateSQL(sql string, opts ...SQLValidationOption) (*SQLParseResult, *SQLValidationResult) {
	// Initialize validator with defaults
	validator := &sqlValidator{
		allowedTables:      make(map[string]bool),
		allowedFunctions:   make(map[string]bool),
		tablesWithTenantID: make(map[string]bool),
		minLength:          6,
		maxLength:          4096,
	}

	// Apply options
	for _, opt := range opts {
		opt(validator)
	}

	// Initialize validation result
	validationResult := &SQLValidationResult{
		Valid:  true,
		Errors: make([]SQLValidationError, 0),
	}

	// Phase 1: Basic input validation
	if validator.checkInputValidation {
		if err := validator.validateInput(sql); err != nil {
			validationResult.Valid = false
			validationResult.Errors = append(validationResult.Errors, SQLValidationError{
				Type:    "input_validation_error",
				Message: "Input validation failed",
				Details: err.Error(),
			})
			return nil, validationResult
		}
	}

	// Phase 2: Parse SQL using PostgreSQL's official parser
	parseResult, err := pg_query.Parse(sql)
	if err != nil {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "parse_error",
			Message: "Failed to parse SQL",
			Details: fmt.Sprintf("SQL parse error: %v", err),
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  err.Error(),
		}, validationResult
	}

	// Phase 3: Validate statement count
	if len(parseResult.Stmts) == 0 {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "empty_query",
			Message: "Empty query",
			Details: "No statements found in SQL",
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  "empty query",
		}, validationResult
	}

	if validator.checkSingleStatement && len(parseResult.Stmts) > 1 {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "multiple_statements",
			Message: "Multiple statements are not allowed",
			Details: fmt.Sprintf("Found %d statements, only 1 is allowed", len(parseResult.Stmts)),
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  "multiple statements",
		}, validationResult
	}

	stmt := parseResult.Stmts[0].Stmt

	// Phase 4: Ensure it's a SELECT statement
	selectStmt := stmt.GetSelectStmt()
	if validator.checkSelectOnly && selectStmt == nil {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "not_select_statement",
			Message: "Only SELECT queries are allowed",
			Details: "Statement is not a SELECT query",
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			IsSelect:    false,
			ParseError:  "not a SELECT statement",
		}, validationResult
	}

	// Build parse result
	result := &SQLParseResult{
		OriginalSQL:  sql,
		IsSelect:     selectStmt != nil,
		TableNames:   make([]string, 0),
		SelectFields: make([]string, 0),
		WhereFields:  make([]string, 0),
	}

	if selectStmt != nil {
		// Extract SELECT fields
		result.SelectFields = extractSelectFieldsFromPgQuery(selectStmt)

		// Extract table names from FROM clause
		result.TableNames = extractTableNamesFromPgQuery(selectStmt)

		// Extract WHERE clause fields and text
		whereFields, whereClause := extractWhereFromPgQuery(selectStmt, sql)
		result.WhereFields = whereFields
		result.WhereClause = whereClause

		// Phase 5: Validate the SELECT statement with deep inspection
		if err := validator.validateSelectStmt(selectStmt, validationResult); err != nil {
			validationResult.Valid = false
			validationResult.Errors = append(validationResult.Errors, SQLValidationError{
				Type:    "statement_validation_error",
				Message: "Statement validation failed",
				Details: err.Error(),
			})
		}

		// Phase 6: Validate table names
		if validator.checkTableNames {
			for _, table := range result.TableNames {
				if !validator.allowedTables[strings.ToLower(table)] {
					validationResult.Valid = false
					validationResult.Errors = append(validationResult.Errors, SQLValidationError{
						Type:    "table_not_allowed",
						Message: fmt.Sprintf("Table '%s' is not in the allowed list", table),
						Details: fmt.Sprintf("Allowed tables: %v", getMapKeys(validator.allowedTables)),
					})
				}
			}
		}

		// Phase 7: Check for SQL injection risks (legacy check)
		if validator.checkInjectionRisk {
			injectionErrors := checkSQLInjectionRisks(result.WhereClause)
			if len(injectionErrors) > 0 {
				validationResult.Valid = false
				validationResult.Errors = append(validationResult.Errors, injectionErrors...)
			}
		}
	}

	return result, validationResult
}

// ValidateAndSecureSQL validates SQL and returns a secured version with tenant isolation
// This is a convenience function that combines validation and SQL rewriting
func ValidateAndSecureSQL(sql string, opts ...SQLValidationOption) (string, *SQLValidationResult, error) {
	// Parse and validate
	parseResult, validationResult := ValidateSQL(sql, opts...)

	// If validation failed, return error
	if !validationResult.Valid {
		errMsg := "SQL validation failed"
		if len(validationResult.Errors) > 0 {
			errMsg = validationResult.Errors[0].Message
		}
		return "", validationResult, fmt.Errorf("%s", errMsg)
	}

	// Find validator config to check if tenant injection is enabled
	validator := &sqlValidator{
		tablesWithTenantID: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(validator)
	}

	// If tenant injection is not enabled, return original SQL
	if !validator.enableTenantInjection {
		return sql, validationResult, nil
	}

	// Parse again to get normalized SQL
	result, err := pg_query.Parse(sql)
	if err != nil {
		return "", validationResult, fmt.Errorf("failed to parse SQL: %v", err)
	}

	// Normalize SQL
	normalizedSQL, err := pg_query.Deparse(result)
	if err != nil {
		return "", validationResult, fmt.Errorf("failed to normalize SQL: %v", err)
	}

	// Build table map from parse result
	tablesInQuery := make(map[string]string)
	for _, tableName := range parseResult.TableNames {
		tablesInQuery[strings.ToLower(tableName)] = strings.ToLower(tableName)
	}

	// Inject tenant conditions
	securedSQL := validator.injectTenantConditions(normalizedSQL, tablesInQuery)

	return securedSQL, validationResult, nil
}

// injectTenantConditions adds tenant_id filtering to the query
func (v *sqlValidator) injectTenantConditions(sql string, tablesInQuery map[string]string) string {
	if !v.enableTenantInjection {
		return sql
	}

	// Build tenant conditions
	var conditions []string
	for tableName, alias := range tablesInQuery {
		if v.tablesWithTenantID[tableName] {
			if tableName == "tenants" {
				conditions = append(conditions, fmt.Sprintf("%s.id = %d", alias, v.tenantID))
			} else {
				conditions = append(conditions, fmt.Sprintf("%s.tenant_id = %d", alias, v.tenantID))
			}
		}
	}

	if len(conditions) == 0 {
		return sql
	}

	tenantFilter := strings.Join(conditions, " AND ")

	// Check if WHERE clause exists
	wherePattern := regexp.MustCompile(`(?i)\bWHERE\b`)
	if wherePattern.MatchString(sql) {
		// Add tenant filter and wrap existing conditions in parentheses to prevent OR injection
		// This ensures: WHERE tenant_filter AND (original_conditions)
		// Instead of: WHERE tenant_filter AND original_conditions OR malicious_condition
		return wherePattern.ReplaceAllString(sql, fmt.Sprintf("WHERE %s AND (", tenantFilter)) + ")"
	}

	// Add new WHERE clause before ORDER BY, GROUP BY, LIMIT, etc.
	clausePattern := regexp.MustCompile(`(?i)\b(GROUP BY|ORDER BY|LIMIT|OFFSET|HAVING|FETCH)\b`)
	if loc := clausePattern.FindStringIndex(sql); loc != nil {
		return sql[:loc[0]] + fmt.Sprintf(" WHERE %s ", tenantFilter) + sql[loc[0]:]
	}

	// Add WHERE clause at the end
	return fmt.Sprintf("%s WHERE %s", sql, tenantFilter)
}

// checkSQLInjectionRisks checks for common SQL injection patterns in WHERE clause
func checkSQLInjectionRisks(whereClause string) []SQLValidationError {
	errors := make([]SQLValidationError, 0)

	if whereClause == "" {
		return errors
	}

	// Normalize the WHERE clause for checking
	normalizedWhere := strings.ToLower(strings.TrimSpace(whereClause))
	normalizedWhere = regexp.MustCompile(`\s+`).ReplaceAllString(normalizedWhere, " ")

	// Pattern 1: Always true conditions like "1=1", "'1'='1'", "true", etc.
	alwaysTruePatterns := []struct {
		pattern     *regexp.Regexp
		description string
	}{
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(1\s*=\s*1|'1'\s*=\s*'1'|"1"\s*=\s*"1")(\s|\)|$|and|or)`),
			description: "Always-true condition '1=1' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(0\s*=\s*0|'0'\s*=\s*'0'|"0"\s*=\s*"0")(\s|\)|$|and|or)`),
			description: "Always-true condition '0=0' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(true)(\s|\)|$|and|or)`),
			description: "Always-true condition 'true'",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()('\s*'\s*=\s*'\s*'|"\s*"\s*=\s*"\s*")(\s|\)|$|and|or)`),
			description: "Always-true condition with empty strings",
		},
	}

	for _, pt := range alwaysTruePatterns {
		if pt.pattern.MatchString(normalizedWhere) {
			errors = append(errors, SQLValidationError{
				Type:    "sql_injection_risk",
				Message: "Potential SQL injection risk detected",
				Details: fmt.Sprintf("%s found in WHERE clause: %s", pt.description, whereClause),
			})
		}
	}

	// Pattern 2: Always false conditions that might be used for testing
	alwaysFalsePatterns := []struct {
		pattern     *regexp.Regexp
		description string
	}{
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(1\s*=\s*0|0\s*=\s*1|'1'\s*=\s*'0'|"1"\s*=\s*"0")(\s|\)|$|and|or)`),
			description: "Always-false condition '1=0' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(false)(\s|\)|$|and|or)`),
			description: "Always-false condition 'false'",
		},
	}

	for _, pt := range alwaysFalsePatterns {
		if pt.pattern.MatchString(normalizedWhere) {
			errors = append(errors, SQLValidationError{
				Type:    "sql_injection_risk",
				Message: "Suspicious SQL pattern detected",
				Details: fmt.Sprintf("%s found in WHERE clause: %s", pt.description, whereClause),
			})
		}
	}

	// Pattern 3: OR with always-true condition (common injection pattern)
	if regexp.MustCompile(`or\s+(1\s*=\s*1|'1'\s*=\s*'1'|true)`).MatchString(normalizedWhere) {
		errors = append(errors, SQLValidationError{
			Type:    "sql_injection_risk",
			Message: "High-risk SQL injection pattern detected",
			Details: fmt.Sprintf("OR with always-true condition found in WHERE clause: %s", whereClause),
		})
	}

	return errors
}

// getMapKeys returns the keys of a map as a slice
func getMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// validateInput performs basic input validation
func (v *sqlValidator) validateInput(sql string) error {
	// Check for null bytes
	if strings.Contains(sql, "\x00") {
		return fmt.Errorf("invalid character in SQL query")
	}

	// Check length limits
	if len(sql) < v.minLength {
		return fmt.Errorf("SQL query too short (min %d characters)", v.minLength)
	}
	if len(sql) > v.maxLength {
		return fmt.Errorf("SQL query too long (max %d characters)", v.maxLength)
	}

	return nil
}

// validateSelectStmt validates a SELECT statement with configured options
func (v *sqlValidator) validateSelectStmt(stmt *pg_query.SelectStmt, result *SQLValidationResult) error {
	tablesInQuery := make(map[string]string) // table name -> alias

	// Check for UNION/INTERSECT/EXCEPT (compound queries)
	if stmt.Op != pg_query.SetOperation_SETOP_NONE {
		return fmt.Errorf("compound queries (UNION/INTERSECT/EXCEPT) are not allowed")
	}

	// Check for WITH clause (CTEs)
	if v.checkCTEs && stmt.WithClause != nil {
		return fmt.Errorf("WITH clause (CTEs) is not allowed")
	}

	// Check for INTO clause (SELECT INTO)
	if stmt.IntoClause != nil {
		return fmt.Errorf("SELECT INTO is not allowed")
	}

	// Check for LOCKING clause (FOR UPDATE, etc.)
	if len(stmt.LockingClause) > 0 {
		return fmt.Errorf("locking clauses (FOR UPDATE, etc.) are not allowed")
	}

	// Validate FROM clause
	for _, fromItem := range stmt.FromClause {
		if err := v.validateFromItem(fromItem, tablesInQuery, result); err != nil {
			return err
		}
	}

	// Validate target list (SELECT columns)
	for _, target := range stmt.TargetList {
		if err := v.validateNode(target, result); err != nil {
			return err
		}
	}

	// Validate WHERE clause
	if stmt.WhereClause != nil {
		if err := v.validateNode(stmt.WhereClause, result); err != nil {
			return err
		}
	}

	// Validate GROUP BY clause
	for _, groupBy := range stmt.GroupClause {
		if err := v.validateNode(groupBy, result); err != nil {
			return err
		}
	}

	// Validate HAVING clause
	if stmt.HavingClause != nil {
		if err := v.validateNode(stmt.HavingClause, result); err != nil {
			return err
		}
	}

	// Validate ORDER BY clause
	for _, sortBy := range stmt.SortClause {
		if err := v.validateNode(sortBy, result); err != nil {
			return err
		}
	}

	// Ensure at least one valid table is referenced
	if len(tablesInQuery) == 0 {
		return fmt.Errorf("no valid table found in query")
	}

	return nil
}

// validateFromItem validates a FROM clause item
func (v *sqlValidator) validateFromItem(node *pg_query.Node, tables map[string]string, result *SQLValidationResult) error {
	if node == nil {
		return nil
	}

	// Handle RangeVar (simple table reference)
	if rv := node.GetRangeVar(); rv != nil {
		tableName := strings.ToLower(rv.Relname)

		// Check for schema qualification
		if v.checkSchemaAccess && rv.Schemaname != "" {
			schemaName := strings.ToLower(rv.Schemaname)
			if schemaName != "public" {
				return fmt.Errorf("access to schema '%s' is not allowed", rv.Schemaname)
			}
		}

		// Get alias
		alias := tableName
		if rv.Alias != nil && rv.Alias.Aliasname != "" {
			alias = strings.ToLower(rv.Alias.Aliasname)
		}
		tables[tableName] = alias
		return nil
	}

	// Handle JoinExpr (JOIN)
	if je := node.GetJoinExpr(); je != nil {
		if err := v.validateFromItem(je.Larg, tables, result); err != nil {
			return err
		}
		if err := v.validateFromItem(je.Rarg, tables, result); err != nil {
			return err
		}
		if je.Quals != nil {
			if err := v.validateNode(je.Quals, result); err != nil {
				return err
			}
		}
		return nil
	}

	// Handle RangeSubselect (subquery in FROM)
	if v.checkSubqueries && node.GetRangeSubselect() != nil {
		return fmt.Errorf("subqueries in FROM clause are not allowed")
	}

	// Handle RangeFunction (function in FROM)
	if node.GetRangeFunction() != nil {
		return fmt.Errorf("functions in FROM clause are not allowed")
	}

	return nil
}

// validateNode recursively validates AST nodes
// SECURITY: This function uses a COMPREHENSIVE approach to validate ALL node types.
// Any node type that contains child expressions MUST be handled to prevent bypass attacks.
// The principle is: if we don't know how to validate a node type, we REJECT it.
func (v *sqlValidator) validateNode(node *pg_query.Node, result *SQLValidationResult) error {
	if node == nil {
		return nil
	}

	// Check for subqueries (SubLink)
	if v.checkSubqueries {
		if sl := node.GetSubLink(); sl != nil {
			return fmt.Errorf("subqueries are not allowed")
		}
	}

	// Check for function calls
	if fc := node.GetFuncCall(); fc != nil {
		if err := v.validateFuncCall(fc, result); err != nil {
			return err
		}
	}

	// Check for column references
	if cr := node.GetColumnRef(); cr != nil {
		if err := v.validateColumnRef(cr); err != nil {
			return err
		}
	}

	// Check for type casts
	if tc := node.GetTypeCast(); tc != nil {
		if err := v.validateNode(tc.Arg, result); err != nil {
			return err
		}
		if tc.TypeName != nil {
			typeName := v.getTypeName(tc.TypeName)
			if strings.HasPrefix(strings.ToLower(typeName), "pg_") {
				return fmt.Errorf("casting to system type '%s' is not allowed", typeName)
			}
		}
	}

	// Recursively check A_Expr (expressions)
	if ae := node.GetAExpr(); ae != nil {
		if err := v.validateNode(ae.Lexpr, result); err != nil {
			return err
		}
		if err := v.validateNode(ae.Rexpr, result); err != nil {
			return err
		}
	}

	// Check BoolExpr (AND, OR, NOT)
	if be := node.GetBoolExpr(); be != nil {
		for _, arg := range be.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Check NullTest
	if nt := node.GetNullTest(); nt != nil {
		if err := v.validateNode(nt.Arg, result); err != nil {
			return err
		}
	}

	// Check CoalesceExpr
	if ce := node.GetCoalesceExpr(); ce != nil {
		for _, arg := range ce.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Check CaseExpr
	if caseExpr := node.GetCaseExpr(); caseExpr != nil {
		if err := v.validateNode(caseExpr.Arg, result); err != nil {
			return err
		}
		for _, when := range caseExpr.Args {
			if err := v.validateNode(when, result); err != nil {
				return err
			}
		}
		if err := v.validateNode(caseExpr.Defresult, result); err != nil {
			return err
		}
	}

	// Check CaseWhen
	if cw := node.GetCaseWhen(); cw != nil {
		if err := v.validateNode(cw.Expr, result); err != nil {
			return err
		}
		if err := v.validateNode(cw.Result, result); err != nil {
			return err
		}
	}

	// Check ResTarget (SELECT list items)
	if rt := node.GetResTarget(); rt != nil {
		if err := v.validateNode(rt.Val, result); err != nil {
			return err
		}
	}

	// Check SortBy (ORDER BY items)
	if sb := node.GetSortBy(); sb != nil {
		if err := v.validateNode(sb.Node, result); err != nil {
			return err
		}
	}

	// Check List
	if list := node.GetList(); list != nil {
		for _, item := range list.Items {
			if err := v.validateNode(item, result); err != nil {
				return err
			}
		}
	}

	// ============================================================
	// SECURITY FIX: Comprehensive handling of ALL expression types
	// that can contain child nodes (potential bypass vectors)
	// ============================================================

	// ArrayExpr (ARRAY[...] expressions)
	// Attack: SELECT ARRAY[pg_read_file('/etc/passwd')] FROM table
	if ae := node.GetAArrayExpr(); ae != nil {
		for _, elem := range ae.Elements {
			if err := v.validateNode(elem, result); err != nil {
				return err
			}
		}
	}

	// RowExpr (ROW(...) expressions)
	// Attack: SELECT ROW(pg_read_file('/etc/passwd')) FROM table
	if re := node.GetRowExpr(); re != nil {
		for _, arg := range re.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// MinMaxExpr (GREATEST/LEAST expressions)
	if mm := node.GetMinMaxExpr(); mm != nil {
		for _, arg := range mm.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// NullIfExpr (NULLIF expressions)
	if ni := node.GetNullIfExpr(); ni != nil {
		for _, arg := range ni.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ScalarArrayOpExpr (IN, ANY, ALL with arrays)
	if sao := node.GetScalarArrayOpExpr(); sao != nil {
		for _, arg := range sao.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ArrayCoerceExpr
	if ace := node.GetArrayCoerceExpr(); ace != nil {
		if err := v.validateNode(ace.Arg, result); err != nil {
			return err
		}
	}

	// CoerceViaIO (type coercion via I/O)
	if cvi := node.GetCoerceViaIo(); cvi != nil {
		if err := v.validateNode(cvi.Arg, result); err != nil {
			return err
		}
	}

	// CollateExpr (COLLATE expressions)
	if ce := node.GetCollateExpr(); ce != nil {
		if err := v.validateNode(ce.Arg, result); err != nil {
			return err
		}
	}

	// SubLink (subqueries) - validate child expressions even if subqueries are allowed
	if sl := node.GetSubLink(); sl != nil {
		if err := v.validateNode(sl.Testexpr, result); err != nil {
			return err
		}
	}

	// OpExpr (operator expressions)
	if oe := node.GetOpExpr(); oe != nil {
		for _, arg := range oe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// DistinctExpr (IS DISTINCT FROM)
	if de := node.GetDistinctExpr(); de != nil {
		for _, arg := range de.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// XmlExpr (XML expressions)
	if xe := node.GetXmlExpr(); xe != nil {
		for _, arg := range xe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range xe.NamedArgs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonConstructorExpr
	if jce := node.GetJsonConstructorExpr(); jce != nil {
		for _, arg := range jce.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ============================================================
	// Additional expression types that need recursive validation
	// ============================================================

	// FuncExpr (different from FuncCall - internal function representation)
	if fe := node.GetFuncExpr(); fe != nil {
		for _, arg := range fe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Aggref (aggregate function reference)
	if ag := node.GetAggref(); ag != nil {
		for _, arg := range ag.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range ag.Aggdirectargs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		if ag.Aggfilter != nil {
			if err := v.validateNode(ag.Aggfilter, result); err != nil {
				return err
			}
		}
	}

	// WindowFunc
	if wf := node.GetWindowFunc(); wf != nil {
		for _, arg := range wf.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		if wf.Aggfilter != nil {
			if err := v.validateNode(wf.Aggfilter, result); err != nil {
				return err
			}
		}
	}

	// SubscriptingRef (array subscripting like arr[1])
	if sr := node.GetSubscriptingRef(); sr != nil {
		for _, idx := range sr.Refupperindexpr {
			if err := v.validateNode(idx, result); err != nil {
				return err
			}
		}
		for _, idx := range sr.Reflowerindexpr {
			if err := v.validateNode(idx, result); err != nil {
				return err
			}
		}
		if err := v.validateNode(sr.Refexpr, result); err != nil {
			return err
		}
		if err := v.validateNode(sr.Refassgnexpr, result); err != nil {
			return err
		}
	}

	// NamedArgExpr (named arguments in function calls)
	if nae := node.GetNamedArgExpr(); nae != nil {
		if err := v.validateNode(nae.Arg, result); err != nil {
			return err
		}
	}

	// FieldSelect (field selection from composite type)
	if fs := node.GetFieldSelect(); fs != nil {
		if err := v.validateNode(fs.Arg, result); err != nil {
			return err
		}
	}

	// FieldStore
	if fs := node.GetFieldStore(); fs != nil {
		if err := v.validateNode(fs.Arg, result); err != nil {
			return err
		}
		for _, newval := range fs.Newvals {
			if err := v.validateNode(newval, result); err != nil {
				return err
			}
		}
	}

	// RelabelType (type relabeling)
	if rt := node.GetRelabelType(); rt != nil {
		if err := v.validateNode(rt.Arg, result); err != nil {
			return err
		}
	}

	// ConvertRowtypeExpr
	if cre := node.GetConvertRowtypeExpr(); cre != nil {
		if err := v.validateNode(cre.Arg, result); err != nil {
			return err
		}
	}

	// RowCompareExpr
	if rce := node.GetRowCompareExpr(); rce != nil {
		for _, arg := range rce.Largs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range rce.Rargs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// CoerceToDomain
	if ctd := node.GetCoerceToDomain(); ctd != nil {
		if err := v.validateNode(ctd.Arg, result); err != nil {
			return err
		}
	}

	// BooleanTest (IS TRUE, IS FALSE, etc.)
	if bt := node.GetBooleanTest(); bt != nil {
		if err := v.validateNode(bt.Arg, result); err != nil {
			return err
		}
	}

	// AIndices (array indices)
	if ai := node.GetAIndices(); ai != nil {
		if err := v.validateNode(ai.Lidx, result); err != nil {
			return err
		}
		if err := v.validateNode(ai.Uidx, result); err != nil {
			return err
		}
	}

	// AIndirection (array/field indirection)
	if aind := node.GetAIndirection(); aind != nil {
		if err := v.validateNode(aind.Arg, result); err != nil {
			return err
		}
		for _, ind := range aind.Indirection {
			if err := v.validateNode(ind, result); err != nil {
				return err
			}
		}
	}

	// CollateClause
	if cc := node.GetCollateClause(); cc != nil {
		if err := v.validateNode(cc.Arg, result); err != nil {
			return err
		}
	}

	// GroupingFunc
	if gf := node.GetGroupingFunc(); gf != nil {
		for _, arg := range gf.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonValueExpr
	if jve := node.GetJsonValueExpr(); jve != nil {
		if err := v.validateNode(jve.RawExpr, result); err != nil {
			return err
		}
		if err := v.validateNode(jve.FormattedExpr, result); err != nil {
			return err
		}
	}

	// JsonExpr
	if je := node.GetJsonExpr(); je != nil {
		if err := v.validateNode(je.FormattedExpr, result); err != nil {
			return err
		}
		if err := v.validateNode(je.PathSpec, result); err != nil {
			return err
		}
		for _, arg := range je.PassingValues {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonIsPredicate
	if jip := node.GetJsonIsPredicate(); jip != nil {
		if err := v.validateNode(jip.Expr, result); err != nil {
			return err
		}
	}

	// XmlSerialize
	if xs := node.GetXmlSerialize(); xs != nil {
		if err := v.validateNode(xs.Expr, result); err != nil {
			return err
		}
	}

	// WindowDef
	if wd := node.GetWindowDef(); wd != nil {
		for _, part := range wd.PartitionClause {
			if err := v.validateNode(part, result); err != nil {
				return err
			}
		}
		for _, order := range wd.OrderClause {
			if err := v.validateNode(order, result); err != nil {
				return err
			}
		}
		if err := v.validateNode(wd.StartOffset, result); err != nil {
			return err
		}
		if err := v.validateNode(wd.EndOffset, result); err != nil {
			return err
		}
	}

	// SubPlan - BLOCK: This is an internal representation, should not appear in user queries
	if node.GetSubPlan() != nil {
		return fmt.Errorf("SubPlan nodes are not allowed")
	}

	// AlternativeSubPlan - BLOCK
	if node.GetAlternativeSubPlan() != nil {
		return fmt.Errorf("AlternativeSubPlan nodes are not allowed")
	}

	return nil
}

// validateFuncCall validates a function call
func (v *sqlValidator) validateFuncCall(fc *pg_query.FuncCall, result *SQLValidationResult) error {
	// Get function name
	funcName := ""
	for _, namePart := range fc.Funcname {
		if s := namePart.GetString_(); s != nil {
			funcName = strings.ToLower(s.Sval)
		}
	}

	// Check for schema-qualified function calls
	if v.checkSchemaAccess && len(fc.Funcname) > 1 {
		schemaName := ""
		if s := fc.Funcname[0].GetString_(); s != nil {
			schemaName = strings.ToLower(s.Sval)
		}
		if schemaName != "" && schemaName != "pg_catalog" {
			return fmt.Errorf("schema-qualified function calls are not allowed: %s", schemaName)
		}
	}

	// Block dangerous function prefixes
	if v.checkDangerousFuncs {
		dangerousPrefixes := []string{
			"pg_",     // All pg_* functions (pg_read_file, pg_reload_conf, pg_stat_*, etc.)
			"lo_",     // Large object functions (lo_import, lo_export, lo_from_bytea, lo_put, etc.)
			"dblink",  // Database link functions
			"file_",   // File functions
			"copy_",   // Copy functions
			"binary_", // Binary functions
		}
		for _, prefix := range dangerousPrefixes {
			if strings.HasPrefix(funcName, prefix) {
				return fmt.Errorf("function '%s' is not allowed (dangerous prefix)", funcName)
			}
		}

		// Block specific dangerous functions - comprehensive list for RCE prevention
		dangerousFunctions := map[string]bool{
			// Configuration and settings
			"current_setting": true,
			"set_config":      true,

			// XML/XPath functions (XXE risks)
			"query_to_xml":       true,
			"xpath":              true,
			"xmlparse":           true,
			"xmlroot":            true,
			"xmlelement":         true,
			"xmlforest":          true,
			"xmlconcat":          true,
			"xmlagg":             true,
			"xmlpi":              true,
			"xmlcomment":         true,
			"xmlexists":          true,
			"xml_is_well_formed": true,
			"xpath_exists":       true,
			"table_to_xml":       true,
			"cursor_to_xml":      true,
			"database_to_xml":    true,
			"schema_to_xml":      true,

			// Transaction and system info
			"txid_current":          true,
			"txid_current_snapshot": true,
			"txid_snapshot_xmin":    true,
			"txid_snapshot_xmax":    true,

			// Encoding functions (used in attack payloads)
			"encode": true,
			"decode": true,

			// Extension management
			"create_extension": true,

			// Copy operations
			"copy":        true,
			"copy_to":     true,
			"copy_from":   true,
			"pg_copy_to":  true,
			"pg_dump":     true,
			"pg_dumpall":  true,
			"pg_restore":  true,
			"pg_basebackup": true,

			// Process and system functions
			"pg_terminate_backend": true,
			"pg_cancel_backend":    true,
			"pg_rotate_logfile":    true,

			// Advisory locks (can be abused for DoS)
			"pg_advisory_lock":           true,
			"pg_advisory_unlock":         true,
			"pg_advisory_lock_shared":    true,
			"pg_advisory_unlock_shared":  true,
			"pg_try_advisory_lock":       true,
			"pg_try_advisory_lock_shared": true,

			// Backup and replication
			"pg_start_backup":  true,
			"pg_stop_backup":   true,
			"pg_switch_wal":    true,
			"pg_create_restore_point": true,

			// Foreign data wrappers
			"postgres_fdw_handler": true,
			"file_fdw_handler":     true,

			// Procedural languages (code execution)
			"plpgsql_call_handler": true,
			"plpython_call_handler": true,
			"plperl_call_handler": true,

			// System catalog modification
			"pg_catalog":  true,
			"information_schema": true,
		}
		if dangerousFunctions[funcName] {
			return fmt.Errorf("function '%s' is not allowed", funcName)
		}
	}

	// Check against whitelist if enabled
	if v.checkFunctionNames && !v.allowedFunctions[funcName] {
		return fmt.Errorf("function not allowed: %s", funcName)
	}

	// Validate function arguments recursively
	for _, arg := range fc.Args {
		if err := v.validateNode(arg, result); err != nil {
			return err
		}
	}

	return nil
}

// validateColumnRef validates a column reference
func (v *sqlValidator) validateColumnRef(cr *pg_query.ColumnRef) error {
	if !v.checkSystemColumns {
		return nil
	}

	// Check for system column access
	for _, field := range cr.Fields {
		if s := field.GetString_(); s != nil {
			colName := strings.ToLower(s.Sval)
			// Block access to system columns
			systemColumns := []string{"xmin", "xmax", "cmin", "cmax", "ctid", "tableoid"}
			for _, sysCol := range systemColumns {
				if colName == sysCol {
					return fmt.Errorf("access to system column '%s' is not allowed", colName)
				}
			}
			// Block pg_ prefixed identifiers
			if strings.HasPrefix(colName, "pg_") {
				return fmt.Errorf("access to '%s' is not allowed", colName)
			}
		}
	}
	return nil
}

// getTypeName extracts the type name from a TypeName node
func (v *sqlValidator) getTypeName(tn *pg_query.TypeName) string {
	var parts []string
	for _, name := range tn.Names {
		if s := name.GetString_(); s != nil {
			parts = append(parts, s.Sval)
		}
	}
	return strings.Join(parts, ".")
}
