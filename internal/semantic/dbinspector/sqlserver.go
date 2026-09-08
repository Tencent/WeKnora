package dbinspector

import (
	"context"
	"fmt"
	"net/url"

	gormsqlserver "gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

func init() { register(SQLServerAdapter{}) }

// SQLServerAdapter inspects Microsoft SQL Server via INFORMATION_SCHEMA.
type SQLServerAdapter struct{}

// Name returns the connection type this adapter serves.
func (SQLServerAdapter) Name() string { return "sqlserver" }

// DefaultPort returns the default port when the user left it blank.
func (SQLServerAdapter) DefaultPort() int { return 1433 }

// Dialector builds the GORM dialector for a stored config.
func (a SQLServerAdapter) Dialector(cfg *ConnectionConfig) (gorm.Dialector, error) {
	if cfg.Host == "" || cfg.Database == "" {
		return nil, fmt.Errorf("SQL Server 连接需要 host 和 database")
	}
	u := url.URL{
		Scheme: "sqlserver",
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
	}
	u.User = url.UserPassword(cfg.Username, cfg.Password)
	q := u.Query()
	q.Set("database", cfg.Database)
	u.RawQuery = q.Encode()
	return gormsqlserver.Open(u.String()), nil
}

// ListTables returns user tables (system schemas excluded).
func (a SQLServerAdapter) ListTables(ctx context.Context, db *gorm.DB) ([]TableRef, error) {
	var rows []TableRef
	err := db.WithContext(ctx).Raw(`
		SELECT TABLE_SCHEMA AS "schema", TABLE_NAME AS "name"
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_TYPE = 'BASE TABLE'
		  AND TABLE_SCHEMA NOT IN ('sys', 'guest', 'INFORMATION_SCHEMA', 'db_owner', 'db_datareader', 'db_datawriter')
		ORDER BY TABLE_SCHEMA, TABLE_NAME`).Scan(&rows).Error
	return rows, err
}

// ListColumns returns the columns of one table.
func (a SQLServerAdapter) ListColumns(ctx context.Context, db *gorm.DB, schema, table string) ([]ColumnSchema, error) {
	var rows []struct {
		ColumnName string `gorm:"column:COLUMN_NAME"`
		DataType   string `gorm:"column:DATA_TYPE"`
		IsNullable string `gorm:"column:IS_NULLABLE"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, quoteIdent(schema), quoteIdent(table)).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	pkSet := map[string]bool{}
	var pks []struct {
		ColumnName string `gorm:"column:COLUMN_NAME"`
	}
	err = db.WithContext(ctx).Raw(`
		SELECT ku.COLUMN_NAME AS COLUMN_NAME
		FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE ku
		  ON tc.CONSTRAINT_NAME = ku.CONSTRAINT_NAME
		 AND tc.TABLE_SCHEMA = ku.TABLE_SCHEMA
		WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
		  AND tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ?`,
		quoteIdent(schema), quoteIdent(table)).Scan(&pks).Error
	if err == nil {
		for _, p := range pks {
			pkSet[p.ColumnName] = true
		}
	}
	out := make([]ColumnSchema, 0, len(rows))
	for _, r := range rows {
		out = append(out, ColumnSchema{
			Name:       r.ColumnName,
			DataType:   r.DataType,
			PrimaryKey: pkSet[r.ColumnName],
			Nullable:   r.IsNullable == "YES",
		})
	}
	return out, nil
}
