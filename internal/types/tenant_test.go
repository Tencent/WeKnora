package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetrieverEngineMappingIncludesTencentVectorDBHybridCapabilities(t *testing.T) {
	mapping := GetRetrieverEngineMapping()

	assert.Contains(t, mapping["tencent_vectordb"], RetrieverEngineParams{
		RetrieverType:       KeywordsRetrieverType,
		RetrieverEngineType: TencentVectorDBRetrieverEngineType,
	})
	assert.Contains(t, mapping["tencent_vectordb"], RetrieverEngineParams{
		RetrieverType:       VectorRetrieverType,
		RetrieverEngineType: TencentVectorDBRetrieverEngineType,
	})
}

func TestResolveMinerUParseMethod(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name      string
		method    string
		legacyOCR *bool
		want      string
	}{
		{name: "new default", want: MinerUParseMethodAuto},
		{name: "legacy enabled", legacyOCR: &trueValue, want: MinerUParseMethodAuto},
		{name: "legacy disabled", legacyOCR: &falseValue, want: MinerUParseMethodText},
		{name: "explicit auto overrides legacy", method: "auto", legacyOCR: &falseValue, want: MinerUParseMethodAuto},
		{name: "explicit OCR is normalized", method: " OCR ", want: MinerUParseMethodOCR},
		{name: "explicit text overrides legacy", method: "txt", legacyOCR: &trueValue, want: MinerUParseMethodText},
		{name: "invalid method falls back safely", method: "invalid", want: MinerUParseMethodAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveMinerUParseMethod(tt.method, tt.legacyOCR))
		})
	}
}

func TestParserEngineConfigToOverridesMapResolvesMinerUParseMethod(t *testing.T) {
	falseValue := false
	explicit := (&ParserEngineConfig{
		MinerUParseMethod: MinerUParseMethodOCR,
		MinerUEnableOCR:   &falseValue,
	}).ToOverridesMap()
	assert.Equal(t, MinerUParseMethodOCR, explicit["mineru_parse_method"])

	legacy := (&ParserEngineConfig{MinerUEnableOCR: &falseValue}).ToOverridesMap()
	assert.Equal(t, MinerUParseMethodText, legacy["mineru_parse_method"])
}
