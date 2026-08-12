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

func TestValidateProviderParametersExa(t *testing.T) {
	valid := types.WebSearchProviderParameters{APIKey: "exa-test"}
	if err := validateProviderParameters(types.WebSearchProviderTypeExa, valid); err != nil {
		t.Fatalf("valid Exa parameters rejected: %v", err)
	}
	if !isValidProviderType(types.WebSearchProviderTypeExa) {
		t.Fatal("Exa provider type is not accepted")
	}

	if err := validateProviderParameters(types.WebSearchProviderTypeExa, types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("missing Exa API key was accepted")
	}
}

func TestValidateProviderParametersMetaso(t *testing.T) {
	valid := types.WebSearchProviderParameters{
		APIKey:      "mk-test",
		ExtraConfig: map[string]string{"scope": "webpage"},
	}
	if err := validateProviderParameters(types.WebSearchProviderTypeMetaso, valid); err != nil {
		t.Fatalf("valid Metaso parameters rejected: %v", err)
	}
	invalid := valid
	invalid.ExtraConfig = map[string]string{"scope": "unsupported"}
	if err := validateProviderParameters(types.WebSearchProviderTypeMetaso, invalid); err == nil {
		t.Fatal("invalid Metaso scope was accepted")
	}
	if !isValidProviderType(types.WebSearchProviderTypeMetaso) {
		t.Fatal("Metaso provider type is not accepted")
	}
}
