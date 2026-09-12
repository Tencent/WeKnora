// Command migrate-runner inspects and applies verified database migrations.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
)

func run(args []string, stdout, stderr io.Writer) int {
	action := "inspect"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("weknora-migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "migrations", "complete migrations root")
	sqlitePath := flags.String("sqlite-path", "", "explicit raw SQLite database file")
	backup := flags.String(
		"backup-id",
		os.Getenv("MIGRATION_BACKUP_ID"),
		"verified backup identifier for existing databases",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if action == "help" {
		_, _ = fmt.Fprintln(stdout, "weknora-migrate {inspect|plan|version|apply|up|create} [--root "+
			"migrations] [--sqlite-path file] [--backup-id id]")
		return 0
	}
	if action == "create" {
		if err := createMigrationPair(*root, flags.Arg(0)); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if action != "inspect" && action != "plan" && action != "version" && action != "apply" && action != "up" {
		_, _ = fmt.Fprintln(stderr, "unsupported command: version forcing and destructive rollback are "+
			"unavailable; use inspect and a verified recovery plan")
		return 2
	}
	dsn := os.Getenv("MIGRATION_DSN")
	if dsn == "" {
		dsn = os.Getenv("DB_URL")
	}
	if *sqlitePath == "" && os.Getenv("DB_DRIVER") == "sqlite" {
		*sqlitePath = os.Getenv("DB_PATH")
	}
	if *sqlitePath != "" {
		dsn = "sqlite3://explicit-file"
	} else if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "set MIGRATION_DSN (or DB_URL), or provide --sqlite-path; no implicit "+
			"database target is selected")
		return 2
	}
	opts := database.MigrationOptions{MigrationsRoot: *root, SQLiteDBPath: *sqlitePath, BackupID: *backup}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var status *database.MigrationStatus
	var err error
	if action == "apply" || action == "up" {
		status, err = database.ApplyMigrations(ctx, dsn, opts)
	} else {
		status, err = database.InspectMigrations(ctx, dsn, opts)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(status); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func createMigrationPair(root, name string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) {
		return fmt.Errorf("migration name must use lowercase letters, digits and underscores")
	}
	maxVersion := -1
	for _, dialect := range []string{"postgres", "sqlite"} {
		files, err := filepath.Glob(filepath.Join(root, "topic3", dialect, "*.up.sql"))
		if err != nil {
			return err
		}
		current := 0
		for _, path := range files {
			version, err := strconv.Atoi(strings.SplitN(filepath.Base(path), "_", 2)[0])
			if err != nil {
				return err
			}
			if version > current {
				current = version
			}
		}
		if maxVersion >= 0 && current != maxVersion {
			return fmt.Errorf("topic3 chain versions differ")
		}
		maxVersion = current
	}
	paths := []string{}
	for _, dialect := range []string{"postgres", "sqlite"} {
		for _, direction := range []string{"up", "down"} {
			path := filepath.Join(
				root,
				"topic3",
				dialect,
				fmt.Sprintf("%06d_%s.%s.sql", maxVersion+1, name, direction),
			)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("migration file already exists: %s", path)
			} else if !os.IsNotExist(err) {
				return err
			}
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString("-- Define and verify this migration before regenerating structure " +
			"profiles.\n")
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
