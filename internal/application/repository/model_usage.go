package repository

import (
	"gorm.io/gorm"
)

// scopeKnowledgeBasesByModelID filters knowledge_bases rows that reference
// modelID in any model-binding field.
func scopeKnowledgeBasesByModelID(db *gorm.DB, modelID string) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres":
		return db.Where(
			"embedding_model_id = ? OR summary_model_id = ? OR "+
				"image_processing_config->>'model_id' = ? OR "+
				"vlm_config->>'model_id' = ? OR "+
				"asr_config->>'model_id' = ? OR "+
				"wiki_config->>'synthesis_model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	case "sqlite":
		// SQLite json_extract returns the raw value directly (no wrapping
		// JSON quotes), so json_unquote is neither needed nor available.
		return db.Where(
			"embedding_model_id = ? OR summary_model_id = ? OR "+
				"json_extract(image_processing_config, '$.model_id') = ? OR "+
				"json_extract(vlm_config, '$.model_id') = ? OR "+
				"json_extract(asr_config, '$.model_id') = ? OR "+
				"json_extract(wiki_config, '$.synthesis_model_id') = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	default: // MySQL || fallback
		return db.Where(
			"embedding_model_id = ? OR summary_model_id = ? OR "+
				"json_unquote(json_extract(image_processing_config, '$.model_id')) = ? OR "+
				"json_unquote(json_extract(vlm_config, '$.model_id')) = ? OR "+
				"json_unquote(json_extract(asr_config, '$.model_id')) = ? OR "+
				"json_unquote(json_extract(wiki_config, '$.synthesis_model_id')) = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	}
}

// scopeCustomAgentsByModelID filters custom_agents rows whose config JSON
// references modelID in any model-binding field.
func scopeCustomAgentsByModelID(db *gorm.DB, modelID string) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres":
		return db.Where(
			"config->>'model_id' = ? OR config->>'rerank_model_id' = ? OR "+
				"config->>'vlm_model_id' = ? OR config->>'asr_model_id' = ? OR "+
				"config->>'query_understand_model_id' = ? OR "+
				"config->'question_suggestions'->'follow_ups'->>'model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	case "sqlite":
		return db.Where(
			"json_extract(config, '$.model_id') = ? OR "+
				"json_extract(config, '$.rerank_model_id') = ? OR "+
				"json_extract(config, '$.vlm_model_id') = ? OR "+
				"json_extract(config, '$.asr_model_id') = ? OR "+
				"json_extract(config, '$.query_understand_model_id') = ?",
			modelID, modelID, modelID, modelID, modelID,
		)
	default: // MySQL || fallback
		return db.Where(
			"json_unquote(json_extract(config, '$.model_id')) = ? OR "+
				"json_unquote(json_extract(config, '$.rerank_model_id')) = ? OR "+
				"json_unquote(json_extract(config, '$.vlm_model_id')) = ? OR "+
				"json_unquote(json_extract(config, '$.asr_model_id')) = ? OR "+
				"json_unquote(json_extract(config, '$.query_understand_model_id')) = ?",
			modelID, modelID, modelID, modelID, modelID,
		)
	}
}
