// internal/handler/parser_engine_allowlist_test.go
package handler

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGetAllowedParserEngines_EmptyEnv_AllAllowed(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "")
	got := getAllowedParserEngines()
	if len(got) != len(supportedParserEngines) {
		t.Fatalf("want all %d engines, got %d: %v", len(supportedParserEngines), len(got), got)
	}
	for _, name := range supportedParserEngines {
		if !got[name] {
			t.Errorf("%q should be allowed when env is empty", name)
		}
	}
}

func TestGetAllowedParserEngines_CommaList(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "builtin,mineru,mineru_cloud")
	got := getAllowedParserEngines()
	want := map[string]bool{"builtin": true, "mineru": true, "mineru_cloud": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGetAllowedParserEngines_MixedSeparatorsAndCase(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", " Builtin;MINERU | mineru_cloud\nweknoracloud\tsimple ")
	got := getAllowedParserEngines()
	if len(got) != 5 {
		t.Fatalf("want 5 entries, got %d: %v", len(got), got)
	}
	for _, name := range []string{"builtin", "mineru", "mineru_cloud", "weknoracloud", "simple"} {
		if !got[name] {
			t.Errorf("%q missing from allow list: %v", name, got)
		}
	}
}

func TestGetAllowedParserEngines_UnknownDropped(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "builtin,unknown_engine,mineru")
	got := getAllowedParserEngines()
	if got["unknown_engine"] {
		t.Fatal("unknown engines must be silently dropped")
	}
	if !got["builtin"] || !got["mineru"] {
		t.Fatalf("known engines missing: %v", got)
	}
}

func TestIsParserEngineAllowed(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "builtin,simple")
	if !isParserEngineAllowed("BUILTIN") {
		t.Error("BUILTIN (case-insensitive) should be allowed")
	}
	if isParserEngineAllowed("mineru") {
		t.Error("mineru not in list, should be disallowed")
	}
	if !isParserEngineAllowed("") {
		t.Error("empty name should default to allowed")
	}
}

func TestFirstAllowedParserEngine(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "mineru,builtin")
	// order follows supportedParserEngines (builtin first), not env order
	if got := firstAllowedParserEngine(); got != "builtin" {
		t.Fatalf("want builtin (preserves supportedParserEngines order), got %q", got)
	}
}

func TestAllowedParserEnginesSorted(t *testing.T) {
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "mineru,builtin,simple")
	got := allowedParserEnginesSorted()
	want := []string{"builtin", "simple", "mineru"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveDefaultParserEngine_NoBuiltin(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	if got := resolveDefaultParserEngine(); got != "" {
		t.Fatalf("want empty when no builtin, got %q", got)
	}
}

func TestResolveDefaultParserEngine_BuiltinSetAndAllowed(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	types.SetBuiltinParserEngineForTest(&types.BuiltinParserEngineConfig{DefaultEngine: "mineru"})
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "")
	if got := resolveDefaultParserEngine(); got != "mineru" {
		t.Fatalf("want mineru, got %q", got)
	}
}

func TestResolveDefaultParserEngine_BlockedByAllowList(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	types.SetBuiltinParserEngineForTest(&types.BuiltinParserEngineConfig{DefaultEngine: "mineru"})
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "builtin,simple")
	if got := resolveDefaultParserEngine(); got != "" {
		t.Fatalf("blocked engine must not be returned, got %q", got)
	}
}

func TestResolveDefaultParserEngine_UnknownName(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	types.SetBuiltinParserEngineForTest(&types.BuiltinParserEngineConfig{DefaultEngine: "no_such_engine"})
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "")
	if got := resolveDefaultParserEngine(); got != "" {
		t.Fatalf("unknown name must yield empty, got %q", got)
	}
}
