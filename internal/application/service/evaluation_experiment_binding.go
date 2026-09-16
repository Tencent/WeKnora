package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// applyEvaluationConfigurationOverrides applies creation-request overrides to
// the resolved parameters before the experiment manifest is frozen.
func applyEvaluationConfigurationOverrides(
	params *types.ChatManage,
	overrides *types.EvaluationConfigurationOverrides,
) error {
	if overrides == nil {
		return nil
	}
	if overrides.Retrieval != nil {
		if overrides.Retrieval.VectorThreshold != nil {
			params.VectorThreshold = *overrides.Retrieval.VectorThreshold
		}
		if overrides.Retrieval.KeywordThreshold != nil {
			params.KeywordThreshold = *overrides.Retrieval.KeywordThreshold
		}
		if overrides.Retrieval.EmbeddingTopK != nil {
			params.EmbeddingTopK = *overrides.Retrieval.EmbeddingTopK
		}
	}
	if overrides.Rerank != nil {
		if overrides.Rerank.RerankTopK != nil {
			params.RerankTopK = *overrides.Rerank.RerankTopK
		}
		if overrides.Rerank.RerankThreshold != nil {
			params.RerankThreshold = *overrides.Rerank.RerankThreshold
		}
	}
	if overrides.Generation != nil {
		if overrides.Generation.Temperature != nil {
			params.SummaryConfig.Temperature = *overrides.Generation.Temperature
		}
		if overrides.Generation.TopP != nil {
			params.SummaryConfig.TopP = *overrides.Generation.TopP
		}
		if overrides.Generation.TopK != nil {
			params.SummaryConfig.TopK = *overrides.Generation.TopK
		}
		if overrides.Generation.MaxTokens != nil {
			params.SummaryConfig.MaxTokens = *overrides.Generation.MaxTokens
			params.SummaryConfig.MaxCompletionTokens = *overrides.Generation.MaxTokens
		}
	}
	return nil
}

// buildExperimentForTask resolves the dataset binding and model snapshots,
// then freezes the immutable experiment manifest. A nil registry keeps the
// legacy no-snapshot path for development fixtures; an explicit
// dataset_version_id without a registry is an error.
func (e *EvaluationService) buildExperimentForTask(
	ctx context.Context,
	tenantID uint64,
	options *types.EvaluationOptions,
	detail *types.EvaluationDetail,
	temporaryKBID string,
) (*types.EvaluationExperimentSnapshot, string, error) {
	if e.datasetRegistry == nil {
		if options.DatasetVersionID != "" {
			return nil, "", errors.New("build evaluation experiment: dataset registry is unavailable")
		}
		return nil, "", nil
	}

	datasetSnapshot, err := e.resolveExperimentDatasetBinding(
		ctx, tenantID, options.DatasetVersionID, detail.Task.DatasetID,
	)
	if err != nil {
		return nil, "", err
	}

	// Resolve the four model roles: embedding and summary come from the
	// temporary evaluation KB configuration; chat and rerank come from the
	// resolved task parameters.
	kb, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, temporaryKBID)
	if err != nil {
		return nil, "", fmt.Errorf("build evaluation experiment: load evaluation knowledge base: %w", err)
	}
	modelFor := func(role, id string) (*types.Model, error) {
		if id == "" {
			return nil, nil
		}
		model, err := e.modelService.GetModelByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("build evaluation experiment: %s model %s: %w", role, id, err)
		}
		return model, nil
	}
	embeddingModel, err := modelFor("embedding", kb.EmbeddingModelID)
	if err != nil {
		return nil, "", err
	}
	summaryModel, err := modelFor("summary", kb.SummaryModelID)
	if err != nil {
		return nil, "", err
	}
	chatModel, err := modelFor("chat", detail.Params.ChatModelID)
	if err != nil {
		return nil, "", err
	}
	rerankModel, err := modelFor("rerank", detail.Params.RerankModelID)
	if err != nil {
		return nil, "", err
	}

	// Seed capability is decided by the actual chat provider: an explicit
	// seed against an unsupported provider fails the request instead of
	// being silently saved-but-not-applied.
	seedSupport := types.EvaluationSeedSupportNotRequested
	if options.Seed != nil {
		seedSupport = seedSupportForChatModel(chatModel)
		if seedSupport == types.EvaluationSeedSupportUnsupported {
			return nil, "", fmt.Errorf("evaluation seed %d: chat model %s: %w",
				*options.Seed, detail.Params.ChatModelID, ErrEvaluationSeedUnsupported)
		}
	}

	dbDriver := os.Getenv("DB_DRIVER")
	if dbDriver == "" {
		dbDriver = "postgres"
	}
	chunking, err := json.Marshal(struct {
		Strategy           string `json:"strategy"`
		OnePassagePerChunk bool   `json:"one_passage_per_chunk"`
	}{
		Strategy:           "dataset_passage",
		OnePassagePerChunk: true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("build evaluation experiment: chunking snapshot: %w", err)
	}
	indexing, err := json.Marshal(struct {
		VectorEnabled             bool `json:"vector_enabled"`
		KeywordEnabled            bool `json:"keyword_enabled"`
		WikiEnabled               bool `json:"wiki_enabled"`
		GraphEnabled              bool `json:"graph_enabled"`
		SummaryEnabled            bool `json:"summary_enabled"`
		QuestionGenerationEnabled bool `json:"question_generation_enabled"`
	}{
		VectorEnabled:             kb.IndexingStrategy.VectorEnabled,
		KeywordEnabled:            kb.IndexingStrategy.KeywordEnabled,
		WikiEnabled:               kb.IndexingStrategy.WikiEnabled,
		GraphEnabled:              kb.IndexingStrategy.GraphEnabled,
		SummaryEnabled:            kb.SummaryModelID != "",
		QuestionGenerationEnabled: kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled,
	})
	if err != nil {
		return nil, "", fmt.Errorf("build evaluation experiment: indexing snapshot: %w", err)
	}
	// Freeze the effective provider budget in both public input aliases.
	// An explicit experiment cap takes precedence over inherited defaults.
	budget := (&chat.ChatOptions{
		MaxTokens:           detail.Params.SummaryConfig.MaxTokens,
		MaxCompletionTokens: detail.Params.SummaryConfig.MaxCompletionTokens,
	}).CompletionBudget()
	if chatModel != nil {
		limit := chatModel.Parameters.MaxOutputTokens
		if limit > 0 && (budget <= 0 || budget > limit) {
			budget = limit
		}
	}
	detail.Params.SummaryConfig.MaxTokens = budget
	detail.Params.SummaryConfig.MaxCompletionTokens = budget
	return BuildEvaluationExperimentSnapshot(&EvaluationExperimentInput{
		Dataset:               datasetSnapshot,
		SourceKnowledgeBaseID: options.KnowledgeBaseID,
		Chunking:              chunking,
		Indexing:              indexing,
		EmbeddingModel:        embeddingModel,
		ChatModel:             chatModel,
		RerankModel:           rerankModel,
		SummaryModel:          summaryModel,
		Params:                detail.Params,
		SeedProvided:          options.Seed != nil,
		SeedSupport:           seedSupport,
		DBDriver:              dbDriver,
		MetricRegistry:        e.metricRegistry,
	})
}

// ErrEvaluationSeedUnsupported is the typed creation-time failure for an
// explicit seed against a provider without seed support; HTTP maps it to 422.
var ErrEvaluationSeedUnsupported = errors.New("evaluation seed is not supported by the chat model provider")

// seedSupportForChatModel maps the resolved chat model to its seed
// capability: OpenAI-compatible providers and Ollama forward the seed and are
// reported applied; providers without a seed parameter report unsupported.
func seedSupportForChatModel(model *types.Model) string {
	if model == nil {
		return types.EvaluationSeedSupportUnavailable
	}
	switch chat.SeedSupportState(model.Parameters.Provider, model.Parameters.BaseURL, model.Source) {
	case chat.ChatSeedSupportApplied:
		return types.EvaluationSeedSupportApplied
	case chat.ChatSeedSupportUnsupported:
		return types.EvaluationSeedSupportUnsupported
	default:
		return types.EvaluationSeedSupportUnavailable
	}
}

// resolveExperimentDatasetBinding pins the dataset version for one task. An
// explicit dataset_version_id wins; otherwise the current version of the
// named dataset (usually the built-in "default") is used. Unknown ids fail
// explicitly and never fall back to a silent default.
func (e *EvaluationService) resolveExperimentDatasetBinding(
	ctx context.Context,
	tenantID uint64,
	datasetVersionID string,
	datasetID string,
) (*types.EvaluationDatasetSnapshot, error) {
	if datasetVersionID != "" {
		version, err := e.datasetRegistry.GetVersion(ctx, tenantID, datasetVersionID)
		if err != nil {
			return nil, fmt.Errorf("bind evaluation dataset version %s: %w", datasetVersionID, err)
		}
		return &types.EvaluationDatasetSnapshot{
			DatasetID:        version.DatasetID,
			DatasetVersionID: version.ID,
			VersionNumber:    version.VersionNumber,
			ArtifactSHA256:   version.ArtifactSHA256,
			ContentSHA256:    version.ContentSHA256,
		}, nil
	}

	dataset, err := e.datasetRegistry.GetDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, fmt.Errorf("bind evaluation dataset %s: %w", datasetID, err)
	}
	if dataset.CurrentVersionID == "" {
		return nil, fmt.Errorf("bind evaluation dataset %s: no registered version", datasetID)
	}
	version, err := e.datasetRegistry.GetVersion(ctx, tenantID, dataset.CurrentVersionID)
	if err != nil {
		return nil, fmt.Errorf("bind evaluation dataset %s version %s: %w",
			datasetID, dataset.CurrentVersionID, err)
	}
	return &types.EvaluationDatasetSnapshot{
		DatasetID:        dataset.ID,
		DatasetVersionID: version.ID,
		VersionNumber:    version.VersionNumber,
		ArtifactSHA256:   version.ArtifactSHA256,
		ContentSHA256:    version.ContentSHA256,
	}, nil
}

// verifyExperimentModelFingerprints re-fetches the frozen models at run time
// and fails the task when any behavior fingerprint drifted, so one Success
// task never mixes two model configurations.
func (e *EvaluationService) verifyExperimentModelFingerprints(
	ctx context.Context,
	experiment *types.EvaluationExperimentSnapshot,
) error {
	if experiment == nil {
		return nil
	}
	roles := []struct {
		name   string
		frozen *types.EvaluationModelSnapshot
	}{
		{"embedding", experiment.Models.Embedding},
		{"chat", experiment.Models.Chat},
		{"rerank", experiment.Models.Rerank},
		{"summary", experiment.Models.Summary},
	}
	for _, role := range roles {
		if role.frozen == nil {
			continue
		}
		current, err := e.modelService.GetModelByID(ctx, role.frozen.ID)
		if err != nil {
			return fmt.Errorf("verify evaluation %s model %s: %w", role.name, role.frozen.ID, err)
		}
		if err := VerifyEvaluationModelFingerprint(ctx, role.name, role.frozen, current); err != nil {
			return err
		}
	}
	return nil
}

// persistEvaluationExperiment fills the frozen manifest columns of one new
// task entity. The manifest is written once at creation; lifecycle
// compare-and-swap updates never touch these columns.
func persistEvaluationExperiment(
	entity *types.EvaluationTaskEntity,
	experiment *types.EvaluationExperimentSnapshot,
	experimentHash string,
) error {
	if experiment == nil {
		return nil
	}
	if len(experimentHash) != 64 {
		return errors.New("persist evaluation experiment: canonical hash is required")
	}
	encoded, err := encodeEvaluationExperiment(experiment)
	if err != nil {
		return err
	}
	datasetVersionID := experiment.Dataset.DatasetVersionID
	contentSHA256 := experiment.Dataset.ContentSHA256
	entity.DatasetVersionID = &datasetVersionID
	entity.DatasetContentSHA256 = &contentSHA256
	entity.ExperimentSnapshot = encoded
	entity.ExperimentSHA256 = &experimentHash
	return nil
}
