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
	// PostProcessImageEnabled overrides the knowledge base's image post-processing
	// switch for this one document. nil keeps the knowledge base's setting.
	PostProcessImageEnabled *bool `json:"post_process_image_enabled,omitempty"`
	// ImageBatchSize overrides the describe-round batch size for this document.
	// nil keeps the knowledge base's setting.
	ImageBatchSize *int `json:"image_batch_size,omitempty"`
	// ImageClassifyDownscaleEnabled overrides the describe-round downscale
	// switch for this document. nil keeps the knowledge base's setting.
	ImageClassifyDownscaleEnabled *bool `json:"image_classify_downscale_enabled,omitempty"`
	// ImageClassPolicies overrides per-class rows of the class->work table for
	// this document (merged per class on top of the knowledge base's table).
	// nil keeps the knowledge base's setting.
	ImageClassPolicies map[string]ImageClassPolicy `json:"image_class_policies,omitempty"`
	ExtractConfig      *ExtractConfig              `json:"extract_config,omitempty"`
	// ParserEngineOverrides passes key-value configuration to docreader parsers
	// (e.g. pdf_force_scanned=true). Merged with workspace-level overrides in the
	// parse pipeline; per-upload values take priority on conflict.
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
}

// EffectiveProcessConfig is the merged view used by the parse pipeline.
type EffectiveProcessConfig struct {
	SummaryEnabled                bool
	ChunkingConfig                ChunkingConfig
	EnableMultimodel              bool
	VLMConfig                     VLMConfig
	ASRConfig                     ASRConfig
	QuestionGenerationConfig      QuestionGenerationConfig
	GraphEnabled                  bool
	PostProcessImageEnabled       bool
	ImageBatchSize                int
	ImageClassifyDownscaleEnabled bool
	ImageClassPolicies            map[string]ImageClassPolicy
	ExtractConfig                 ExtractConfig
}
