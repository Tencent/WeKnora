package migrations

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

var migrationFilePattern = regexp.MustCompile(`^(\d{6})_[a-z0-9_]+\.(up|down)\.sql$`)

func TestEmbeddedMigrationsAreComplete(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	versions := make(map[int]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			t.Errorf("migration has invalid filename: %s", entry.Name())
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			t.Errorf("migration has invalid version: %s", entry.Name())
			continue
		}
		if versions[version] == nil {
			versions[version] = make(map[string]bool)
		}
		versions[version][match[2]] = true
	}

	if len(versions) == 0 {
		t.Fatal("no embedded migrations found")
	}

	orderedVersions := make([]int, 0, len(versions))
	for version := range versions {
		orderedVersions = append(orderedVersions, version)
	}
	sort.Ints(orderedVersions)
	for index, version := range orderedVersions {
		expected := index + 1
		if version != expected {
			t.Errorf("migration sequence is not contiguous: found %06d, want %06d", version, expected)
		}
		for _, direction := range []string{"up", "down"} {
			if !versions[version][direction] {
				t.Errorf("migration %06d is missing %s file", version, direction)
			}
		}
	}

	for _, file := range []string{
		"000008_processing_observability.up.sql",
		"000008_processing_observability.down.sql",
		"000009_transcription_source.up.sql",
		"000009_transcription_source.down.sql",
		"000010_content_pipeline_metadata.up.sql",
		"000010_content_pipeline_metadata.down.sql",
		"000011_knowledge_audit_status.up.sql",
		"000011_knowledge_audit_status.down.sql",
	} {
		content, err := fs.ReadFile(FS, file)
		if err != nil {
			t.Errorf("read required migration %s: %v", file, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("required migration is empty: %s", file)
		}
	}

	if !versions[8]["up"] || !versions[8]["down"] {
		t.Error("processing observability migration 000008 must include up and down files")
	}
}
