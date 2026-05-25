// internal/types/builtin_parser_engine_config.go
package types

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// BuiltinParserEngineConfig is the top-level shape of config/builtin_parser_engine.yaml.
// String fields use ${VAR}; bool fields MUST use ${VAR:-default} (expandEnv-handled).
type BuiltinParserEngineConfig struct {
	// DefaultEngine declares which engine (builtin/simple/weknoracloud/mineru/mineru_cloud)
	// the frontend should pre-select as the default for each supported file type.
	// Tenant-level KB parser_engine_rules still win when present.
	DefaultEngine string                     `yaml:"default_engine,omitempty"`
	DocReaderAddr string                     `yaml:"docreader_addr,omitempty"`
	MinerU        *BuiltinMinerUConfig       `yaml:"mineru,omitempty"`
	MinerUCloud   *BuiltinMinerUCloudConfig  `yaml:"mineru_cloud,omitempty"`
	WeKnoraCloud  *BuiltinWeKnoraCloudConfig `yaml:"weknoracloud,omitempty"`
}

type BuiltinMinerUConfig struct {
	Endpoint      string `yaml:"endpoint,omitempty"`
	Model         string `yaml:"model,omitempty"`
	VLMServerURL  string `yaml:"vlm_server_url,omitempty"`
	EnableFormula *bool  `yaml:"enable_formula,omitempty"`
	EnableTable   *bool  `yaml:"enable_table,omitempty"`
	EnableOCR     *bool  `yaml:"enable_ocr,omitempty"`
	Language      string `yaml:"language,omitempty"`
}

type BuiltinMinerUCloudConfig struct {
	APIKey        string `yaml:"api_key,omitempty"`
	Model         string `yaml:"model,omitempty"`
	EnableFormula *bool  `yaml:"enable_formula,omitempty"`
	EnableTable   *bool  `yaml:"enable_table,omitempty"`
	EnableOCR     *bool  `yaml:"enable_ocr,omitempty"`
	Language      string `yaml:"language,omitempty"`
}

type BuiltinWeKnoraCloudConfig struct {
	AppID string `yaml:"app_id,omitempty"`
}

// builtinParserEngineFile mirrors the top-level YAML structure.
type builtinParserEngineFile struct {
	ParserEngine *BuiltinParserEngineConfig `yaml:"parser_engine"`
}

// builtinParserEngine is the process-wide singleton.
var builtinParserEngine atomic.Pointer[BuiltinParserEngineConfig]

// GetBuiltinParserEngine returns the loaded built-in config, or nil if
// LoadBuiltinParserEngineConfig has not been called or found no file.
func GetBuiltinParserEngine() *BuiltinParserEngineConfig {
	return builtinParserEngine.Load()
}

// ResetBuiltinParserEngineForTest clears the singleton. Intended for test t.Cleanup.
func ResetBuiltinParserEngineForTest() {
	builtinParserEngine.Store(nil)
}

// SetBuiltinParserEngineForTest is a test-only writer for cross-package tests.
// Production code MUST use LoadBuiltinParserEngineConfig instead.
func SetBuiltinParserEngineForTest(cfg *BuiltinParserEngineConfig) {
	builtinParserEngine.Store(cfg)
}

// LoadBuiltinParserEngineConfig reads config/builtin_parser_engine.yaml
// (or the path pointed to by BUILTIN_PARSER_ENGINE_CONFIG), expands ${ENV}
// placeholders, and stores the result into the singleton.
//
// Behaviour (matches LoadBuiltinStorageEngineConfig):
//   - file missing / mount point is a directory: prints "not present"; returns nil
//   - YAML parse error: prints warning; returns nil
//   - parsed but empty (no docreader_addr and no providers): prints "no entries"; returns nil
//   - parsed with content: stores into singleton; logs which providers were populated
func LoadBuiltinParserEngineConfig(configDir string) error {
	path := os.Getenv("BUILTIN_PARSER_ENGINE_CONFIG")
	source := "BUILTIN_PARSER_ENGINE_CONFIG env"
	if path == "" {
		path = filepath.Join(configDir, "builtin_parser_engine.yaml")
		source = "configDir"
	}

	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		fmt.Printf("Built-in parser engine config not present at %s; skipping.\n", path)
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Warning: read built-in parser engine config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	expanded := expandEnv(string(raw))

	var file builtinParserEngineFile
	if err := yaml.Unmarshal([]byte(expanded), &file); err != nil {
		fmt.Printf("Warning: parse built-in parser engine config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	if file.ParserEngine == nil || isParserEngineFileEmpty(file.ParserEngine) {
		fmt.Printf("Built-in parser engine config %s contains no entries; skipping.\n", path)
		return nil
	}

	builtinParserEngine.Store(file.ParserEngine)
	providers := populatedParserProviders(file.ParserEngine)
	docreaderAddrState := "unset"
	if file.ParserEngine.DocReaderAddr != "" {
		docreaderAddrState = "set"
	}
	defaultEngine := file.ParserEngine.DefaultEngine
	if defaultEngine == "" {
		defaultEngine = "(unset)"
	}
	fmt.Printf("Built-in parser engine loaded from %s (source: %s): default_engine=%s, providers=%v, docreader_addr=%s\n",
		path, source, defaultEngine, providers, docreaderAddrState)
	return nil
}

// isParserEngineFileEmpty returns true when no default_engine, docreader_addr, and no provider sub-config is present.
func isParserEngineFileEmpty(c *BuiltinParserEngineConfig) bool {
	if c.DefaultEngine != "" || c.DocReaderAddr != "" {
		return false
	}
	return c.MinerU == nil && c.MinerUCloud == nil && c.WeKnoraCloud == nil
}

// populatedParserProviders returns the names of provider sub-configs that are non-nil.
// Used only for startup logging — does NOT touch any secret field values.
func populatedParserProviders(c *BuiltinParserEngineConfig) []string {
	out := []string{}
	if c.MinerU != nil {
		out = append(out, "mineru")
	}
	if c.MinerUCloud != nil {
		out = append(out, "mineru_cloud")
	}
	if c.WeKnoraCloud != nil {
		out = append(out, "weknoracloud")
	}
	return out
}
