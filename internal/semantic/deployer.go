package semantic

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Deployer writes the declarative model files that Cube (dev mode) hot-reloads:
//
//	<CUBE_MODEL_DIR>/auto/<model>.yaml   — one file per published model
//	<CUBE_DATASOURCES_FILE>              — connection configs for driverFactory
//
// All writes are atomic (temp file + rename in the same directory) so Cube
// never observes a half-written file. The hand-written JS/YAML models outside
// auto/ are never touched.
type Deployer struct {
	modelDir        string
	datasourcesFile string
}

// NewDeployer validates the target directories and returns a deployer.
func NewDeployer(modelDir, datasourcesFile string) (*Deployer, error) {
	if modelDir == "" {
		return nil, fmt.Errorf("CUBE_MODEL_DIR is not set")
	}
	if datasourcesFile == "" {
		datasourcesFile = filepath.Join(filepath.Dir(modelDir), "datasources.yaml")
	}
	if err := os.MkdirAll(filepath.Join(modelDir, "auto"), 0o755); err != nil {
		return nil, fmt.Errorf("model directory not writable %s: %w", modelDir, err)
	}
	d := &Deployer{modelDir: modelDir, datasourcesFile: datasourcesFile}
	if err := d.ensureDatasourcesFile(); err != nil {
		return nil, err
	}
	return d, nil
}

// ensureDatasourcesFile seeds an empty datasources.yaml so the Cube
// container's bind mount always finds a valid file on first boot.
func (d *Deployer) ensureDatasourcesFile() error {
	if _, err := os.Stat(d.datasourcesFile); err == nil {
		return nil
	}
	return d.SyncDatasources(nil)
}

// autoDir is the subdirectory owned by this module.
func (d *Deployer) autoDir() string { return filepath.Join(d.modelDir, "auto") }

// PublishModel writes one model file atomically.
func (d *Deployer) PublishModel(name, yamlText string) error {
	if !ValidSlug(name) {
		return fmt.Errorf("model name %q is invalid", name)
	}
	if err := os.MkdirAll(d.autoDir(), 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(d.autoDir(), name+".yaml"), []byte(yamlText), 0o644)
}

// UnpublishModel removes one model file. A missing file is not an error —
// unpublishing an already-removed model is idempotent.
func (d *Deployer) UnpublishModel(name string) error {
	path := filepath.Join(d.autoDir(), name+".yaml")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DatasourceEntry is one connection serialized into datasources.yaml.
type DatasourceEntry struct {
	ID     string
	Title  string
	Type   string
	Config *ConnectionConfig
}

// datasourcesDoc mirrors deploy/cube/cube.js loadDatasources().
type datasourcesDoc struct {
	Datasources []map[string]interface{} `yaml:"datasources"`
}

// extraKeyPattern guards passthrough keys before they reach YAML.
var extraKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// SyncDatasources rewrites datasources.yaml from the full connection list.
// Called on every connection mutation; driverFactory re-reads the file on
// mtime change, so credential updates apply without any restart.
func (d *Deployer) SyncDatasources(entries []DatasourceEntry) error {
	doc := datasourcesDoc{Datasources: make([]map[string]interface{}, 0, len(entries))}
	for _, e := range entries {
		if !ValidSlug(e.ID) {
			return fmt.Errorf("connection slug %q is invalid", e.ID)
		}
		entry := map[string]interface{}{
			"id":    e.ID,
			"title": e.Title,
			"type":  e.Type,
		}
		c := e.Config
		if c != nil {
			if c.Host != "" {
				entry["host"] = c.Host
			}
			if c.Port != 0 {
				entry["port"] = c.Port
			}
			if c.Database != "" {
				entry["database"] = c.Database
			}
			if c.Username != "" {
				entry["user"] = c.Username
			}
			if c.Password != "" {
				entry["password"] = c.Password
			}
			for k, v := range c.Extra {
				if !extraKeyPattern.MatchString(k) {
					return fmt.Errorf("extra parameter name %q is invalid", k)
				}
				if _, taken := entry[k]; taken {
					continue // fixed fields win over passthrough
				}
				entry[k] = v
			}
		}
		doc.Datasources = append(doc.Datasources, entry)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return atomicWrite(d.datasourcesFile, out, 0o600) // contains credentials
}

// atomicWrite writes via temp file + rename in the same directory.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".semantic-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file (%s): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName) // no-op after successful rename
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
