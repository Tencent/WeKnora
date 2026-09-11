package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// duckdbExtensions is the list of DuckDB extensions required by WeKnora's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

const (
	duckdbExtensionInstallAttempts = 4
	duckdbExtensionRetryDelay      = 2 * time.Second
	duckdbExtensionAttemptTimeout  = 30 * time.Second
)

// retryWithBackoff retries an operation that may fail because an external
// service is temporarily unavailable. The context keeps a cancelled build
// from waiting for the next backoff interval.
func retryWithBackoff(ctx context.Context, attempts int, initialDelay time.Duration, operation func() error) error {
	if attempts < 1 {
		attempts = 1
	}

	var err error
	delay := initialDelay
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		if attempt == attempts {
			break
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}

	return err
}

func installDuckDBExtension(ctx context.Context, sqlDB *sql.DB, extension string) error {
	if err := retryWithBackoff(ctx, duckdbExtensionInstallAttempts, duckdbExtensionRetryDelay, func() error {
		attemptCtx, cancel := context.WithTimeout(ctx, duckdbExtensionAttemptTimeout)
		defer cancel()
		_, err := sqlDB.ExecContext(attemptCtx, fmt.Sprintf("INSTALL %s;", extension))
		return err
	}); err != nil {
		return fmt.Errorf("failed to install %s extension after %d attempts: %w", extension, duckdbExtensionInstallAttempts, err)
	}

	if err := retryWithBackoff(ctx, duckdbExtensionInstallAttempts, duckdbExtensionRetryDelay, func() error {
		attemptCtx, cancel := context.WithTimeout(ctx, duckdbExtensionAttemptTimeout)
		defer cancel()
		_, err := sqlDB.ExecContext(attemptCtx, fmt.Sprintf("LOAD %s;", extension))
		return err
	}); err != nil {
		return fmt.Errorf("failed to load %s extension after %d attempts: %w", extension, duckdbExtensionInstallAttempts, err)
	}

	return nil
}

func downloadExtensions() {
	ctx := context.Background()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	for _, ext := range duckdbExtensions {
		if err := installDuckDBExtension(ctx, sqlDB, ext); err != nil {
			panic(err)
		}
	}
}

func main() {
	downloadExtensions()
}
