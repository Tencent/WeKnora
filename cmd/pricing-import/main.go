// Package main imports versioned model pricing rules.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		writeOutput(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("pricing-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "path to a versioned model pricing YAML file")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("pricing-import: %w", err)
	}
	if *file == "" {
		return fmt.Errorf("pricing-import: --file is required")
	}
	db, err := openDatabase()
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("pricing-import: get database handle: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if !db.Migrator().HasTable("model_pricing") {
		return fmt.Errorf("pricing-import: model_pricing table is missing; apply existing migrations first")
	}

	importer := service.NewPricingImporter(repository.NewPricingRepository(db))
	result, err := importer.ImportFile(context.Background(), *file)
	if err != nil {
		return err
	}
	writeOutput(stdout, "pricing_version=%s source=%s inserted=%d no_op=%d closed=%d\n",
		result.PricingVersion, result.SourceName, result.Inserted, result.NoOp, result.Closed)
	return nil
}

func writeOutput(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func openDatabase() (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver := os.Getenv("DB_DRIVER"); driver {
	case "postgres":
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
			os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))
		dialector = postgres.Open(dsn)
	case "sqlite":
		path := os.Getenv("DB_PATH")
		if path == "" {
			path = "./data/weknora.db"
		}
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if _, err := os.Stat(dir); err != nil {
				return nil, fmt.Errorf("pricing-import: SQLite directory %s is not accessible: %w", dir, err)
			}
		}
		dialector = sqlite.Open(path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	default:
		return nil, fmt.Errorf("pricing-import: unsupported DB_DRIVER %q", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		return nil, fmt.Errorf("pricing-import: open database: %w", err)
	}
	if db.Name() == "sqlite" {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("pricing-import: get SQLite handle: %w", err)
		}
		sqlDB.SetMaxOpenConns(1)
	}
	return db, nil
}
