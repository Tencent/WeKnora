package mysql

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// mockDB implements a minimal sql.DB-like interface for testing
type mockDB struct {
	tables map[string]bool
}

func (m *mockDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	// This is a simplified mock for testing
	return nil, nil
}

func (m *mockDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}

func TestCreateTableSQLTemplate(t *testing.T) {
	// Test that the table template contains expected elements
	expectedElements := []string{
		"CREATE TABLE IF NOT EXISTS %s",
		"VARCHAR(64) NOT NULL",
		"PRIMARY KEY (id)",
		"INDEX idx_chunk",
		"INDEX idx_kb",
		"INDEX idx_kid",
		"INDEX idx_src",
		"INDEX idx_tag",
		"INDEX idx_enabled",
		"FULLTEXT INDEX idx_content_ft",
		"embedding JSON NOT NULL",
		"ENGINE=InnoDB",
		"CHARSET=utf8mb4",
	}

	for _, elem := range expectedElements {
		if !containsNormalized(createTableTpl, elem) {
			t.Errorf("Table template missing expected element: %s", elem)
		}
	}
}

func TestGetTableName(t *testing.T) {
	repo := &mysqlRepository{
		tablePrefix: defaultTablePrefix,
	}

	tests := []struct {
		dim  int
		want string
	}{
		{768, "weknora_embeddings_768"},
		{1024, "weknora_embeddings_1024"},
		{1536, "weknora_embeddings_1536"},
		{384, "weknora_embeddings_384"},
		{256, "weknora_embeddings_256"},
		{0, "weknora_embeddings_0"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := repo.getTableName(tt.dim)
			if got != tt.want {
				t.Errorf("getTableName(%d) = %v, want %v", tt.dim, got, tt.want)
			}
		})
	}
}

func TestGetTableNameWithCustomPrefix(t *testing.T) {
	repo := &mysqlRepository{
		tablePrefix: "custom_prefix_",
	}

	got := repo.getTableName(768)
	want := "custom_prefix_768"
	if got != want {
		t.Errorf("getTableName(768) with custom prefix = %v, want %v", got, want)
	}
}

func TestDefaultTablePrefix(t *testing.T) {
	if defaultTablePrefix != "weknora_embeddings_" {
		t.Errorf("defaultTablePrefix = %v, want weknora_embeddings_", defaultTablePrefix)
	}
}

func TestTableNameUniqueness(t *testing.T) {
	repo := &mysqlRepository{
		tablePrefix: defaultTablePrefix,
	}

	// Different dimensions should produce different table names
	names := make(map[string]bool)
	dims := []int{128, 256, 384, 512, 768, 1024, 1536, 2048}

	for _, dim := range dims {
		name := repo.getTableName(dim)
		if names[name] {
			t.Errorf("Duplicate table name for dimension %d: %s", dim, name)
		}
		names[name] = true
	}
}

func containsNormalized(s, substr string) bool {
	return strings.Contains(strings.Join(strings.Fields(s), " "), strings.Join(strings.Fields(substr), " "))
}
