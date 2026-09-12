package repository

import (
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

func knowledgeBaseModelUsageBindings(kb *types.KnowledgeBase, modelID string) []types.ModelUsageBinding {
	bindings := make([]types.ModelUsageBinding, 0, 6)
	if kb.EmbeddingModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingEmbeddingModel)
	}
	if kb.SummaryModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingSummaryModel)
	}
	if kb.ImageProcessingConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingImageProcessingModel)
	}
	if kb.VLMConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingVLMModel)
	}
	if kb.ASRConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingASRModel)
	}
	if kb.WikiConfig != nil && kb.WikiConfig.SynthesisModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingWikiSynthesisModel)
	}
	return bindings
}

func customAgentModelUsageBindings(agent *types.CustomAgent, modelID string) []types.ModelUsageBinding {
	bindings := make([]types.ModelUsageBinding, 0, 6)
	if agent.Config.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingChatModel)
	}
	if agent.Config.RerankModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingRerankModel)
	}
	if agent.Config.VLMModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingVLMModel)
	}
	if agent.Config.ASRModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingASRModel)
	}
	if agent.Config.QueryUnderstandModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingQueryUnderstandModel)
	}
	if agent.Config.QuestionSuggestions != nil &&
		agent.Config.QuestionSuggestions.FollowUps.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingFollowUpModel)
	}
	return bindings
}

// scopeKnowledgeBasesByModelID filters knowledge_bases rows that reference
// modelID in any model-binding field.
func scopeKnowledgeBasesByModelID(db *gorm.DB, modelID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where(
			"embedding_model_id = ? OR summary_model_id = ? OR "+
				"image_processing_config->>'model_id' = ? OR "+
				"vlm_config->>'model_id' = ? OR "+
				"asr_config->>'model_id' = ? OR "+
				"wiki_config->>'synthesis_model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	}
	return db.Where(
		"embedding_model_id = ? OR summary_model_id = ? OR "+
			"json_extract(image_processing_config, '$.model_id') = ? OR "+
			"json_extract(vlm_config, '$.model_id') = ? OR "+
			"json_extract(asr_config, '$.model_id') = ? OR "+
			"json_extract(wiki_config, '$.synthesis_model_id') = ?",
		modelID, modelID, modelID, modelID, modelID, modelID,
	)
}

// scopeCustomAgentsByModelID filters custom_agents rows whose config JSON
// references modelID in any model-binding field.
func scopeCustomAgentsByModelID(db *gorm.DB, modelID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where(
			"config->>'model_id' = ? OR config->>'rerank_model_id' = ? OR "+
				"config->>'vlm_model_id' = ? OR config->>'asr_model_id' = ? OR "+
				"config->>'query_understand_model_id' = ? OR "+
				"config->'question_suggestions'->'follow_ups'->>'model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	}
	return db.Where(
		"json_extract(config, '$.model_id') = ? OR "+
			"json_extract(config, '$.rerank_model_id') = ? OR "+
			"json_extract(config, '$.vlm_model_id') = ? OR "+
			"json_extract(config, '$.asr_model_id') = ? OR "+
			"json_extract(config, '$.query_understand_model_id') = ? OR "+
			"json_extract(config, '$.question_suggestions.follow_ups.model_id') = ?",
		modelID, modelID, modelID, modelID, modelID, modelID,
	)
}

func scopeCustomAgentsBySandboxConfigID(db *gorm.DB, configID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where("config->>'sandbox_config_id' = ?", configID)
	}
	return db.Where("json_extract(config, '$.sandbox_config_id') = ?", configID)
}
