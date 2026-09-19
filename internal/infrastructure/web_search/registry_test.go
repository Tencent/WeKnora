package web_search

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type testProvider struct{}

func (testProvider) Name() string { return "test" }
func (testProvider) Search(context.Context, string, int, bool) ([]*types.WebSearchResult, error) {
	return nil, nil
}

func TestRegistryRejectsDuplicateAndExposesExternalMetadata(t *testing.T) {
	registry := NewRegistry()
	factory := func(types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return testProvider{}, nil
	}
	info := types.WebSearchProviderTypeInfo{ID: "ignored", Name: "External Search", Description: "test"}
	if err := registry.RegisterWithInfo("external", factory, info); err != nil {
		t.Fatalf("RegisterWithInfo: %v", err)
	}
	if err := registry.Register("external", factory); err == nil {
		t.Fatal("Register accepted duplicate provider type")
	}
	infos := registry.ListTypeInfos()
	if len(infos) != 1 || infos[0].ID != "external" || infos[0].Name != "External Search" {
		t.Fatalf("unexpected provider infos: %#v", infos)
	}
	if !registry.Has("external") {
		t.Fatal("registry does not report registered external provider")
	}
	if !registry.Unregister("external") || registry.Has("external") {
		t.Fatal("registry failed to unregister external provider")
	}
}
