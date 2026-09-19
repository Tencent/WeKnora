package plugin

import (
	"context"
	"testing"

	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type builtinTestSearchProvider struct{}

func (builtinTestSearchProvider) Name() string { return "builtin-test" }
func (builtinTestSearchProvider) Search(context.Context, string, int, bool) ([]*types.WebSearchResult, error) {
	return nil, nil
}

func TestRegisterBuiltinWebSearchUsesCommonManager(t *testing.T) {
	registry := infraWebSearch.NewRegistry()
	if err := registry.Register("builtin-test", func(types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return builtinTestSearchProvider{}, nil
	}); err != nil {
		t.Fatalf("register test provider: %v", err)
	}
	manager := NewManager("")
	if err := RegisterBuiltinWebSearch(manager, registry); err != nil {
		t.Fatalf("RegisterBuiltinWebSearch: %v", err)
	}
	info, ok := manager.Get("search.builtin-test")
	if !ok {
		t.Fatal("built-in web search provider was not registered")
	}
	if info.Manifest.ExtensionType != ExtensionSearch {
		t.Fatalf("extension type = %q, want %q", info.Manifest.ExtensionType, ExtensionSearch)
	}
}

func TestRegisterBuiltinParsersUsesCommonManager(t *testing.T) {
	manager := NewManager("")
	if err := RegisterBuiltinParsers(manager); err != nil {
		t.Fatalf("RegisterBuiltinParsers: %v", err)
	}
	info, ok := manager.Get("parser.builtin")
	if !ok {
		t.Fatal("built-in parser was not registered")
	}
	if info.Manifest.ExtensionType != ExtensionParser {
		t.Fatalf("extension type = %q, want %q", info.Manifest.ExtensionType, ExtensionParser)
	}
}
