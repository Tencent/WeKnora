package types

// KnowledgeProcessOverrides stores per-upload parse config overrides in knowledge metadata.
type KnowledgeProcessOverrides struct {
	ParserEngineRules        []ParserEngineRule        `json:"parser_engine_rules,omitempty"`
	ChunkingConfig           *ChunkingConfigOverride   `json:"chunking_config,omitempty"`
	EnableMultimodel         *bool                     `json:"enable_multimodel,omitempty"`
	VLMConfig                *VLMConfig                `json:"vlm_config,omitempty"`
	ASRConfig                *ASRConfig                `json:"asr_config,omitempty"`
	QuestionGenerationConfig *QuestionGenerationConfig `json:"question_generation_config,omitempty"`
	GraphEnabled             *bool                     `json:"graph_enabled,omitempty"`
	ExtractConfig            *ExtractConfig            `json:"extract_config,omitempty"`
	// ParserEngineOverrides passes key-value configuration to docreader parsers
	// (e.g. pdf_force_scanned=true). Merged with workspace-level overrides in the
	// parse pipeline; per-upload values take priority on conflict.
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
}

// ChunkingConfigOverride contains optional per-upload chunking settings.
// ChunkOverlap is a pointer so an omitted value can inherit the knowledge-base
// setting while an explicit zero can disable overlap.
type ChunkingConfigOverride struct {
	ChunkSize                 int                `json:"chunk_size,omitempty"`
	ChunkOverlap              *int               `json:"chunk_overlap,omitempty"`
	Separators                []string           `json:"separators,omitempty"`
	ParserEngineRules         []ParserEngineRule `json:"parser_engine_rules,omitempty"`
	EnableParentChild         bool               `json:"enable_parent_child,omitempty"`
	ParentChunkSize           int                `json:"parent_chunk_size,omitempty"`
	ChildChunkSize            int                `json:"child_chunk_size,omitempty"`
	Strategy                  string             `json:"strategy,omitempty"`
	TokenLimit                int                `json:"token_limit,omitempty"`
	Languages                 []string           `json:"languages,omitempty"`
	TableMetadataInstructions string             `json:"table_metadata_instructions,omitempty"`
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
