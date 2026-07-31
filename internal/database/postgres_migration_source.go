package database

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
)

const (
	postgresMigrationsDirectory           = "migrations/versioned"
	requiredPostgresMigrationVersion uint = 72
	requiredPostgresMigrationName         = "knowledge_folder_index_pending"
)

var postgresMigrationFilePattern = regexp.MustCompile(
	`^([0-9]{6})_([a-z0-9][a-z0-9_-]*)\.(up|down)\.sql$`,
)

type postgresMigrationInventory struct {
	directory string
	files     []string
	versions  []uint
}

type postgresMigrationPair struct {
	name       string
	directions map[string]string
}

type postgresMigratorFactory func(sourceURL string, databaseURL string) (*migrate.Migrate, error)

func validatePostgresMigrationsDirectory(
	directory string,
) (*postgresMigrationInventory, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, fmt.Errorf("postgres migrations directory is empty")
	}
	absoluteDirectory, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return nil, fmt.Errorf("resolve postgres migrations directory %q: %w", directory, err)
	}
	directoryInfo, err := os.Stat(absoluteDirectory)
	if err != nil {
		return nil, fmt.Errorf("stat postgres migrations directory %q: %w", absoluteDirectory, err)
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("postgres migrations path %q is not a directory", absoluteDirectory)
	}

	entries, err := os.ReadDir(absoluteDirectory)
	if err != nil {
		return nil, fmt.Errorf("read postgres migrations directory %q: %w", absoluteDirectory, err)
	}

	pairs := make(map[uint]*postgresMigrationPair)
	for _, entry := range entries {
		filename := entry.Name()
		if !strings.HasSuffix(strings.ToLower(filename), ".sql") {
			continue
		}

		match := postgresMigrationFilePattern.FindStringSubmatch(filename)
		if match == nil {
			return nil, fmt.Errorf("invalid postgres migration filename %q", filename)
		}
		versionValue, parseErr := strconv.ParseUint(match[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf(
				"parse postgres migration version in %q: %w",
				filename,
				parseErr,
			)
		}
		version := uint(versionValue)

		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("postgres migration %q is not a regular file", filename)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("stat postgres migration %q: %w", filename, infoErr)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("postgres migration %q is not a regular file", filename)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("postgres migration %q is empty", filename)
		}

		pair := pairs[version]
		if pair == nil {
			pair = &postgresMigrationPair{directions: make(map[string]string, 2)}
			pairs[version] = pair
		}
		direction := match[3]
		if existing := pair.directions[direction]; existing != "" {
			return nil, fmt.Errorf(
				"postgres migration version %06d has duplicate %s files %q and %q",
				version,
				direction,
				existing,
				filename,
			)
		}
		if pair.name != "" && pair.name != match[2] {
			return nil, fmt.Errorf(
				"postgres migration version %06d has mismatched names %q and %q",
				version,
				pair.name,
				match[2],
			)
		}
		pair.name = match[2]
		pair.directions[direction] = filename
	}

	versions := make([]uint, 0, len(pairs))
	for version := range pairs {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	files := make([]string, 0, len(versions)*2)
	for _, version := range versions {
		pair := pairs[version]
		up := pair.directions["up"]
		down := pair.directions["down"]
		if up == "" || down == "" {
			return nil, fmt.Errorf(
				"postgres migration version %06d must have one up and one down file",
				version,
			)
		}
		files = append(files, filepath.ToSlash(up), filepath.ToSlash(down))
	}
	if len(versions) == 0 || versions[0] != 0 {
		return nil, fmt.Errorf("postgres migration version 000000 is missing")
	}
	maxVersion := versions[len(versions)-1]
	for version := uint(0); version <= maxVersion; version++ {
		if pairs[version] == nil {
			return nil, fmt.Errorf("postgres migration version %06d is missing", version)
		}
	}

	requiredPair := pairs[requiredPostgresMigrationVersion]
	if requiredPair == nil {
		return nil, fmt.Errorf(
			"required postgres migration version %06d is missing",
			requiredPostgresMigrationVersion,
		)
	}
	if requiredPair.name != requiredPostgresMigrationName {
		return nil, fmt.Errorf(
			"required postgres migration version %06d has name %q, expected %q",
			requiredPostgresMigrationVersion,
			requiredPair.name,
			requiredPostgresMigrationName,
		)
	}

	return &postgresMigrationInventory{
		directory: absoluteDirectory,
		files:     files,
		versions:  versions,
	}, nil
}

func postgresMigrationSourceURL(directory string) string {
	return "file://" + filepath.Clean(directory)
}

func newValidatedPostgresMigrator(
	directory string,
	databaseURL string,
	factory postgresMigratorFactory,
) (*migrate.Migrate, error) {
	inventory, err := validatePostgresMigrationsDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("validate postgres migration source: %w", err)
	}

	migrator, err := factory(
		postgresMigrationSourceURL(inventory.directory),
		databaseURL,
	)
	if err != nil {
		return nil, err
	}
	return migrator, nil
}
