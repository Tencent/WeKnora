package dbinspector

import (
	"context"
	"fmt"
	"strings"

	gormclickhouse "gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

func init() { register(ClickHouseAdapter{}) }

// ClickHouseAdapter inspects ClickHouse via system.tables/system.columns.
type ClickHouseAdapter struct{}

// Name returns the connection type this adapter serves.
func (ClickHouseAdapter) Name() string { return "clickhouse" }

// DefaultPort returns the default port when the user left it blank.
func (ClickHouseAdapter) DefaultPort() int { return 8123 }

// Dialector builds the GORM dialector for a stored config.
func (a ClickHouseAdapter) Dialector(cfg *ConnectionConfig) (gorm.Dialector, error) {
	if cfg.Host == "" || cfg.Database == "" {
		return nil, fmt.Errorf("ClickHouse 连接需要 host 和 database")
	}
	dsn := fmt.Sprintf("clickhouse://%s:%d/%s?username=%s&password=%s&dial_timeout=10s&read_timeout=30s",
		cfg.Host, cfg.Port, cfg.Database, cfg.Username, cfg.Password)
	if v := cfg.ExtraString("secure"); v == "true" {
		dsn += "&secure=true"
	}
	if v := cfg.ExtraString("skip_verify"); v != "" {
		dsn += "&skip_verify=" + v
	}
	return gormclickhouse.Open(dsn), nil
}

// ListTables returns user tables (system schemas excluded).
func (a ClickHouseAdapter) ListTables(ctx context.Context, db *gorm.DB) ([]TableRef, error) {
	var rows []TableRef
	err := db.WithContext(ctx).Raw(`
		SELECT database AS "schema", name AS "name"
		FROM system.tables
		WHERE database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema')
		  AND engine NOT LIKE '%View%'
		ORDER BY database, name`).Scan(&rows).Error
	return rows, err
}

// ListColumns returns the columns of one table.
func (a ClickHouseAdapter) ListColumns(ctx context.Context, db *gorm.DB, schema, table string) ([]ColumnSchema, error) {
	var rows []struct {
		Name           string `gorm:"column:name"`
		Type           string `gorm:"column:type"`
		IsInPrimaryKey bool   `gorm:"column:is_in_primary_key"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT name, type, is_in_primary_key
		FROM system.columns
		WHERE database = ? AND table = ?
		ORDER BY position`, quoteIdent(schema), quoteIdent(table)).Scan(&rows).Error
	out := make([]ColumnSchema, 0, len(rows))
	for _, r := range rows {
		// ClickHouse types look like Nullable(String) / LowCardinality(String)
		base := r.Type
		for {
			inner, ok := unwrapWrapper(base)
			if !ok {
				break
			}
			base = inner
		}
		nullable := strings.Contains(r.Type, "Nullable(")
		out = append(out, ColumnSchema{
			Name:       r.Name,
			DataType:   base,
			PrimaryKey: r.IsInPrimaryKey,
			Nullable:   nullable,
		})
	}
	return out, err
}

// unwrapWrapper strips one Wrapper(...) layer (case-sensitive as CH types are).
func unwrapWrapper(t string) (string, bool) {
	open := strings.Index(t, "(")
	if open <= 0 || !strings.HasSuffix(t, ")") {
		return "", false
	}
	switch t[:open] {
	case "Nullable", "LowCardinality", "DateTime64":
		return t[open+1 : len(t)-1], true
	default:
		return "", false
	}
}
