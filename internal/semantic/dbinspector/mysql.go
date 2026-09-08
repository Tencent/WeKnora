package dbinspector

import (
	"context"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func init() { register(MySQLAdapter{}) }

// MySQLAdapter inspects MySQL / MariaDB. The current database name doubles
// as the schema qualifier.
type MySQLAdapter struct{}

// Name returns the connection type this adapter serves.
func (MySQLAdapter) Name() string { return "mysql" }

// DefaultPort returns the default port when the user left it blank.
func (MySQLAdapter) DefaultPort() int { return 3306 }

// Dialector builds the GORM dialector for a stored config.
func (a MySQLAdapter) Dialector(cfg *ConnectionConfig) (gorm.Dialector, error) {
	if cfg.Host == "" || cfg.Database == "" {
		return nil, fmt.Errorf("MySQL 连接需要 host 和 database")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=UTC&timeout=10s&readTimeout=30s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	for k, v := range cfg.Extra {
		if s, ok := v.(string); ok && k != "" {
			dsn += "&" + k + "=" + s
		}
	}
	return mysql.Open(dsn), nil
}

// ListTables returns user tables (system schemas excluded).
func (a MySQLAdapter) ListTables(ctx context.Context, db *gorm.DB) ([]TableRef, error) {
	var rows []struct {
		TableSchema string `gorm:"column:TABLE_SCHEMA"`
		TableName   string `gorm:"column:TABLE_NAME"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT TABLE_SCHEMA, TABLE_NAME
		FROM information_schema.tables
		WHERE table_type = 'BASE TABLE'
		  AND table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		ORDER BY TABLE_SCHEMA, TABLE_NAME`).Scan(&rows).Error
	out := make([]TableRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, TableRef{Schema: r.TableSchema, Name: r.TableName})
	}
	return out, err
}

// ListColumns returns the columns of one table.
func (a MySQLAdapter) ListColumns(ctx context.Context, db *gorm.DB, schema, table string) ([]ColumnSchema, error) {
	var rows []struct {
		ColumnName string `gorm:"column:COLUMN_NAME"`
		DataType   string `gorm:"column:DATA_TYPE"`
		IsNullable string `gorm:"column:IS_NULLABLE"`
		ColumnKey  string `gorm:"column:COLUMN_KEY"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_KEY
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ORDINAL_POSITION`, quoteIdent(schema), quoteIdent(table)).Scan(&rows).Error
	out := make([]ColumnSchema, 0, len(rows))
	for _, r := range rows {
		out = append(out, ColumnSchema{
			Name:       r.ColumnName,
			DataType:   r.DataType,
			PrimaryKey: r.ColumnKey == "PRI",
			Nullable:   r.IsNullable == "YES",
		})
	}
	return out, err
}
