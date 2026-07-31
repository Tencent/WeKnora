package sqlitemigrations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RequiredVersion is the minimum SQLite schema version required by this build.
const RequiredVersion uint = 2

var migrationFilePattern = regexp.MustCompile(
	`^([0-9]{6})_([a-z0-9][a-z0-9_-]*)\.(up|down)\.sql$`,
)

// Inventory is the validated, canonical SQLite migration file set.
type Inventory struct {
	Files []string
}

type migrationPair struct {
	name       string
	directions map[string]string
}

// ValidateDirectory validates SQLite migration pairing and continuity.
func ValidateDirectory(directory string, requiredVersion uint) (*Inventory, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read sqlite migrations directory %q: %w", directory, err)
	}

	pairs := make(map[uint]*migrationPair)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}

		match := migrationFilePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("invalid sqlite migration filename %q", name)
		}
		versionValue, parseErr := strconv.ParseUint(match[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parse sqlite migration version in %q: %w", name, parseErr)
		}
		version := uint(versionValue)

		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("stat sqlite migration %q: %w", name, infoErr)
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("sqlite migration %q is not a regular file", name)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("sqlite migration %q is empty", name)
		}

		pair := pairs[version]
		if pair == nil {
			pair = &migrationPair{directions: make(map[string]string, 2)}
			pairs[version] = pair
		}
		if existing := pair.directions[match[3]]; existing != "" {
			return nil, fmt.Errorf(
				"sqlite migration version %06d has duplicate %s files %q and %q",
				version,
				match[3],
				existing,
				name,
			)
		}
		if pair.name != "" && pair.name != match[2] {
			return nil, fmt.Errorf(
				"sqlite migration version %06d has mismatched names %q and %q",
				version,
				pair.name,
				match[2],
			)
		}
		pair.name = match[2]
		pair.directions[match[3]] = name
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("sqlite migrations directory %q contains no migrations", directory)
	}

	versions := make([]uint, 0, len(pairs))
	for version := range pairs {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	if versions[0] != 0 {
		return nil, fmt.Errorf("sqlite migration version 000000 is missing")
	}
	maxVersion := versions[len(versions)-1]
	if maxVersion < requiredVersion {
		return nil, fmt.Errorf(
			"required sqlite migration version %06d is missing",
			requiredVersion,
		)
	}
	if uint(len(versions)) != maxVersion+1 {
		for version := uint(0); version <= maxVersion; version++ {
			if pairs[version] == nil {
				return nil, fmt.Errorf("sqlite migration version %06d is missing", version)
			}
		}
	}

	files := make([]string, 0, len(versions)*2)
	for _, version := range versions {
		pair := pairs[version]
		up := pair.directions["up"]
		down := pair.directions["down"]
		if up == "" || down == "" {
			return nil, fmt.Errorf(
				"sqlite migration version %06d must have one up and one down file",
				version,
			)
		}
		files = append(files, filepath.ToSlash(up), filepath.ToSlash(down))
	}
	return &Inventory{Files: files}, nil
}
