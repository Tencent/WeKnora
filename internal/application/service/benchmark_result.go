package service

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type benchmarkResultService struct {
	evaluationRuns interfaces.EvaluationRunRepository
	modelUsage     interfaces.ModelUsageRepository
}

func NewBenchmarkResultService(
	evaluationRuns interfaces.EvaluationRunRepository,
	modelUsage interfaces.ModelUsageRepository,
) interfaces.BenchmarkResultService {
	return &benchmarkResultService{evaluationRuns: evaluationRuns, modelUsage: modelUsage}
}

func (s *benchmarkResultService) GetBenchmarkResult(
	ctx context.Context, taskID string,
) (*types.BenchmarkResult, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	run, err := s.evaluationRuns.GetByTaskID(ctx, tenantID, taskID)
	if err != nil {
		return nil, fmt.Errorf("get evaluation run: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("task not found")
	}

	modelFacts, err := s.modelUsage.AggregateEvaluationRun(ctx, tenantID, run.ID)
	if err != nil {
		return nil, fmt.Errorf("aggregate benchmark model facts: %w", err)
	}
	quality, err := benchmarkQuality(run)
	if err != nil {
		return nil, err
	}

	return &types.BenchmarkResult{
		BenchmarkVersion: run.ConfigSnapshot.BenchmarkContractVersion,
		Run: types.BenchmarkRunSummary{
			EvaluationRunID: run.ID, TaskID: run.TaskID, Status: run.Status,
			Total: run.Total, Finished: run.Finished, CreatedAt: run.CreatedAt,
			StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, ErrorMessage: run.ErrorMessage,
		},
		Config: run.ConfigSnapshot, Quality: quality,
		RunWallClockDurationMS: run.DurationMS, ModelFacts: modelFacts,
		Reproducibility: benchmarkReproducibility(run.ConfigSnapshot),
	}, nil
}

func benchmarkQuality(run *types.EvaluationRun) (types.BenchmarkQuality, error) {
	switch run.Status {
	case types.EvaluationStatuePending, types.EvaluationStatueRunning:
		return types.BenchmarkQuality{State: types.BenchmarkQualityStatePending}, nil
	case types.EvaluationStatueFailed:
		return types.BenchmarkQuality{State: types.BenchmarkQualityStateUnavailable}, nil
	case types.EvaluationStatueSuccess:
		if evaluationRunMetric(run) == nil {
			return types.BenchmarkQuality{}, fmt.Errorf(
				"benchmark invariant violation: successful run %q does not have all quality metrics", run.TaskID,
			)
		}
		return types.BenchmarkQuality{
			State: types.BenchmarkQualityStateComplete,
			Retrieval: &types.BenchmarkRetrievalQuality{
				Precision: run.Precision, Recall: run.Recall, NDCG3: run.NDCG3,
				NDCG10: run.NDCG10, MRR: run.MRR, MAP: run.MAP,
			},
			Answer: &types.BenchmarkAnswerQuality{
				BLEU1: run.BLEU1, BLEU2: run.BLEU2, BLEU4: run.BLEU4,
				ROUGE1: run.ROUGE1, ROUGE2: run.ROUGE2, ROUGEL: run.ROUGEL,
			},
		}, nil
	default:
		return types.BenchmarkQuality{}, fmt.Errorf("benchmark invariant violation: unknown evaluation status %d", run.Status)
	}
}

func benchmarkReproducibility(snapshot types.EvaluationConfigSnapshotV1) types.BenchmarkReproducibilityState {
	if err := validateBenchmarkReproducibilitySnapshot(snapshot); err != nil {
		return types.BenchmarkReproducibilityLegacyUnknown
	}
	return types.BenchmarkReproducibilityComplete
}

var lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateBenchmarkReproducibilitySnapshot(snapshot types.EvaluationConfigSnapshotV1) error {
	if snapshot.SnapshotSchemaVersion != 1 {
		return fmt.Errorf("unsupported snapshot schema version %d", snapshot.SnapshotSchemaVersion)
	}
	if snapshot.BenchmarkContractVersion != types.BenchmarkContractVersionV11 {
		return fmt.Errorf("unsupported benchmark contract version %q", snapshot.BenchmarkContractVersion)
	}
	dataset := snapshot.Dataset
	if strings.TrimSpace(dataset.DatasetID) == "" {
		return fmt.Errorf("dataset id is required")
	}
	if !lowercaseSHA256Pattern.MatchString(dataset.DatasetSemanticSHA256) {
		return fmt.Errorf("dataset semantic hash must be 64 lowercase hexadecimal characters")
	}
	if dataset.CorpusCount <= 0 || dataset.QuestionCount <= 0 || dataset.QrelsCount <= 0 || dataset.AnswerCount <= 0 {
		return fmt.Errorf("dataset counts must be positive")
	}
	if dataset.QrelsCount < dataset.QuestionCount {
		return fmt.Errorf("qrels count must cover every question")
	}
	if dataset.AnswerCount < dataset.QuestionCount {
		return fmt.Errorf("answer count must cover every question")
	}
	if dataset.CorpusMode != "pre_chunked_passages" || dataset.ChunkingApplied {
		return fmt.Errorf("dataset must use the unchunked pre-chunked passage corpus")
	}

	tokenizer := snapshot.Pipeline.Tokenizer
	if strings.TrimSpace(snapshot.Pipeline.Name) == "" {
		return fmt.Errorf("pipeline name is required")
	}
	if tokenizer.Name != "jieba" {
		return fmt.Errorf("tokenizer must be jieba")
	}
	switch tokenizer.DictionaryMode {
	case "builtin":
	case "custom":
		if !lowercaseSHA256Pattern.MatchString(tokenizer.DictionaryFingerprint) {
			return fmt.Errorf("custom tokenizer dictionary fingerprint must be 64 lowercase hexadecimal characters")
		}
	default:
		return fmt.Errorf("unsupported tokenizer dictionary mode %q", tokenizer.DictionaryMode)
	}

	wantMetrics := []string{"precision", "recall", "ndcg", "mrr", "map", "bleu", "rouge"}
	if !slices.Equal(snapshot.Pipeline.Metrics, wantMetrics) {
		return fmt.Errorf("metric suite does not match Benchmark v1.1")
	}
	if !slices.Equal(snapshot.Pipeline.NDCGCutoffs, []int{3, 10}) {
		return fmt.Errorf("NDCG cutoffs do not match Benchmark v1.1")
	}
	if snapshot.Execution.WorkerLimit <= 0 {
		return fmt.Errorf("worker limit must be positive")
	}

	if strings.TrimSpace(snapshot.Models.EmbeddingModelID) == "" ||
		strings.TrimSpace(snapshot.Models.ChatModelID) == "" ||
		strings.TrimSpace(snapshot.Models.SummaryModelID) == "" {
		return fmt.Errorf("required model ids are missing")
	}
	if err := validateConfiguredBenchmarkModel(
		"embedding", snapshot.Models.Embedding, snapshot.Models.EmbeddingModelID,
	); err != nil {
		return err
	}
	if snapshot.Models.Embedding.Embedding == nil {
		return fmt.Errorf("embedding model parameters are missing")
	}
	if err := validateConfiguredBenchmarkModel("chat", snapshot.Models.Chat, snapshot.Models.ChatModelID); err != nil {
		return err
	}
	if err := validateConfiguredBenchmarkModel(
		"summary", snapshot.Models.Summary, snapshot.Models.SummaryModelID,
	); err != nil {
		return err
	}
	if snapshot.Models.RerankModelID != nil {
		if strings.TrimSpace(*snapshot.Models.RerankModelID) == "" {
			return fmt.Errorf("rerank model id is empty")
		}
		if err := validateConfiguredBenchmarkModel(
			"rerank", snapshot.Models.Rerank, *snapshot.Models.RerankModelID,
		); err != nil {
			return err
		}
	} else if snapshot.Models.Rerank != nil {
		return fmt.Errorf("rerank model snapshot exists without an id")
	}
	return nil
}

func validateConfiguredBenchmarkModel(
	role string, model *types.EvaluationConfiguredModelSnapshot, configuredID string,
) error {
	if model == nil {
		return fmt.Errorf("%s model snapshot is missing", role)
	}
	if strings.TrimSpace(model.ID) == "" || strings.TrimSpace(model.Name) == "" ||
		strings.TrimSpace(model.Type) == "" || strings.TrimSpace(model.Source) == "" {
		return fmt.Errorf("%s model identity is incomplete", role)
	}
	if model.ID != configuredID {
		return fmt.Errorf("%s model id does not match configured id", role)
	}
	if model.Source == string(types.ModelSourceRemote) && !lowercaseSHA256Pattern.MatchString(model.EndpointFingerprint) {
		return fmt.Errorf("remote %s model endpoint fingerprint is missing or invalid", role)
	}
	return nil
}
