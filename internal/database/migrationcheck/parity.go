package migrationcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var migrationFilePattern = regexp.MustCompile(`^(\d{6})_(.+)\.(up|down)\.sql$`)

type migrationFile struct {
	Version string
	Name    string
	Dir     string
	Up      bool
	Down    bool
}

// CheckMySQLParity verifies that the MySQL migration directory can represent
// the PostgreSQL schema at the MySQL baseline and every post-baseline schema
// change. It deliberately checks only migration topology; schema equivalence is
// covered by real database integration tests.
func CheckMySQLParity(root string) error {
	versioned, err := collectMigrationFiles(filepath.Join(root, "versioned"))
	if err != nil {
		return err
	}
	mysql, err := collectMigrationFiles(filepath.Join(root, "mysql"))
	if err != nil {
		if os.IsNotExist(err) {
			mysql = map[string]migrationFile{}
		} else {
			return err
		}
	}
	if err := checkMySQLMigrationFiles(filepath.Join(root, "mysql")); err != nil {
		return err
	}

	head := latestMigrationVersion(versioned)
	if head == "" {
		return fmt.Errorf("no PostgreSQL migrations found in migrations/versioned")
	}

	baselineVersion := mysqlBaselineVersion(mysql)
	if baselineVersion == "" {
		return fmt.Errorf("missing MySQL migration baseline for current PostgreSQL head %s", head)
	}
	if baselineVersion > head {
		return fmt.Errorf("MySQL baseline version %s is newer than PostgreSQL head %s", baselineVersion, head)
	}

	for version, pg := range versioned {
		if version <= baselineVersion {
			continue
		}
		my, ok := mysql[version]
		if !ok {
			return fmt.Errorf("missing MySQL migration pair for PostgreSQL version %s", version)
		}
		if pg.Up && !my.Up {
			return fmt.Errorf("missing MySQL migration migrations/mysql/%s_%s.up.sql", version, pg.Name)
		}
		if pg.Down && !my.Down {
			return fmt.Errorf("missing MySQL migration migrations/mysql/%s_%s.down.sql", version, pg.Name)
		}
	}

	return nil
}

func collectMigrationFiles(dir string) (map[string]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := map[string]migrationFile{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 4 {
			continue
		}
		version, name, direction := matches[1], matches[2], matches[3]
		current := files[version]
		if current.Version == "" {
			current.Version = version
			current.Name = name
			current.Dir = dir
		}
		switch direction {
		case "up":
			current.Up = true
		case "down":
			current.Down = true
		}
		files[version] = current
	}
	return files, nil
}

func checkMySQLMigrationFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !migrationFilePattern.MatchString(entry.Name()) {
			return fmt.Errorf("non-versioned MySQL migration file %s", filepath.Join(dir, entry.Name()))
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := checkMySQLMigrationSQL(path, string(content)); err != nil {
			return err
		}
	}
	return nil
}

func checkMySQLMigrationSQL(path, sql string) error {
	if strings.HasPrefix(sql, "\uFEFF") {
		return fmt.Errorf("%s starts with a UTF-8 BOM, which MySQL treats as SQL text", path)
	}

	indexIfNotExists := regexp.MustCompile(`(?i)CREATE\s+(UNIQUE\s+)?INDEX\s+IF\s+NOT\s+EXISTS`)
	if indexIfNotExists.MatchString(sql) {
		return fmt.Errorf("%s uses CREATE INDEX IF NOT EXISTS, which MySQL does not support", path)
	}
	datetimeDefault := regexp.MustCompile(`(?i)DATETIME\(6\)\s+(NOT\s+NULL\s+)?DEFAULT\s+CURRENT_TIMESTAMP([^(\w]|$)`)
	if datetimeDefault.MatchString(sql) {
		return fmt.Errorf("%s uses DATETIME(6) DEFAULT CURRENT_TIMESTAMP without matching precision", path)
	}

	for _, statement := range strings.Split(sql, ";") {
		trimmed := strings.TrimSpace(statement)
		if trimmed == "" {
			continue
		}
		if isCreateIndexStatement(trimmed) && strings.Contains(strings.ToUpper(trimmed), " WHERE ") {
			return fmt.Errorf("%s uses a partial index WHERE clause, which MySQL does not support", path)
		}
	}

	return nil
}

func isCreateIndexStatement(statement string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(statement))
	return strings.HasPrefix(normalized, "CREATE INDEX ") ||
		strings.HasPrefix(normalized, "CREATE UNIQUE INDEX ")
}

func mysqlBaselineVersion(files map[string]migrationFile) string {
	var versions []string
	for version, file := range files {
		if file.Up && file.Down && strings.Contains(file.Name, "mysql_baseline") {
			versions = append(versions, version)
		}
	}
	sort.Strings(versions)
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}

func latestMigrationVersion(files map[string]migrationFile) string {
	versions := make([]string, 0, len(files))
	for version := range files {
		if strings.TrimSpace(version) != "" {
			versions = append(versions, version)
		}
	}
	sort.Strings(versions)
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}
