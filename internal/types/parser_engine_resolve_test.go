// internal/types/parser_engine_resolve_test.go
package types

import (
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestResolveMinerUOverrides_TenantWins(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		MinerU: &BuiltinMinerUConfig{Endpoint: "builtin-ep", Model: "builtin-model"},
	})

	tenant := &ParserEngineConfig{MinerUEndpoint: "tenant-ep"}
	got := ResolveMinerUOverrides(tenant)
	if got["mineru_endpoint"] != "tenant-ep" {
		t.Fatalf("tenant should win, got %v", got)
	}
	if _, present := got["mineru_model"]; present {
		t.Fatalf("tenant package wins as a whole; builtin model must NOT leak: %v", got)
	}
}

func TestResolveMinerUOverrides_TenantEmpty_FallbackToBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		MinerU: &BuiltinMinerUConfig{
			Endpoint: "builtin-ep", Model: "vlm-http-client",
			EnableFormula: boolPtr(false), Language: "en",
		},
	})

	got := ResolveMinerUOverrides(&ParserEngineConfig{})
	if got["mineru_endpoint"] != "builtin-ep" {
		t.Errorf("want builtin endpoint, got %v", got)
	}
	if got["mineru_model"] != "vlm-http-client" {
		t.Errorf("want builtin model, got %v", got)
	}
	if got["mineru_enable_formula"] != "false" {
		t.Errorf("want false, got %v", got["mineru_enable_formula"])
	}
	if got["mineru_language"] != "en" {
		t.Errorf("want en, got %v", got["mineru_language"])
	}
}

func TestResolveMinerUOverrides_NoTenantNoBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	if got := ResolveMinerUOverrides(nil); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestResolveMinerUOverrides_BuiltinEndpointEmpty_Skip(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		MinerU: &BuiltinMinerUConfig{Model: "x"}, // 没 endpoint → 视为未配置
	})
	if got := ResolveMinerUOverrides(nil); got != nil {
		t.Fatalf("builtin with no endpoint must not produce overrides, got %v", got)
	}
}

func TestResolveMinerUCloudOverrides_TenantWins(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		MinerUCloud: &BuiltinMinerUCloudConfig{APIKey: "builtin-key", Model: "builtin-m"},
	})
	got := ResolveMinerUCloudOverrides(&ParserEngineConfig{MinerUAPIKey: "tenant-key"})
	if got["mineru_api_key"] != "tenant-key" {
		t.Fatalf("tenant should win, got %v", got)
	}
	if _, ok := got["mineru_cloud_model"]; ok {
		t.Fatalf("builtin model must not leak: %v", got)
	}
}

func TestResolveMinerUCloudOverrides_FallbackToBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		MinerUCloud: &BuiltinMinerUCloudConfig{APIKey: "builtin-key", Model: "vlm"},
	})
	got := ResolveMinerUCloudOverrides(nil)
	if got["mineru_api_key"] != "builtin-key" || got["mineru_cloud_model"] != "vlm" {
		t.Fatalf("want builtin, got %v", got)
	}
}

func TestResolveWeKnoraCloudAppID_CredsWin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		WeKnoraCloud: &BuiltinWeKnoraCloudConfig{AppID: "builtin-app"},
	})
	if got := ResolveWeKnoraCloudAppID("tenant-app"); got != "tenant-app" {
		t.Fatalf("want tenant-app, got %q", got)
	}
}

func TestResolveWeKnoraCloudAppID_FallbackToBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		WeKnoraCloud: &BuiltinWeKnoraCloudConfig{AppID: "builtin-app"},
	})
	if got := ResolveWeKnoraCloudAppID(""); got != "builtin-app" {
		t.Fatalf("want builtin-app, got %q", got)
	}
}

func TestResolveWeKnoraCloudAppID_NeitherSet(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	if got := ResolveWeKnoraCloudAppID(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestResolveDocReaderAddr_EnvWins(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{DocReaderAddr: "builtin:1"})
	if got := ResolveDocReaderAddr("env:9"); got != "env:9" {
		t.Fatalf("env should win, got %q", got)
	}
}

func TestResolveDocReaderAddr_FallbackToBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{DocReaderAddr: "builtin:1"})
	if got := ResolveDocReaderAddr(""); got != "builtin:1" {
		t.Fatalf("want builtin:1, got %q", got)
	}
}

func TestResolveDocReaderAddr_Empty(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	if got := ResolveDocReaderAddr(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestMergeParserEngineOverrides_NilTenantWithBuiltin(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	SetBuiltinParserEngineForTest(&BuiltinParserEngineConfig{
		DocReaderAddr: "doc:1",
		MinerU:        &BuiltinMinerUConfig{Endpoint: "m-ep"},
		MinerUCloud:   &BuiltinMinerUCloudConfig{APIKey: "k"},
		WeKnoraCloud:  &BuiltinWeKnoraCloudConfig{AppID: "wkc"},
	})
	t.Setenv("DOCREADER_ADDR", "") // 确保 env 为空

	got := MergeParserEngineOverrides(nil)
	if got["mineru_endpoint"] != "m-ep" {
		t.Errorf("mineru_endpoint=%v", got)
	}
	if got["mineru_api_key"] != "k" {
		t.Errorf("mineru_api_key=%v", got)
	}
	if got["weknoracloud_app_id"] != "wkc" {
		t.Errorf("weknoracloud_app_id=%v", got)
	}
	if got["docreader_addr"] != "doc:1" {
		t.Errorf("docreader_addr=%v", got)
	}
}

func TestMergeParserEngineOverrides_EmptyEverywhere(t *testing.T) {
	ResetBuiltinParserEngineForTest()
	t.Cleanup(ResetBuiltinParserEngineForTest)
	t.Setenv("DOCREADER_ADDR", "")
	if got := MergeParserEngineOverrides(nil); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
