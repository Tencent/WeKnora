package semantic

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNewDeployerCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDeployer(filepath.Join(dir, "model"), "")
	if err != nil {
		t.Fatalf("NewDeployer: %v", err)
	}
	if d.modelDir != filepath.Join(dir, "model") {
		t.Errorf("modelDir mismatch: %s", d.modelDir)
	}
	// auto/ subdirectory should be created
	if _, err := os.Stat(filepath.Join(dir, "model", "auto")); os.IsNotExist(err) {
		t.Errorf("auto/ directory not created")
	}
	// default datasources path
	if d.datasourcesFile != filepath.Join(dir, "datasources.yaml") {
		t.Errorf("datasourcesFile default wrong: %s", d.datasourcesFile)
	}
	// empty datasources.yaml seeded
	if _, err := os.Stat(d.datasourcesFile); os.IsNotExist(err) {
		t.Errorf("empty datasources.yaml not seeded")
	}
}

func TestNewDeployerEmptyDir(t *testing.T) {
	if _, err := NewDeployer("", "/tmp/test-ds.yaml"); err == nil {
		t.Errorf("empty model dir should error")
	}
}

func TestPublishUnpublishModel(t *testing.T) {
	dir := t.TempDir()
	d, _ := NewDeployer(filepath.Join(dir, "model"), "")

	yaml := "cubes:\n  - name: orders\n    sql: SELECT 1\n"
	if err := d.PublishModel("orders", yaml); err != nil {
		t.Fatalf("PublishModel: %v", err)
	}
	path := filepath.Join(dir, "model", "auto", "orders.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != yaml {
		t.Errorf("file content mismatch")
	}

	// idempotent unpublish (non-existent file is OK)
	if err := d.UnpublishModel("orders"); err != nil {
		t.Errorf("UnpublishModel: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed after unpublish")
	}
	// unpublish again is safe (already gone)
	if err := d.UnpublishModel("orders"); err != nil {
		t.Errorf("idempotent unpublish failed: %v", err)
	}
}

func TestPublishModelRejectsBadSlug(t *testing.T) {
	dir := t.TempDir()
	d, _ := NewDeployer(filepath.Join(dir, "model"), "")
	for _, name := range []string{"Bad-Name", "123start", "", "has space"} {
		if err := d.PublishModel(name, "yaml"); err == nil {
			t.Errorf("PublishModel(%q) should reject invalid slug", name)
		}
	}
}

func TestSyncDatasources(t *testing.T) {
	dir := t.TempDir()
	dsFile := filepath.Join(dir, "datasources.yaml")
	d, _ := NewDeployer(filepath.Join(dir, "model"), dsFile)

	entries := []DatasourceEntry{
		{ID: "primary_db", Title: "Primary", Type: "postgres", Config: &ConnectionConfig{
			Host: "10.0.0.1", Port: 5432, Database: "app", Username: "reader", Password: "secret",
			Extra: map[string]interface{}{"sslmode": "require"},
		}},
		{ID: "analytics", Title: "Analytics", Type: "clickhouse", Config: &ConnectionConfig{
			Host: "10.0.0.2", Port: 8123, Database: "events", Username: "analyst",
		}},
	}
	if err := d.SyncDatasources(entries); err != nil {
		t.Fatalf("SyncDatasources: %v", err)
	}
	data, _ := os.ReadFile(dsFile)
	var doc datasourcesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Datasources) != 2 {
		t.Fatalf("expected 2 datasources, got %d", len(doc.Datasources))
	}
	if doc.Datasources[0]["id"] != "primary_db" || doc.Datasources[0]["type"] != "postgres" {
		t.Errorf("first entry wrong: %+v", doc.Datasources[0])
	}
	if doc.Datasources[0]["password"] != "secret" {
		t.Errorf("password not written to datasources.yaml")
	}
	if doc.Datasources[0]["sslmode"] != "require" {
		t.Errorf("extra param not passed through")
	}

	// empty list produces empty datasources
	if err := d.SyncDatasources(nil); err != nil {
		t.Fatalf("SyncDatasources(nil): %v", err)
	}
	data2, _ := os.ReadFile(dsFile)
	var doc2 datasourcesDoc
	_ = yaml.Unmarshal(data2, &doc2)
	if len(doc2.Datasources) != 0 {
		t.Errorf("expected 0 datasources after clear, got %d", len(doc2.Datasources))
	}
}

func TestSyncDatasourcesRejectsBadExtraKey(t *testing.T) {
	dir := t.TempDir()
	d, _ := NewDeployer(filepath.Join(dir, "model"), filepath.Join(dir, "ds.yaml"))
	entries := []DatasourceEntry{
		{ID: "db1", Type: "mysql", Config: &ConnectionConfig{
			Extra: map[string]interface{}{"bad key!": "value"},
		}},
	}
	if err := d.SyncDatasources(entries); err == nil {
		t.Errorf("should reject invalid extra key name")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := atomicWrite(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello" {
		t.Errorf("content mismatch: %s", string(data))
	}
	// overwrite
	if err := atomicWrite(path, []byte("world"), 0o644); err != nil {
		t.Fatalf("atomicWrite overwrite: %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "world" {
		t.Errorf("overwrite content mismatch: %s", string(data))
	}
	// no temp files left behind
	matches, _ := filepath.Glob(filepath.Join(dir, ".semantic-*"))
	if len(matches) > 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}
