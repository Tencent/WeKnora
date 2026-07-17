package types

import "testing"

func TestGetWebSearchProviderTypesIncludesZhipuConfig(t *testing.T) {
	var zhipu *WebSearchProviderTypeInfo
	providerTypes := GetWebSearchProviderTypes()
	for i := range providerTypes {
		if providerTypes[i].ID == string(WebSearchProviderTypeZhipu) {
			zhipu = &providerTypes[i]
			break
		}
	}
	if zhipu == nil {
		t.Fatal("Zhipu provider metadata is missing")
	}
	if !zhipu.RequiresAPIKey || !zhipu.SupportsProxy {
		t.Fatalf("unexpected Zhipu capability metadata: %+v", zhipu)
	}
	if len(zhipu.ConfigFields) != 2 {
		t.Fatalf("len(ConfigFields) = %d, want 2", len(zhipu.ConfigFields))
	}
	if zhipu.ConfigFields[0].Key != "search_engine" || zhipu.ConfigFields[0].Default != "search_std" {
		t.Fatalf("unexpected search engine config metadata: %+v", zhipu.ConfigFields[0])
	}
	if len(zhipu.ConfigFields[0].Options) != 4 {
		t.Fatalf("len(search engine options) = %d, want 4", len(zhipu.ConfigFields[0].Options))
	}
	if zhipu.ConfigFields[1].Key != "content_size" || zhipu.ConfigFields[1].Default != "medium" {
		t.Fatalf("unexpected content size config metadata: %+v", zhipu.ConfigFields[1])
	}
}
