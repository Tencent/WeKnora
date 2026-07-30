package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateProviderParametersZhipu(t *testing.T) {
	valid := types.WebSearchProviderParameters{
		APIKey: "key",
		ExtraConfig: map[string]string{
			"search_engine": "search_pro",
			"content_size":  "high",
		},
	}
	if err := validateProviderParameters(types.WebSearchProviderTypeZhipu, valid); err != nil {
		t.Fatalf("valid Zhipu parameters rejected: %v", err)
	}

	invalid := valid
	invalid.ExtraConfig = map[string]string{"search_engine": "unsupported"}
	if err := validateProviderParameters(types.WebSearchProviderTypeZhipu, invalid); err == nil {
		t.Fatal("invalid Zhipu search engine was accepted")
	}
}

func TestIsValidProviderTypeIncludesZhipu(t *testing.T) {
	if !isValidProviderType(types.WebSearchProviderTypeZhipu) {
		t.Fatal("Zhipu provider type is not accepted")
	}
}
