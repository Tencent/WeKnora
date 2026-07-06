package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	mysqlDriver "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var tableOrder = []string{
	"tenants",
	"models",
	"knowledge_bases",
	"knowledges",
	"sessions",
	"messages",
	"chunks",
	"users",
	"auth_tokens",
	"tenant_members",
	"audit_logs",
	"knowledge_tags",
	"knowledge_tag_relations",
	"mcp_services",
	"mcp_tool_approvals",
	"mcp_oauth_clients",
	"mcp_oauth_tokens",
	"custom_agents",
	"organizations",
	"organization_tenant_members",
	"kb_shares",
	"organization_join_requests",
	"agent_shares",
	"tenant_disabled_shared_agents",
	"im_channel_sessions",
	"im_channels",
	"embed_channels",
	"data_sources",
	"sync_logs",
	"web_search_providers",
	"vector_stores",
	"wiki_pages",
	"wiki_folders",
	"wiki_page_issues",
	"wiki_log_entries",
	"task_pending_ops",
	"task_dead_letters",
	"system_settings",
	"knowledge_processing_spans",
	"user_resource_favorites",
	"user_kb_pins",
	"tenant_invitations",
}

type config struct {
	pgDSN               string
	mysqlDSN            string
	batchSize           int
	migrateSchema       bool
	dryRun              bool
	allowNonEmptyTarget bool
}

type tableResult struct {
	table       string
	sourceRows  int64
	targetStart int64
	targetRows  int64
	copiedRows  int64
	skipped     bool
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		log.Printf("migration failed: %v", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.pgDSN, "pg-dsn", "", "PostgreSQL DSN，例如 postgres://user:pass@host:5432/weknora?sslmode=disable")
	flag.StringVar(&cfg.mysqlDSN, "mysql-dsn", "", "MySQL DSN，例如 root:pass@tcp(localhost:3306)/weknora?parseTime=true")
	flag.IntVar(&cfg.batchSize, "batch-size", 1000, "每个事务复制的行数")
	flag.BoolVar(&cfg.migrateSchema, "migrate-schema", false, "复制数据前先执行 migrations/mysql")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "只检查并打印计划执行的工作，不写入 MySQL")
	flag.BoolVar(&cfg.allowNonEmptyTarget, "allow-non-empty-target", false, "允许复制到已有表数据的目标库")
	flag.Parse()
	return cfg
}

func run(ctx context.Context, cfg config) error {
	if strings.TrimSpace(cfg.pgDSN) == "" {
		return fmt.Errorf("--pg-dsn is required")
	}
	if strings.TrimSpace(cfg.mysqlDSN) == "" {
		return fmt.Errorf("--mysql-dsn is required")
	}
	if cfg.batchSize <= 0 {
		return fmt.Errorf("--batch-size must be positive")
	}

	pgDB, err := sql.Open("pgx", cfg.pgDSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer pgDB.Close()
	mysqlDB, err := sql.Open("mysql", mysqlOpenDSN(cfg.mysqlDSN))
	if err != nil {
		return fmt.Errorf("open mysql: %w", err)
	}
	defer mysqlDB.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pgDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := mysqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping mysql: %w", err)
	}

	if cfg.migrateSchema {
		if cfg.dryRun {
			log.Printf("[dry-run] would run migrations/mysql")
		} else if err := database.RunMigrationsWithOptions(mysqlMigrateDSN(cfg.mysqlDSN), database.MigrationOptions{}); err != nil {
			return fmt.Errorf("migrate mysql schema: %w", err)
		}
	}

	if !cfg.allowNonEmptyTarget {
		if err := rejectNonEmptyTarget(ctx, mysqlDB); err != nil {
			return err
		}
	}

	results := make([]tableResult, 0, len(tableOrder))
	for _, table := range tableOrder {
		res, err := copyTable(ctx, pgDB, mysqlDB, table, cfg.batchSize, cfg.dryRun)
		if err != nil {
			return err
		}
		results = append(results, res)
	}

	for _, res := range results {
		if res.skipped {
			log.Printf("%s: skipped (missing source or target table)", res.table)
			continue
		}
		expectedTarget := res.sourceRows
		if cfg.allowNonEmptyTarget {
			expectedTarget += res.targetStart
		}
		if !cfg.dryRun && res.targetRows != expectedTarget {
			return fmt.Errorf("%s: row count mismatch, source=%d target_start=%d target=%d expected_target=%d",
				res.table, res.sourceRows, res.targetStart, res.targetRows, expectedTarget)
		}
		log.Printf("%s: source=%d copied=%d target=%d", res.table, res.sourceRows, res.copiedRows, res.targetRows)
	}
	log.Printf("done")
	return nil
}

func copyTable(ctx context.Context, pgDB, mysqlDB *sql.DB, table string, batchSize int, dryRun bool) (tableResult, error) {
	res := tableResult{table: table}

	sourceExists, err := pgTableExists(ctx, pgDB, table)
	if err != nil {
		return res, fmt.Errorf("%s: check source table: %w", table, err)
	}
	targetExists, err := mysqlTableExists(ctx, mysqlDB, table)
	if err != nil {
		return res, fmt.Errorf("%s: check target table: %w", table, err)
	}
	if !sourceExists || !targetExists {
		if !sourceExists {
			res.skipped = true
			return res, nil
		}
		return res, fmt.Errorf("%s: target table does not exist; run with --migrate-schema or initialize migrations/mysql first", table)
	}

	sourceCols, err := pgColumns(ctx, pgDB, table)
	if err != nil {
		return res, fmt.Errorf("%s: inspect source columns: %w", table, err)
	}
	targetCols, err := mysqlColumns(ctx, mysqlDB, table)
	if err != nil {
		return res, fmt.Errorf("%s: inspect target columns: %w", table, err)
	}
	cols := intersectColumns(targetCols, sourceCols)
	if len(cols) == 0 {
		res.skipped = true
		return res, nil
	}

	sourceRows, err := countRows(ctx, pgDB, pgQuote(table))
	if err != nil {
		return res, fmt.Errorf("%s: count source: %w", table, err)
	}
	targetStart, err := countRows(ctx, mysqlDB, mysqlQuote(table))
	if err != nil {
		return res, fmt.Errorf("%s: count target: %w", table, err)
	}
	res.sourceRows = sourceRows
	res.targetStart = targetStart
	if dryRun {
		res.targetRows = targetStart
		log.Printf("[dry-run] %s: would copy %d rows using %d common columns", table, sourceRows, len(cols))
		return res, nil
	}

	for offset := int64(0); offset < sourceRows; offset += int64(batchSize) {
		limit := batchSize
		if remaining := sourceRows - offset; remaining < int64(limit) {
			limit = int(remaining)
		}
		n, err := copyBatch(ctx, pgDB, mysqlDB, table, cols, limit, offset)
		if err != nil {
			end := offset + int64(limit) - 1
			return res, fmt.Errorf("%s batch [%d,%d]: %w", table, offset, end, err)
		}
		res.copiedRows += n
	}

	targetRows, err := countRows(ctx, mysqlDB, mysqlQuote(table))
	if err != nil {
		return res, fmt.Errorf("%s: count target after copy: %w", table, err)
	}
	res.targetRows = targetRows
	return res, nil
}

func copyBatch(ctx context.Context, pgDB, mysqlDB *sql.DB, table string, cols []string, limit int, offset int64) (int64, error) {
	selectSQL := fmt.Sprintf(
		"SELECT %s FROM %s LIMIT $1 OFFSET $2",
		joinQuoted(cols, pgQuote),
		pgQuote(table),
	)
	rows, err := pgDB.QueryContext(ctx, selectSQL, limit, offset)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	values := make([][]any, 0, limit)
	for rows.Next() {
		row := make([]any, len(cols))
		scanDest := make([]any, len(cols))
		for i := range row {
			scanDest[i] = &row[i]
		}
		if err := rows.Scan(scanDest...); err != nil {
			return 0, err
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, nil
	}

	tx, err := mysqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	insertSQL, args := buildInsert(table, cols, values)
	if _, err := tx.ExecContext(ctx, insertSQL, args...); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(values)), nil
}

func rejectNonEmptyTarget(ctx context.Context, db *sql.DB) error {
	tables, err := mysqlUserTables(ctx, db)
	if err != nil {
		return fmt.Errorf("list target tables: %w", err)
	}
	var nonEmpty []string
	for _, table := range tables {
		n, err := countRows(ctx, db, mysqlQuote(table))
		if err != nil {
			return fmt.Errorf("count target table %s: %w", table, err)
		}
		if n > 0 {
			nonEmpty = append(nonEmpty, fmt.Sprintf("%s(%d)", table, n))
		}
	}
	if len(nonEmpty) > 0 {
		sort.Strings(nonEmpty)
		return fmt.Errorf("target mysql database is not empty: %s; pass --allow-non-empty-target to override",
			strings.Join(nonEmpty, ", "))
	}
	return nil
}

func mysqlUserTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name
		 FROM information_schema.tables
		 WHERE table_schema = DATABASE()
		   AND table_type = 'BASE TABLE'
		   AND table_name NOT IN ('schema_migrations', 'migrate_version')
		 ORDER BY table_name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func pgTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`,
		table,
	).Scan(&exists)
	return exists, err
}

func mysqlTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		table,
	).Scan(&n)
	return n > 0, err
}

func pgColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name
		 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`,
		table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func mysqlColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name
		 FROM information_schema.columns
		 WHERE table_schema = DATABASE()
		   AND table_name = ?
		   AND extra NOT LIKE '%GENERATED%'
		 ORDER BY ordinal_position`,
		table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func intersectColumns(primary, secondary []string) []string {
	seen := make(map[string]bool, len(secondary))
	for _, col := range secondary {
		seen[col] = true
	}
	out := make([]string, 0, len(primary))
	for _, col := range primary {
		if seen[col] {
			out = append(out, col)
		}
	}
	return out
}

func countRows(ctx context.Context, db *sql.DB, quotedTable string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quotedTable).Scan(&n)
	return n, err
}

func buildInsert(table string, cols []string, rows [][]any) (string, []any) {
	args := make([]any, 0, len(rows)*len(cols))
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(cols)), ",") + ")"
	placeholders := make([]string, 0, len(rows))
	for _, row := range rows {
		placeholders = append(placeholders, rowPlaceholder)
		args = append(args, row...)
	}
	stmt := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s",
		mysqlQuote(table),
		joinQuoted(cols, mysqlQuote),
		strings.Join(placeholders, ","),
	)
	return stmt, args
}

func joinQuoted(cols []string, quote func(string) string) string {
	out := make([]string, len(cols))
	for i, col := range cols {
		out[i] = quote(col)
	}
	return strings.Join(out, ",")
}

func pgQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func mysqlQuote(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func mysqlOpenDSN(dsn string) string {
	return strings.TrimPrefix(dsn, "mysql://")
}

func mysqlMigrateDSN(dsn string) string {
	raw := mysqlOpenDSN(dsn)
	cfg, err := mysqlDriver.ParseDSN(raw)
	if err != nil {
		if strings.HasPrefix(dsn, "mysql://") {
			return dsn
		}
		return "mysql://" + dsn
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["multiStatements"] = "true"
	return "mysql://" + cfg.FormatDSN()
}
