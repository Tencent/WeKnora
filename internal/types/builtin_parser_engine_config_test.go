// internal/types/builtin_parser_engine_config_test.go
package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuiltinParserEngine_FileMissing(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	dir := t.TempDir() // 不写文件

	if err := LoadBuiltinParserEngineConfig(dir); err != nil {
		t.Fatalf("want nil err on missing file, got %v", err)
	}
	if GetBuiltinParserEngine() != nil {
		t.Fatalf("singleton should remain nil")
	}
}

func TestLoadBuiltinParserEngine_InvalidYAML(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "builtin_parser_engine.yaml"),
		[]byte("not: : valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinParserEngineConfig(dir); err != nil {
		t.Fatalf("want nil err on invalid yaml, got %v", err)
	}
	if GetBuiltinParserEngine() != nil {
		t.Fatalf("singleton should remain nil")
	}
}

func TestLoadBuiltinParserEngine_EmptyConfig(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "builtin_parser_engine.yaml"),
		[]byte("parser_engine: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinParserEngineConfig(dir); err != nil {
		t.Fatalf("load err: %v", err)
	}
	if GetBuiltinParserEngine() != nil {
		t.Fatalf("empty cfg should NOT populate singleton")
	}
}

func TestLoadBuiltinParserEngine_EnvInterpolation(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	t.Setenv("TEST_MINERU_ENDPOINT", "http://mineru-from-env:8080")
	t.Setenv("TEST_MINERU_CLOUD_KEY", "key-from-env")

	dir := t.TempDir()
	yaml := `
parser_engine:
  docreader_addr: docreader:50051
  mineru:
    endpoint: ${TEST_MINERU_ENDPOINT}
    model: pipeline
    enable_formula: ${TEST_MINERU_FORMULA:-true}
    enable_ocr: ${TEST_MINERU_OCR:-false}
    language: ch
  mineru_cloud:
    api_key: ${TEST_MINERU_CLOUD_KEY}
    model: pipeline
  weknoracloud:
    app_id: ${TEST_WKC_APPID:-builtin-appid}
`
	if err := os.WriteFile(filepath.Join(dir, "builtin_parser_engine.yaml"),
		[]byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinParserEngineConfig(dir); err != nil {
		t.Fatalf("load err: %v", err)
	}
	got := GetBuiltinParserEngine()
	if got == nil {
		t.Fatal("singleton not loaded")
	}
	if got.DocReaderAddr != "docreader:50051" {
		t.Errorf("DocReaderAddr=%q", got.DocReaderAddr)
	}
	if got.MinerU == nil || got.MinerU.Endpoint != "http://mineru-from-env:8080" {
		t.Errorf("MinerU.Endpoint not interpolated: %+v", got.MinerU)
	}
	if got.MinerU.EnableFormula == nil || *got.MinerU.EnableFormula != true {
		t.Errorf("MinerU.EnableFormula want true (default), got %+v", got.MinerU.EnableFormula)
	}
	if got.MinerU.EnableOCR == nil || *got.MinerU.EnableOCR != false {
		t.Errorf("MinerU.EnableOCR want false (default), got %+v", got.MinerU.EnableOCR)
	}
	if got.MinerUCloud == nil || got.MinerUCloud.APIKey != "key-from-env" {
		t.Errorf("MinerUCloud.APIKey not interpolated: %+v", got.MinerUCloud)
	}
	if got.WeKnoraCloud == nil || got.WeKnoraCloud.AppID != "builtin-appid" {
		t.Errorf("WeKnoraCloud.AppID want builtin-appid (default), got %+v", got.WeKnoraCloud)
	}
}

func TestLoadBuiltinParserEngine_EnvOverridePath(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	dir := t.TempDir()
	customPath := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(customPath,
		[]byte("parser_engine:\n  docreader_addr: env-addr:1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILTIN_PARSER_ENGINE_CONFIG", customPath)

	if err := LoadBuiltinParserEngineConfig("/nonexistent"); err != nil {
		t.Fatalf("load err: %v", err)
	}
	got := GetBuiltinParserEngine()
	if got == nil || got.DocReaderAddr != "env-addr:1234" {
		t.Fatalf("env override path not honored, got %+v", got)
	}
}

func TestLoadBuiltinParserEngine_AtomicReplace(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	dir := t.TempDir()
	p := filepath.Join(dir, "builtin_parser_engine.yaml")

	if err := os.WriteFile(p, []byte("parser_engine:\n  docreader_addr: a:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = LoadBuiltinParserEngineConfig(dir)
	if GetBuiltinParserEngine().DocReaderAddr != "a:1" {
		t.Fatal("first load failed")
	}

	if err := os.WriteFile(p, []byte("parser_engine:\n  docreader_addr: b:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = LoadBuiltinParserEngineConfig(dir)
	if GetBuiltinParserEngine().DocReaderAddr != "b:2" {
		t.Fatal("second load did not replace")
	}
}
