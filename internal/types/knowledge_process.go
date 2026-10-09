package types

// KnowledgeProcessOverrides stores per-upload parse config overrides in knowledge metadata.
type KnowledgeProcessOverrides struct {
	// SummaryEnabled defaults to true when omitted for backward compatibility.
	SummaryEnabled           *bool                     `json:"summary_enabled,omitempty"`
	ParserEngineRules        []ParserEngineRule        `json:"parser_engine_rules,omitempty"`
	ChunkingConfig           *ChunkingConfig           `json:"chunking_config,omitempty"`
	EnableMultimodel         *bool                     `json:"enable_multimodel,omitempty"`
	VLMConfig                *VLMConfig                `json:"vlm_config,omitempty"`
	ASRConfig                *ASRConfig                `json:"asr_config,omitempty"`
	QuestionGenerationConfig *QuestionGenerationConfig `json:"question_generation_config,omitempty"`
	GraphEnabled             *bool                     `json:"graph_enabled,omitempty"`
	// ImageAttrsEnabled overrides the knowledge base's attribute-observed image
	// pipeline switch for this one document. nil keeps the knowledge base's
	// setting.
	ImageAttrsEnabled *bool `json:"image_attrs_enabled,omitempty"`
	// ImagePipelineID overrides which image pipeline runs for this one
	// document. nil keeps the knowledge base's resolved pipeline. When set,
	// ImagePipelineParams travels with it: the params of one pipeline are
	// meaningless to another, so the pair is replaced as a unit.
	ImagePipelineID *ImagePipelineID `json:"image_pipeline,omitempty"`
	// ImagePipelineParams are the overridden pipeline's private tunables. They
	// are only applied together with ImagePipelineID.
	ImagePipelineParams map[string]any `json:"image_pipeline_params,omitempty"`
	// ImageActions overrides rows of the attribute->work table for this document
	// (merged per action key on top of the knowledge base's table).
	// nil keeps the knowledge base's setting.
	ImageActions  *ImageActionsConfig `json:"image_actions,omitempty"`
	ExtractConfig *ExtractConfig      `json:"extract_config,omitempty"`
	// ParserEngineOverrides passes key-value configuration to docreader parsers
	// (e.g. pdf_force_scanned=true). Merged with workspace-level overrides in the
	// parse pipeline; per-upload values take priority on conflict.
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
}

// EffectiveProcessConfig is the merged view used by the parse pipeline.
type EffectiveProcessConfig struct {
	SummaryEnabled           bool
	ChunkingConfig           ChunkingConfig
	EnableMultimodel         bool
	VLMConfig                VLMConfig
	ASRConfig                ASRConfig
	QuestionGenerationConfig QuestionGenerationConfig
	GraphEnabled             bool
	ImageAttrsEnabled        bool
	ImageActions             ImageActionsConfig
	ImagePipelineID          ImagePipelineID
	ImagePipelineParams      map[string]any
	ExtractConfig            ExtractConfig
}
