package mysql

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MySQL vector storage constants
const (
	defaultTablePrefix = "weknora_embeddings_"
)

// tableSchema defines the MySQL table schema for vector embeddings.
// Vectors are stored as JSON arrays and scored with standard MySQL JSON functions.
const createTableTpl = "CREATE TABLE IF NOT EXISTS %s (" + `
    id                VARCHAR(64) NOT NULL,
    chunk_id          VARCHAR(64),
    knowledge_id      VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    source_id         VARCHAR(255),
    source_type       INT,
    tag_id            VARCHAR(64),
    is_enabled        BOOLEAN DEFAULT TRUE,
    content           LONGTEXT,
    embedding         JSON NULL,
    PRIMARY KEY (id),
    INDEX idx_chunk    (chunk_id),
    INDEX idx_kb       (knowledge_base_id),
    INDEX idx_kid      (knowledge_id),
    INDEX idx_src      (source_id),
    INDEX idx_tag      (tag_id),
    INDEX idx_enabled  (is_enabled),
    FULLTEXT INDEX idx_content_ft (content) WITH PARSER ngram
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`

// ensureTable 保证目标维度对应的表已经存在。
func (r *mysqlRepository) ensureTable(ctx context.Context, dimension int) error {
	tableName := r.getTableName(dimension)
	if len(tableName) > 64 {
		return fmt.Errorf("MySQL table identifier %q exceeds the 64-character limit", tableName)
	}
	exists, err := r.tableExists(ctx, tableName)
	if err != nil {
		return fmt.Errorf("check table existence: %w", err)
	}

	if !exists {
		if err := r.createTable(ctx, tableName); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	if err := r.ensureContentCapacity(ctx, tableName); err != nil {
		return fmt.Errorf("ensure content capacity: %w", err)
	}
	if err := r.ensureEmbeddingNullable(ctx, tableName); err != nil {
		return fmt.Errorf("ensure nullable embedding: %w", err)
	}
	return nil
}

// tableExists 通过 information_schema 判断表是否存在。
func (r *mysqlRepository) tableExists(ctx context.Context, tableName string) (bool, error) {
	const q = `SELECT COUNT(1) FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`
	var n int
	if err := r.db.QueryRowContext(ctx, q, r.database, tableName).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// createTable 发出 CREATE TABLE DDL。
func (r *mysqlRepository) createTable(ctx context.Context, tableName string) error {
	ddl := fmt.Sprintf(createTableTpl, quoteIdentifier(tableName))
	_, err := r.db.ExecContext(ctx, ddl)
	return err
}

func (r *mysqlRepository) ensureContentCapacity(ctx context.Context, tableName string) error {
	const q = `SELECT DATA_TYPE FROM information_schema.columns
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = 'content'`
	var dataType string
	if err := r.db.QueryRowContext(ctx, q, r.database, tableName).Scan(&dataType); err != nil {
		return err
	}
	switch strings.ToLower(dataType) {
	case "longtext":
		return nil
	case "tinytext", "text", "mediumtext":
		_, err := r.db.ExecContext(
			ctx,
			"ALTER TABLE "+quoteIdentifier(tableName)+" MODIFY COLUMN content LONGTEXT",
		)
		return err
	default:
		return fmt.Errorf("unsupported content column type %q", dataType)
	}
}

func (r *mysqlRepository) ensureEmbeddingNullable(ctx context.Context, tableName string) error {
	const q = `SELECT IS_NULLABLE FROM information_schema.columns
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = 'embedding'`
	var nullable string
	if err := r.db.QueryRowContext(ctx, q, r.database, tableName).Scan(&nullable); err != nil {
		return err
	}
	switch strings.ToUpper(nullable) {
	case "YES":
		return nil
	case "NO":
		_, err := r.db.ExecContext(
			ctx,
			"ALTER TABLE "+quoteIdentifier(tableName)+" MODIFY COLUMN embedding JSON NULL",
		)
		return err
	default:
		return fmt.Errorf("unsupported embedding nullability %q", nullable)
	}
}

// listEmbeddingTables 返回当前 database 下所有 <prefix>_% 命名的表。
func (r *mysqlRepository) listEmbeddingTables(ctx context.Context) ([]string, error) {
	const q = `SELECT TABLE_NAME FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ? ESCAPE '\\'`
	likePattern := escapeLikePattern(r.tablePrefix) + "%"
	rows, err := r.db.QueryContext(ctx, q, r.database, likePattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if _, ok := embeddingTableDimension(r.tablePrefix, n); ok {
			names = append(names, n)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(names, func(i, j int) bool {
		left, _ := embeddingTableDimension(r.tablePrefix, names[i])
		right, _ := embeddingTableDimension(r.tablePrefix, names[j])
		return left < right
	})
	return names, nil
}

func embeddingTableDimension(prefix, tableName string) (int, bool) {
	if !strings.HasPrefix(tableName, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(tableName, prefix)
	dimension, err := strconv.Atoi(suffix)
	if err != nil || dimension <= 0 || strconv.Itoa(dimension) != suffix {
		return 0, false
	}
	return dimension, true
}

// buildVectorFilterSQL 构建向量距离过滤条件。
// 调用方传入已构建好的打分表达式，避免依赖非标准向量函数。
func buildVectorFilterSQL(dim int, threshold float64, distanceType string) (string, error) {
	switch distanceType {
	case "cosine":
		return fmt.Sprintf("%%s >= %f", threshold), nil
	case "dot":
		return fmt.Sprintf("%%s >= %f", threshold), nil
	case "euclidean":
		return fmt.Sprintf("%%s <= %f", threshold), nil
	default:
		return "", fmt.Errorf("unsupported distance type: %s", distanceType)
	}
}

// buildKeywordFilterSQL 构建关键词过滤条件。
func buildKeywordFilterSQL() string {
	return `MATCH(content) AGAINST(? IN NATURAL LANGUAGE MODE)`
}

// buildBaseWhereClause 构建基础过滤条件。
func buildBaseWhereClause(params *vectorParams) (string, []interface{}, error) {
	var conditions []string
	var args []interface{}

	if len(params.knowledgeBaseIDs) > 0 {
		placeholders := make([]string, len(params.knowledgeBaseIDs))
		for i, id := range params.knowledgeBaseIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, fmt.Sprintf("knowledge_base_id IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(params.knowledgeIDs) > 0 {
		placeholders := make([]string, len(params.knowledgeIDs))
		for i, id := range params.knowledgeIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, fmt.Sprintf("knowledge_id IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(params.tagIDs) > 0 {
		placeholders := make([]string, len(params.tagIDs))
		for i, id := range params.tagIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, fmt.Sprintf("tag_id IN (%s)", strings.Join(placeholders, ",")))
	}

	conditions = append(conditions, "(is_enabled IS NULL OR is_enabled = TRUE)")

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	return where, args, nil
}

// vectorParams holds parameters for vector search operations.
type vectorParams struct {
	knowledgeBaseIDs []string
	knowledgeIDs     []string
	tagIDs           []string
}
