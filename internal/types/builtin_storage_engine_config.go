package types

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// builtinStorageEngineFile mirrors the top-level YAML structure.
// Top level is a single object (not a list) — see design 7.1.
type builtinStorageEngineFile struct {
	StorageEngine *StorageEngineConfig `yaml:"storage_engine"`
}

// LoadBuiltinStorageEngineConfig reads config/builtin_storage_engine.yaml
// (or the path pointed to by BUILTIN_STORAGE_ENGINE_CONFIG), expands ${ENV}
// placeholders, and stores the result into the singleton.
//
// Behaviour (matches LoadBuiltinModelsConfig):
//   - file missing / mount point is a directory: prints "not present"; returns nil
//   - YAML parse error: prints warning; returns nil
//   - parsed but empty (no default_provider and no providers): prints "no entries"; returns nil
//   - parsed with content: stores into singleton; logs which providers were populated
func LoadBuiltinStorageEngineConfig(configDir string) error {
	path := os.Getenv("BUILTIN_STORAGE_ENGINE_CONFIG")
	source := "BUILTIN_STORAGE_ENGINE_CONFIG env"
	if path == "" {
		path = filepath.Join(configDir, "builtin_storage_engine.yaml")
		source = "configDir"
	}

	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		fmt.Printf("Built-in storage engine config not present at %s; skipping.\n", path)
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Warning: read built-in storage engine config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	expanded := expandEnv(string(raw))

	var file builtinStorageEngineFile
	if err := yaml.Unmarshal([]byte(expanded), &file); err != nil {
		fmt.Printf("Warning: parse built-in storage engine config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	if file.StorageEngine == nil || isStorageEngineFileEmpty(file.StorageEngine) {
		fmt.Printf("Built-in storage engine config %s contains no entries; skipping.\n", path)
		return nil
	}

	builtinStorageEngine.Store(file.StorageEngine)
	providers := populatedProviders(file.StorageEngine)
	fmt.Printf("Built-in storage engine loaded from %s (source: %s): default=%q, providers=%v\n",
		path, source, file.StorageEngine.DefaultProvider, providers)
	return nil
}

// isStorageEngineFileEmpty returns true when no default_provider and no provider sub-config is present.
func isStorageEngineFileEmpty(c *StorageEngineConfig) bool {
	if c.DefaultProvider != "" {
		return false
	}
	return c.Local == nil && c.MinIO == nil && c.COS == nil && c.TOS == nil &&
		c.S3 == nil && c.OSS == nil && c.KS3 == nil && c.OBS == nil
}

// populatedProviders returns the names of provider sub-configs that are non-nil.
// Used only for startup logging — does NOT touch any secret field values.
func populatedProviders(c *StorageEngineConfig) []string {
	out := []string{}
	if c.Local != nil { out = append(out, "local") }
	if c.MinIO != nil { out = append(out, "minio") }
	if c.COS != nil   { out = append(out, "cos") }
	if c.TOS != nil   { out = append(out, "tos") }
	if c.S3 != nil    { out = append(out, "s3") }
	if c.OSS != nil   { out = append(out, "oss") }
	if c.KS3 != nil   { out = append(out, "ks3") }
	if c.OBS != nil   { out = append(out, "obs") }
	return out
}
