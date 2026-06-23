package types

// KnowledgeProcessOverrides stores per-upload parse config overrides in knowledge metadata.
type KnowledgeProcessOverrides struct {
	ParserEngineRules        []ParserEngineRule        `json:"parser_engine_rules,omitempty"`
	ChunkingConfig           *ChunkingConfig           `json:"chunking_config,omitempty"`
	EnableMultimodel         *bool                     `json:"enable_multimodel,omitempty"`
	VLMConfig                *VLMConfig                `json:"vlm_config,omitempty"`
	ASRConfig                *ASRConfig                `json:"asr_config,omitempty"`
	QuestionGenerationConfig *QuestionGenerationConfig `json:"question_generation_config,omitempty"`
	GraphEnabled             *bool                     `json:"graph_enabled,omitempty"`
	ExtractConfig            *ExtractConfig            `json:"extract_config,omitempty"`
}

// EffectiveProcessConfig is the merged view used by the parse pipeline.
type EffectiveProcessConfig struct {
	ChunkingConfig           ChunkingConfig
	EnableMultimodel         bool
	VLMConfig                VLMConfig
	ASRConfig                ASRConfig
	QuestionGenerationConfig QuestionGenerationConfig
	GraphEnabled             bool
	ExtractConfig            ExtractConfig
}
