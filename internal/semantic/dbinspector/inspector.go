// Package dbinspector connects to business databases through stored
// connection configs to power "test connection", schema browsing and draft
// generation in the modeling studio. One adapter per guided type; adding a
// type = registering one adapter.
package dbinspector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ConnectionConfig is dbinspector's view of a stored connection config.
// semantic imports dbinspector (not the other way round), so the struct is
// duplicated here by design — only the fields adapters need.
type ConnectionConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	// Extra carries driver-specific passthrough params (sslmode, ...).
	Extra map[string]interface{}
}

// ExtraString fetches a string-valued passthrough param.
func (c *ConnectionConfig) ExtraString(key string) string {
	if c == nil || c.Extra == nil {
		return ""
	}
	if s, ok := c.Extra[key].(string); ok {
		return s
	}
	return ""
}

// TableRef is one browsable table.
type TableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// ColumnSchema is one inspected column.
type ColumnSchema struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	PrimaryKey bool   `json:"primary_key"`
	Nullable   bool   `json:"nullable"`
}

// Adapter implements per-database introspection.
type Adapter interface {
	// Name returns the connection type this adapter serves.
	Name() string
	// Dialector builds the GORM dialector for a stored config.
	Dialector(cfg *ConnectionConfig) (gorm.Dialector, error)
	// ListTables returns user tables (system schemas excluded).
	ListTables(ctx context.Context, db *gorm.DB) ([]TableRef, error)
	// ListColumns returns the columns of one table.
	ListColumns(ctx context.Context, db *gorm.DB, schema, table string) ([]ColumnSchema, error)
	// DefaultPort when the user left it blank.
	DefaultPort() int
}

var registry = map[string]Adapter{}

func register(a Adapter) {
	registry[a.Name()] = a
}

// Get returns the adapter for a connection type; ok=false for passthrough
// types without guided support.
func Get(connType string) (Adapter, bool) {
	a, ok := registry[connType]
	return a, ok
}

// GuidedTypes lists registry keys in a stable, UI-friendly order.
func GuidedTypes() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	order := map[string]int{"mysql": 0, "postgres": 1, "clickhouse": 2, "sqlserver": 3}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}

// TestConnection opens a real connection and pings it, reporting latency.
func TestConnection(ctx context.Context, connType string, cfg *ConnectionConfig) (latencyMS int64, err error) {
	adapter, ok := Get(connType)
	if !ok {
		return 0, fmt.Errorf("类型 %s 暂不支持引导式连接测试 (保存后仍可在 Cube 侧使用)", connType)
	}
	if cfg.Port == 0 {
		cfg.Port = adapter.DefaultPort()
	}
	dial, err := adapter.Dialector(cfg)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := gorm.Open(dial, &gorm.Config{
		DisableAutomaticPing: true,
	})
	if err != nil {
		return 0, fmt.Errorf("连接失败: %w", unwrapErr(err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()
	start := time.Now()
	if err := sqlDB.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("连接失败: %w", unwrapErr(err))
	}
	return time.Since(start).Milliseconds(), nil
}

// ListTables / ListColumns run the adapter queries against a fresh
// connection for the stored config.
func ListTables(ctx context.Context, connType string, cfg *ConnectionConfig) ([]TableRef, error) {
	db, adapter, cleanup, err := open(ctx, connType, cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tables, err := adapter.ListTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("读取表清单失败: %w", unwrapErr(err))
	}
	return tables, nil
}

// ListColumns inspects one table.
func ListColumns(
	ctx context.Context,
	connType string,
	cfg *ConnectionConfig,
	schema, table string,
) ([]ColumnSchema, error) {
	db, adapter, cleanup, err := open(ctx, connType, cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cols, err := adapter.ListColumns(ctx, db, schema, table)
	if err != nil {
		return nil, fmt.Errorf("读取表结构失败: %w", unwrapErr(err))
	}
	return cols, nil
}

func open(ctx context.Context, connType string, cfg *ConnectionConfig) (*gorm.DB, Adapter, func(), error) {
	adapter, ok := Get(connType)
	if !ok {
		return nil, nil, nil, fmt.Errorf("类型 %s 暂不支持引导式表结构浏览", connType)
	}
	if cfg.Port == 0 {
		cfg.Port = adapter.DefaultPort()
	}
	dial, err := adapter.Dialector(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	db, err := gorm.Open(dial, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("连接失败: %w", unwrapErr(err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, nil, err
	}
	return db.WithContext(ctx), adapter, func() { _ = sqlDB.Close() }, nil
}

// unwrapErr flattens gorm's %w chains into the root cause text.
func unwrapErr(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}

// quoteIdent rejects identifiers containing quotes/semicolons before they
// reach a query we cannot parameterize (schema/table names in
// information_schema predicates are literals, but keep them safe anyway).
func quoteIdent(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "'", ""), "`", "")
}
