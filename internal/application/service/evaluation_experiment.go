package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/buildinfo"
	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationExperimentInput carries every resolved fact needed to freeze one
// experiment manifest before the task enters Pending.
type EvaluationExperimentInput struct {
	Dataset               *types.EvaluationDatasetSnapshot
	SourceKnowledgeBaseID string
	Chunking              []byte
	Indexing              []byte
	EmbeddingModel        *types.Model
	ChatModel             *types.Model
	RerankModel           *types.Model
	SummaryModel          *types.Model
	Params                *types.ChatManage
	SeedProvided          bool
	SeedSupport           string
	DBDriver              string
	MetricRegistry        *metricregistry.Registry
}

// SelectEvaluationDefaultModel deterministically picks the default model of
// one type: an explicit default wins; ties break by ascending ID. Selection
// never depends on unordered database return order.
func SelectEvaluationDefaultModel(models []*types.Model, modelType types.ModelType) *types.Model {
	candidates := make([]*types.Model, 0, len(models))
	for _, model := range models {
		if model != nil && model.Type == modelType {
			candidates = append(candidates, model)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].IsDefault != candidates[j].IsDefault {
			return candidates[i].IsDefault
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates[0]
}

// BuildEvaluationExperimentSnapshot freezes the schema-version-1 manifest and
// its canonical hash. The snapshot never contains API keys, app secrets,
// custom authorization headers, full environment variables, or URLs carrying
// credentials; AssertEvaluationExperimentSecretFree re-verifies the bytes.
func BuildEvaluationExperimentSnapshot(
	input *EvaluationExperimentInput,
) (*types.EvaluationExperimentSnapshot, string, error) {
	if input == nil || input.Dataset == nil || input.Params == nil {
		return nil, "", errors.New("build evaluation experiment: dataset and resolved params are required")
	}
	if input.Dataset.DatasetVersionID == "" || input.Dataset.ContentSHA256 == "" {
		return nil, "", errors.New("build evaluation experiment: dataset version and content hash are required")
	}

	registry := input.MetricRegistry
	if registry == nil {
		var err error
		registry, err = metricregistry.NewDefaultRegistry()
		if err != nil {
			return nil, "", fmt.Errorf("build evaluation experiment: metric registry: %w", err)
		}
	}
	resolvedPlan, err := registry.Resolve(metricregistry.DefaultSpecs())
	if err != nil {
		return nil, "", fmt.Errorf("build evaluation experiment: metric plan: %w", err)
	}
	metricPlan := resolvedPlan.Snapshot.Clone()

	seed := input.Params.SummaryConfig.Seed
	var seedValue *int
	if input.SeedProvided {
		seedCopy := seed
		seedValue = &seedCopy
	}
	seedSupport := input.SeedSupport
	if seedSupport == "" {
		seedSupport = types.EvaluationSeedSupportUnavailable
	}

	var sourceKB *string
	if input.SourceKnowledgeBaseID != "" {
		source := input.SourceKnowledgeBaseID
		sourceKB = &source
	}

	build := buildinfo.Get()
	snapshot := &types.EvaluationExperimentSnapshot{
		SchemaVersion:         types.EvaluationExperimentSchemaVersion,
		Dataset:               *input.Dataset,
		SourceKnowledgeBaseID: sourceKB,
		Models: types.EvaluationModelSetSnapshot{
			Embedding: types.EvaluationModelSnapshotFrom(input.EmbeddingModel),
			Chat:      types.EvaluationModelSnapshotFrom(input.ChatModel),
			Rerank:    types.EvaluationModelSnapshotFrom(input.RerankModel),
			Summary:   types.EvaluationModelSnapshotFrom(input.SummaryModel),
		},
		Configuration: types.EvaluationConfigurationSnapshot{
			Retrieval: types.EvaluationRetrievalSnapshot{
				VectorThreshold:  input.Params.VectorThreshold,
				KeywordThreshold: input.Params.KeywordThreshold,
				EmbeddingTopK:    input.Params.EmbeddingTopK,
			},
			Rerank: types.EvaluationRerankSnapshot{
				RerankTopK:      input.Params.RerankTopK,
				RerankThreshold: input.Params.RerankThreshold,
			},
			Generation: types.EvaluationGenerationSnapshot{
				Seed:                seedValue,
				SeedProvided:        input.SeedProvided,
				SeedSupport:         seedSupport,
				MaxTokens:           input.Params.SummaryConfig.MaxTokens,
				MaxCompletionTokens: input.Params.SummaryConfig.MaxCompletionTokens,
				Temperature:         input.Params.SummaryConfig.Temperature,
				TopP:                input.Params.SummaryConfig.TopP,
				TopK:                input.Params.SummaryConfig.TopK,
				RepeatPenalty:       input.Params.SummaryConfig.RepeatPenalty,
				FrequencyPenalty:    input.Params.SummaryConfig.FrequencyPenalty,
				PresencePenalty:     input.Params.SummaryConfig.PresencePenalty,
			},
			Chunking: append([]byte(nil), input.Chunking...),
			Indexing: append([]byte(nil), input.Indexing...),
		},
		MetricPlan: metricPlan,
		Code: types.EvaluationCodeSnapshot{
			Version:   build.Version,
			CommitID:  build.CommitID,
			Dirty:     build.Dirty,
			BuildTime: build.BuildTime,
			GoVersion: build.GoVersion,
		},
		Environment: types.EvaluationEnvironmentSnapshot{
			Edition:  build.Edition,
			GOOS:     runtime.GOOS,
			GOARCH:   runtime.GOARCH,
			DBDriver: input.DBDriver,
		},
		Reproducibility: evaluationReproducibilityFor(seedSupport, build),
	}
	if snapshot.Configuration.Chunking == nil {
		snapshot.Configuration.Chunking = []byte(`{}`)
	}
	if snapshot.Configuration.Indexing == nil {
		snapshot.Configuration.Indexing = []byte(`{}`)
	}

	if err := AssertEvaluationExperimentSecretFree(snapshot); err != nil {
		return nil, "", err
	}
	hash, err := snapshot.SHA256()
	if err != nil {
		return nil, "", fmt.Errorf("build evaluation experiment: hash: %w", err)
	}
	return snapshot, hash, nil
}

func evaluationReproducibilityFor(
	seedSupport string,
	build buildinfo.Info,
) types.EvaluationReproducibilitySnapshot {
	warnings := make([]string, 0, 2)
	level := types.EvaluationReproducibilityAuditable
	if seedSupport != types.EvaluationSeedSupportApplied && seedSupport != types.EvaluationSeedSupportNotRequested {
		warnings = append(warnings, "generation seed is not applied by the provider")
		level = types.EvaluationReproducibilityPartial
	}
	if buildinfo.Unknown(build.CommitID) {
		warnings = append(warnings, "code commit is unavailable")
	}
	return types.EvaluationReproducibilitySnapshot{Level: level, Warnings: warnings}
}

// AssertEvaluationExperimentSecretFree scans the canonical snapshot bytes for
// credential-shaped material. It is a defense-in-depth check: the builders
// already exclude secrets by construction.
func AssertEvaluationExperimentSecretFree(snapshot *types.EvaluationExperimentSnapshot) error {
	canonical, err := snapshot.CanonicalJSON()
	if err != nil {
		return fmt.Errorf("scan evaluation experiment: %w", err)
	}
	text := string(canonical)
	for _, marker := range []string{
		`"api_key"`, `"app_secret"`, `"custom_headers"`, `"authorization"`, `"sk-`,
	} {
		if strings.Contains(strings.ToLower(text), marker) {
			return fmt.Errorf("evaluation experiment snapshot carries credential-shaped field %s", marker)
		}
	}
	if snapshot.Models.Embedding == nil || snapshot.Models.Chat == nil {
		return errors.New("evaluation experiment requires embedding and chat model snapshots")
	}
	if snapshot.SourceKnowledgeBaseID != nil && *snapshot.SourceKnowledgeBaseID == "" {
		return errors.New("evaluation experiment source knowledge base id must be null or non-empty")
	}
	return nil
}

// VerifyEvaluationModelFingerprint recomputes the behavior fingerprint of one
// model fetched at run time and fails the task when it drifted from the
// frozen snapshot, so one Success task never mixes two model configurations.
func VerifyEvaluationModelFingerprint(
	_ context.Context,
	role string,
	frozen *types.EvaluationModelSnapshot,
	current *types.Model,
) error {
	if frozen == nil {
		return nil
	}
	if current == nil {
		return fmt.Errorf("evaluation %s model %s is no longer available", role, frozen.ID)
	}
	if current.ID != frozen.ID {
		return fmt.Errorf("evaluation %s model identity drifted: frozen %s, current %s",
			role, frozen.ID, current.ID)
	}
	if got := types.EvaluationModelConfigSHA256ForVersion(current, frozen.ConfigVersion); got != frozen.ConfigSHA256 {
		return fmt.Errorf("evaluation %s model %s configuration drifted: frozen %s, current %s",
			role, frozen.ID, frozen.ConfigSHA256, got)
	}
	return nil
}
