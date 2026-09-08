package dbinspector

import (
	"context"
	"fmt"
	"net/url"

	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func init() { register(PostgresAdapter{}) }

// PostgresAdapter inspects PostgreSQL (and ParadeDB etc.).
type PostgresAdapter struct{}

// Name returns the connection type this adapter serves.
func (PostgresAdapter) Name() string { return "postgres" }

// DefaultPort returns the default port when the user left it blank.
func (PostgresAdapter) DefaultPort() int { return 5432 }

// Dialector builds the GORM dialector for a stored config.
func (a PostgresAdapter) Dialector(cfg *ConnectionConfig) (gorm.Dialector, error) {
	if cfg.Host == "" || cfg.Database == "" {
		return nil, fmt.Errorf("PostgreSQL 连接需要 host 和 database")
	}
	u := url.URL{Scheme: "postgres", Host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)}
	u.User = url.UserPassword(cfg.Username, cfg.Password)
	q := u.Query()
	q.Set("dbname", cfg.Database)
	q.Set("sslmode", cfg.ExtraString("sslmode"))
	q.Set("connect_timeout", "10")
	if schema := cfg.ExtraString("search_path"); schema != "" {
		q.Set("search_path", schema)
	}
	u.RawQuery = q.Encode()
	return gormpostgres.Open(u.String()), nil
}

// ListTables returns user tables (system schemas excluded).
func (a PostgresAdapter) ListTables(ctx context.Context, db *gorm.DB) ([]TableRef, error) {
	var rows []TableRef
	err := db.WithContext(ctx).Raw(`
		SELECT table_schema AS "schema", table_name AS "name"
		FROM information_schema.tables
		WHERE table_type = 'BASE TABLE'
		  AND table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema, table_name`).Scan(&rows).Error
	return rows, err
}

// ListColumns returns the columns of one table.
func (a PostgresAdapter) ListColumns(ctx context.Context, db *gorm.DB, schema, table string) ([]ColumnSchema, error) {
	var rows []struct {
		ColumnName string `gorm:"column:column_name"`
		DataType   string `gorm:"column:data_type"`
		IsNullable string `gorm:"column:is_nullable"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, quoteIdent(schema), quoteIdent(table)).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	// primary keys live in a separate catalog query
	pkSet := map[string]bool{}
	var pks []struct {
		ColumnName string `gorm:"column:column_name"`
	}
	err = db.WithContext(ctx).Raw(`
		SELECT kcu.column_name AS column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = ? AND tc.table_name = ?`,
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
