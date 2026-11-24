// Package hologres implements WeKnora's graph contracts on Hologres V5+ AGE.
// It is not a vector/BM25 retrieval engine or a general-purpose Cypher proxy.
package hologres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DefaultGraph is the dedicated AGE graph name used when none is configured.
const DefaultGraph = "weknora_graph"

var graphIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Config is separate from WeKnora's primary database configuration. Graph is a
// dedicated AGE schema; tenant/KB/document identifiers remain bound SQL data.
type Config struct {
	DSN        string
	Graph      string
	AutoCreate bool
}

// ConfigFromEnv reads and validates the dedicated Hologres graph configuration.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		DSN:   strings.TrimSpace(os.Getenv("HOLOGRES_GRAPH_DSN")),
		Graph: strings.TrimSpace(os.Getenv("HOLOGRES_GRAPH_NAME")),
	}
	if value := strings.TrimSpace(os.Getenv("HOLOGRES_GRAPH_AUTO_CREATE")); value != "" {
		var err error
		cfg.AutoCreate, err = strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HOLOGRES_GRAPH_AUTO_CREATE must be a boolean")
		}
	}
	if cfg.Graph == "" {
		cfg.Graph = DefaultGraph
	}
	if cfg.DSN == "" {
		return Config{}, fmt.Errorf("HOLOGRES_GRAPH_DSN is required for GRAPH_BACKEND=hologres")
	}
	if !graphIdentifier.MatchString(cfg.Graph) {
		return Config{}, fmt.Errorf("HOLOGRES_GRAPH_NAME must be a lowercase SQL identifier of at most 63 characters")
	}
	return cfg, nil
}

// Open uses the existing pgx dependency and never includes the DSN in errors.
// Only an explicitly enabled Hologres graph backend should call this function.
func Open(ctx context.Context, cfg Config) (*Repository, error) {
	pgcfg, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("invalid Hologres graph connection configuration")
	}
	// The context bounds each query; a server timeout also limits abandoned work.
	pgcfg.RuntimeParams["statement_timeout"] = "30000"
	// Keep ag_catalog OUT of search_path: Hologres may deparse expressions
	// for its PostgreSQL executor, whose path does not inherit this session's.
	// All AGE functions/types are qualified so deparsing keeps their schema.
	pgcfg.RuntimeParams["search_path"] = "public"
	db := stdlib.OpenDB(*pgcfg)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	repo, err := NewRepository(db, cfg.Graph)
	if err != nil {
		_ = db.Close() // Preserve the initialization error if cleanup also fails.
		return nil, err
	}
	if err = db.PingContext(ctx); err == nil {
		err = repo.EnsureSchema(ctx, cfg.AutoCreate)
	}
	if err != nil {
		_ = db.Close() // Preserve the initialization error if cleanup also fails.
		return nil, fmt.Errorf("initialize Hologres graph backend: %w", err)
	}
	return repo, nil
}

// Close releases the dedicated Hologres graph connection pool.
func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// beginWrite pins the SET and transaction to the same physical connection.
// Hologres mixed DML is opt-in and locks accessed tables, including reads.
// All graph writers access entity before relation to keep lock ordering uniform.
func (r *Repository) beginWrite(ctx context.Context) (*sql.Conn, *sql.Tx, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err = conn.ExecContext(ctx, "SET hg_experimental_enable_transaction = on"); err != nil {
		_ = conn.Close() // Preserve the setup error if cleanup also fails.
		return nil, nil, fmt.Errorf("enable Hologres mixed-DML transactions: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_ = conn.Close() // Preserve the setup error if cleanup also fails.
		return nil, nil, err
	}
	return conn, tx, nil
}
