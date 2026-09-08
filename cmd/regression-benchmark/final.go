package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type finalProfile struct {
	SchemaVersion    int            `json:"schema_version"`
	BenchmarkID      string         `json:"benchmark_id"`
	BenchmarkVersion string         `json:"benchmark_version"`
	Dataset          profileDataset `json:"dataset"`
	Models           profileModels  `json:"models"`
	Runtime          profileRuntime `json:"runtime"`
}

type profileDataset struct {
	SemanticSHA256 string `json:"semantic_sha256"`
	Corpus         int    `json:"corpus"`
	Questions      int    `json:"questions"`
	Qrels          int    `json:"qrels"`
	Answers        int    `json:"answers"`
}

type profileModel struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Source    string `json:"source"`
	Provider  string `json:"provider"`
	Dimension int    `json:"dimension,omitempty"`
}

type profileModels struct {
	Embedding profileModel `json:"embedding"`
	Chat      profileModel `json:"chat"`
	Rerank    profileModel `json:"rerank"`
}

type profileRetrieval struct {
	VectorThreshold  float64 `json:"vector_threshold"`
	KeywordThreshold float64 `json:"keyword_threshold"`
	EmbeddingTopK    int     `json:"embedding_top_k"`
	RerankTopK       int     `json:"rerank_top_k"`
	RerankThreshold  float64 `json:"rerank_threshold"`
	RetrieveDriver   string  `json:"retrieve_driver"`
}

type profileGeneration struct {
	MaxRounds           int     `json:"max_rounds"`
	MaxInputChars       int     `json:"max_input_chars"`
	MaxTokens           int     `json:"max_tokens"`
	RepeatPenalty       float64 `json:"repeat_penalty"`
	TopK                int     `json:"top_k"`
	TopP                float64 `json:"top_p"`
	FrequencyPenalty    float64 `json:"frequency_penalty"`
	PresencePenalty     float64 `json:"presence_penalty"`
	Temperature         float64 `json:"temperature"`
	Seed                int     `json:"seed"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
}

type profileRuntime struct {
	WorkerLimit int               `json:"worker_limit"`
	CacheMode   string            `json:"cache_mode"`
	Retrieval   profileRetrieval  `json:"retrieval"`
	Generation  profileGeneration `json:"generation"`
}

type preflightEnvironment struct {
	CommitSHA      string
	Dataset        types.EvaluationDatasetIdentity
	Models         []*types.Model
	Config         *appconfig.Config
	RetrieveDriver string
	WorkerLimit    int
	CacheEnabled   bool
}

type selectedModels struct {
	EmbeddingID string
	ChatID      string
	RerankID    string
}

type benchmarkExecutionMode string

const (
	executionModeStrict benchmarkExecutionMode = "strict"
	executionModeCustom benchmarkExecutionMode = "custom"
)

func parseExecutionMode(value string) (benchmarkExecutionMode, error) {
	mode := benchmarkExecutionMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case executionModeStrict, executionModeCustom:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid execution mode %q (expected strict or custom)", value)
	}
}

func loadFinalProfile(path string) (finalProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return finalProfile{}, fmt.Errorf("read final benchmark profile: %w", err)
	}
	var p finalProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return finalProfile{}, fmt.Errorf("decode final benchmark profile: %w", err)
	}
	if p.SchemaVersion != 1 || p.BenchmarkID == "" || p.BenchmarkVersion == "" {
		return finalProfile{}, errors.New("invalid final benchmark profile header")
	}
	return p, nil
}

func validatePreflight(p finalProfile, env preflightEnvironment) (selectedModels, error) {
	if err := validatePreflightBase(p, env); err != nil {
		return selectedModels{}, err
	}

	// EvaluationService creates its temporary KB by iterating every visible
	// model row, without filtering status or imposing an order, and retains the
	// last Embedding and KnowledgeQA rows it sees. Requiring exactly one visible
	// row for each auto-selected role is therefore the wrapper-level invariant
	// that makes the embedding and summary choices deterministic.
	embedding, err := requireSingleAutomaticModel(env.Models, "embedding", types.ModelTypeEmbedding)
	if err != nil {
		return selectedModels{}, err
	}
	chat, err := requireSingleAutomaticModel(env.Models, "KnowledgeQA (chat/summary)", types.ModelTypeKnowledgeQA)
	if err != nil {
		return selectedModels{}, err
	}
	if err := validateExactModel("embedding", p.Models.Embedding, embedding); err != nil {
		return selectedModels{}, err
	}
	if err := validateExactModel("chat/summary", p.Models.Chat, chat); err != nil {
		return selectedModels{}, err
	}
	rerank, err := requireSingleExactActiveModel(env.Models, "rerank", p.Models.Rerank)
	if err != nil {
		return selectedModels{}, err
	}
	return selectedModels{EmbeddingID: embedding.ID, ChatID: chat.ID, RerankID: rerank.ID}, nil
}

func validateCustomPreflight(p finalProfile, env preflightEnvironment) (selectedModels, error) {
	if err := validatePreflightBase(p, env); err != nil {
		return selectedModels{}, err
	}
	embedding, err := requireSingleAutomaticModel(env.Models, "embedding", types.ModelTypeEmbedding)
	if err != nil {
		return selectedModels{}, err
	}
	chat, err := requireSingleAutomaticModel(env.Models, "KnowledgeQA (chat/summary)", types.ModelTypeKnowledgeQA)
	if err != nil {
		return selectedModels{}, err
	}
	rerank, err := requireSingleActiveModel(env.Models, "rerank", types.ModelTypeRerank)
	if err != nil {
		return selectedModels{}, err
	}
	return selectedModels{EmbeddingID: embedding.ID, ChatID: chat.ID, RerankID: rerank.ID}, nil
}

func validatePreflightBase(p finalProfile, env preflightEnvironment) error {
	if strings.TrimSpace(env.CommitSHA) == "" {
		return errors.New("git commit unreadable")
	}
	want, got := p.Dataset, env.Dataset
	if got.DatasetID != p.BenchmarkID {
		return fmt.Errorf("benchmark dataset mismatch: expected %s, actual %s", p.BenchmarkID, got.DatasetID)
	}
	if got.DatasetSemanticSHA256 != want.SemanticSHA256 {
		return fmt.Errorf(
			"benchmark dataset semantic SHA mismatch: expected %s, actual %s",
			want.SemanticSHA256, got.DatasetSemanticSHA256,
		)
	}
	if got.CorpusCount != want.Corpus || got.QuestionCount != want.Questions ||
		got.QrelsCount != want.Qrels || got.AnswerCount != want.Answers {
		return fmt.Errorf(
			"benchmark dataset count mismatch: expected corpus/questions/qrels/answers "+
				"%d/%d/%d/%d, actual %d/%d/%d/%d",
			want.Corpus, want.Questions, want.Qrels, want.Answers,
			got.CorpusCount, got.QuestionCount, got.QrelsCount, got.AnswerCount,
		)
	}
	if env.CacheEnabled || !strings.EqualFold(p.Runtime.CacheMode, "off") {
		return errors.New("embedding cache must be OFF for the benchmark profile")
	}
	if env.WorkerLimit != p.Runtime.WorkerLimit {
		return fmt.Errorf(
			"worker limit mismatch: expected %d, actual %d (set GOMAXPROCS=%d)",
			p.Runtime.WorkerLimit, env.WorkerLimit, p.Runtime.WorkerLimit+1,
		)
	}
	if env.Config == nil || env.Config.Conversation == nil || env.Config.Conversation.Summary == nil {
		return errors.New("benchmark runtime conversation configuration unavailable")
	}
	if err := validateRuntime(p.Runtime, env); err != nil {
		return err
	}
	return nil
}

func requireSingleAutomaticModel(
	models []*types.Model, label string, modelType types.ModelType,
) (*types.Model, error) {
	candidates := modelsOfType(models, modelType, false)
	if len(candidates) != 1 {
		return nil, fmt.Errorf(
			"evaluation service automatic %s selection is ambiguous: "+
				"expected exactly one visible %s row, found %d (%s)",
			label, modelType, len(candidates), describeModels(candidates),
		)
	}
	if err := validateUsableModel(label, candidates[0]); err != nil {
		return nil, err
	}
	return candidates[0], nil
}

func requireSingleActiveModel(
	models []*types.Model, label string, modelType types.ModelType,
) (*types.Model, error) {
	candidates := modelsOfType(models, modelType, true)
	if len(candidates) != 1 {
		return nil, fmt.Errorf(
			"custom benchmark %s selection is ambiguous: "+
				"expected exactly one active %s row, found %d (%s)",
			label, modelType, len(candidates), describeModels(candidates),
		)
	}
	if err := validateUsableModel(label, candidates[0]); err != nil {
		return nil, err
	}
	return candidates[0], nil
}

func requireSingleExactActiveModel(models []*types.Model, label string, want profileModel) (*types.Model, error) {
	matches := make([]*types.Model, 0, 1)
	actual := modelsOfType(models, types.ModelType(want.Type), true)
	for _, model := range actual {
		if model.Name == want.Name && string(model.Source) == want.Source && model.Parameters.Provider == want.Provider {
			matches = append(matches, model)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf(
			"configured %s model does not match final benchmark profile. "+
				"Expected exactly one %s (%s/%s). Actual: %s. "+
				"This run cannot be compared with the published final baseline",
			label, want.Name, want.Source, want.Provider, describeModels(actual),
		)
	}
	if err := validateExactModel(label, want, matches[0]); err != nil {
		return nil, err
	}
	return matches[0], nil
}

func validateExactModel(label string, want profileModel, model *types.Model) error {
	if model.Name != want.Name || string(model.Type) != want.Type ||
		string(model.Source) != want.Source || model.Parameters.Provider != want.Provider {
		return fmt.Errorf(
			"configured %s model does not match final benchmark profile. "+
				"Expected: %s (%s/%s). Actual: %s. "+
				"This run cannot be compared with the published final baseline",
			label, want.Name, want.Source, want.Provider, describeModels([]*types.Model{model}),
		)
	}
	if want.Dimension != 0 && model.Parameters.EmbeddingParameters.Dimension != want.Dimension {
		return fmt.Errorf(
			"configured %s model dimension mismatch: expected %d, actual %d",
			label, want.Dimension, model.Parameters.EmbeddingParameters.Dimension,
		)
	}
	return validateUsableModel(label, model)
}

func validateUsableModel(label string, model *types.Model) error {
	if model == nil || strings.TrimSpace(model.ID) == "" {
		return fmt.Errorf("required %s model has no usable ID", label)
	}
	if model.Status != types.ModelStatusActive {
		return fmt.Errorf("required %s model %s is not active (status=%s)", label, model.Name, model.Status)
	}
	if model.Source != types.ModelSourceLocal && model.Parameters.APIKey == "" && model.Parameters.AppSecret == "" {
		return fmt.Errorf("required credential unavailable for %s model %s", label, model.Name)
	}
	return nil
}

func modelsOfType(models []*types.Model, modelType types.ModelType, activeOnly bool) []*types.Model {
	result := make([]*types.Model, 0)
	for _, model := range models {
		if model == nil || model.Type != modelType || (activeOnly && model.Status != types.ModelStatusActive) {
			continue
		}
		result = append(result, model)
	}
	return result
}

func describeModels(models []*types.Model) string {
	values := make([]string, 0, len(models))
	for _, model := range models {
		values = append(values, fmt.Sprintf(
			"%s[id=%s,status=%s,source=%s,provider=%s]",
			model.Name, model.ID, model.Status, model.Source, model.Parameters.Provider,
		))
	}
	sort.Strings(values)
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func validateRuntime(want profileRuntime, env preflightEnvironment) error {
	c := env.Config.Conversation
	gotRetrieval := profileRetrieval{
		VectorThreshold: c.VectorThreshold, KeywordThreshold: c.KeywordThreshold,
		EmbeddingTopK: c.EmbeddingTopK, RerankTopK: c.RerankTopK,
		RerankThreshold: c.RerankThreshold, RetrieveDriver: env.RetrieveDriver,
	}
	if gotRetrieval != want.Retrieval {
		return fmt.Errorf(
			"retrieval configuration does not match final benchmark profile: expected %+v, actual %+v",
			want.Retrieval, gotRetrieval,
		)
	}
	s := c.Summary
	gotGeneration := profileGeneration{
		MaxRounds: c.MaxRounds, MaxInputChars: s.MaxInputChars, MaxTokens: s.MaxTokens,
		RepeatPenalty: s.RepeatPenalty, TopK: s.TopK, TopP: s.TopP,
		FrequencyPenalty: s.FrequencyPenalty, PresencePenalty: s.PresencePenalty,
		Temperature: s.Temperature, Seed: s.Seed, MaxCompletionTokens: s.MaxCompletionTokens,
	}
	if gotGeneration != want.Generation {
		return fmt.Errorf(
			"generation configuration does not match final benchmark profile: expected %+v, actual %+v",
			want.Generation, gotGeneration,
		)
	}
	return nil
}

func currentCommitSHA() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func checkBackend(ctx context.Context, baseURL string) error {
	return checkBackendWithClient(ctx, baseURL, http.DefaultClient)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func checkBackendWithClient(ctx context.Context, baseURL string, client httpDoer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/health", nil)
	if err != nil {
		return fmt.Errorf("backend unavailable: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("backend unavailable at %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("backend unavailable at %s: health returned HTTP %d", baseURL, resp.StatusCode)
	}
	return nil
}

func checkOutputWritable(path string) error {
	parent := filepath.Dir(path)
	for {
		info, err := os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("output path cannot be written: %s is not a directory", parent)
			}
			f, err := os.CreateTemp(parent, ".benchmark-write-check-*")
			if err != nil {
				return fmt.Errorf("output path cannot be written: %w", err)
			}
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("output path cannot be written: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return errors.New("output path cannot be written: no existing parent directory")
		}
		parent = next
	}
}

func loadDatasetIdentity(ctx context.Context, id string) (types.EvaluationDatasetIdentity, error) {
	dataset, err := service.NewDatasetService().GetDatasetByID(ctx, id)
	if err != nil {
		return types.EvaluationDatasetIdentity{}, fmt.Errorf("benchmark dataset missing or invalid: %w", err)
	}
	return dataset.Identity, nil
}

func loadConfiguredModels(ctx context.Context, tenant uint64) ([]*types.Model, error) {
	driver := os.Getenv("DB_DRIVER")
	var dialector gorm.Dialector
	switch driver {
	case "postgres":
		dsn := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
			os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"),
		)
		dialector = postgres.Open(dsn)
	case "sqlite":
		path := os.Getenv("DB_PATH")
		if path == "" {
			path = "./data/weknora.db"
		}
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("postgresql unavailable (configured sqlite database missing): %w", err)
		}
		dialector = sqlite.Open(path + "?mode=ro")
	default:
		return nil, fmt.Errorf("postgresql unavailable: unsupported DB_DRIVER %q", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("postgresql unavailable: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgresql unavailable: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("postgresql unavailable: %w", err)
	}
	models, err := repository.NewModelRepository(db).List(ctx, tenant, "", "")
	if err != nil {
		return nil, fmt.Errorf("read configured models: %w", err)
	}
	return models, nil
}

func collectPreflightEnvironment(ctx context.Context, tenant uint64) (preflightEnvironment, error) {
	sha, err := currentCommitSHA()
	if err != nil {
		return preflightEnvironment{}, fmt.Errorf("git commit unreadable: %w", err)
	}
	identity, err := loadDatasetIdentity(ctx, "benchmark_v1")
	if err != nil {
		return preflightEnvironment{}, err
	}
	cfg, err := appconfig.LoadConfig()
	if err != nil {
		return preflightEnvironment{}, fmt.Errorf("load runtime configuration: %w", err)
	}
	models, err := loadConfiguredModels(ctx, tenant)
	if err != nil {
		return preflightEnvironment{}, err
	}
	cacheConfig, cacheWarnings := embedding.LoadEmbeddingCacheConfigFromEnv()
	if len(cacheWarnings) != 0 {
		return preflightEnvironment{}, fmt.Errorf("embedding cache mode cannot be confirmed: %v", cacheWarnings[0])
	}
	return preflightEnvironment{
		CommitSHA: sha, Dataset: identity, Models: models, Config: cfg,
		RetrieveDriver: os.Getenv("RETRIEVE_DRIVER"),
		WorkerLimit:    max(runtime.GOMAXPROCS(0)-1, 1),
		CacheEnabled:   cacheConfig.Enabled,
	}, nil
}

type artifactModel struct {
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Source            string  `json:"source"`
	Provider          string  `json:"provider"`
	ResolvedProvider  *string `json:"resolved_provider"`
	ResolvedModelName *string `json:"resolved_model_name"`
}

type finalArtifact struct {
	SchemaVersion             int                                  `json:"schema_version"`
	ExecutionMode             benchmarkExecutionMode               `json:"execution_mode"`
	ComparableToFinalBaseline bool                                 `json:"comparable_to_final_baseline"`
	ComparisonNote            string                               `json:"comparison_note"`
	BenchmarkID               string                               `json:"benchmark_id"`
	BenchmarkVersion          string                               `json:"benchmark_version"`
	CommitSHA                 string                               `json:"commit_sha"`
	GeneratedAt               time.Time                            `json:"generated_at"`
	Dataset                   profileDataset                       `json:"dataset"`
	Models                    map[string]artifactModel             `json:"models"`
	Runtime                   profileRuntime                       `json:"runtime"`
	EvaluationRunID           string                               `json:"evaluation_run_id"`
	Metrics                   types.BenchmarkQuality               `json:"metrics"`
	Usage                     *types.EvaluationModelUsageAggregate `json:"usage"`
	Latency                   artifactLatency                      `json:"latency"`
	Reproducibility           types.BenchmarkReproducibilityState  `json:"reproducibility"`
}

type artifactLatency struct {
	RunWallClockDurationMS *int64                 `json:"run_wall_clock_duration_ms"`
	ModelCalls             types.LatencyAggregate `json:"model_calls"`
}

func buildFinalArtifact(
	p finalProfile, commit string, now time.Time, result *types.BenchmarkResult,
) (finalArtifact, error) {
	return buildBenchmarkArtifact(executionModeStrict, p, commit, now, result)
}

func buildBenchmarkArtifact(
	mode benchmarkExecutionMode, p finalProfile, commit string,
	now time.Time, result *types.BenchmarkResult,
) (finalArtifact, error) {
	if result == nil || result.Run.EvaluationRunID == "" {
		return finalArtifact{}, errors.New("cannot build benchmark artifact from empty unified benchmark result")
	}
	if err := validateResultProfile(mode, p, result); err != nil {
		return finalArtifact{}, err
	}
	models := map[string]artifactModel{
		"embedding": artifactModelFromSnapshot(result.Config.Models.Embedding),
		"chat":      artifactModelFromSnapshot(result.Config.Models.Chat),
		"summary":   artifactModelFromSnapshot(result.Config.Models.Summary),
		"rerank":    artifactModelFromSnapshot(result.Config.Models.Rerank),
	}
	var latency types.LatencyAggregate
	if result.ModelFacts != nil {
		latency = result.ModelFacts.Latency
		for _, observed := range result.ModelFacts.ObservedModels {
			key := string(observed.CallType)
			m, ok := models[key]
			if !ok {
				continue
			}
			m.ResolvedProvider = stringPointer(observed.ResolvedProvider)
			if observed.ResolvedModelName != nil {
				m.ResolvedModelName = stringPointer(*observed.ResolvedModelName)
			}
			models[key] = m
			if observed.CallType == types.CallTypeChat &&
				result.Config.Models.Summary != nil && result.Config.Models.Chat != nil &&
				result.Config.Models.Summary.ID == result.Config.Models.Chat.ID {
				summary := models["summary"]
				summary.ResolvedProvider = stringPointer(observed.ResolvedProvider)
				if observed.ResolvedModelName != nil {
					summary.ResolvedModelName = stringPointer(*observed.ResolvedModelName)
				}
				models["summary"] = summary
			}
		}
	}
	comparable := mode == executionModeStrict
	note := "Uses environment-specific models and MUST NOT be compared directly " +
		"with the published Final Benchmark baseline."
	if comparable {
		note = "Matches the published final benchmark profile."
	}
	return finalArtifact{
		SchemaVersion: 1, ExecutionMode: mode, ComparableToFinalBaseline: comparable, ComparisonNote: note,
		BenchmarkID: p.BenchmarkID, BenchmarkVersion: p.BenchmarkVersion, CommitSHA: commit, GeneratedAt: now.UTC(),
		Dataset: p.Dataset, Models: models, Runtime: p.Runtime, EvaluationRunID: result.Run.EvaluationRunID,
		Metrics: result.Quality, Usage: result.ModelFacts,
		Latency: artifactLatency{result.RunWallClockDurationMS, latency}, Reproducibility: result.Reproducibility,
	}, nil
}

func validateResultProfile(mode benchmarkExecutionMode, p finalProfile, result *types.BenchmarkResult) error {
	if mode != executionModeStrict && mode != executionModeCustom {
		return fmt.Errorf("unsupported benchmark execution mode %q", mode)
	}
	if result.BenchmarkVersion != p.BenchmarkVersion {
		return fmt.Errorf(
			"unified benchmark result version mismatch: expected %s, actual %s",
			p.BenchmarkVersion, result.BenchmarkVersion,
		)
	}
	d := result.Config.Dataset
	if d.DatasetID != p.BenchmarkID || d.DatasetSemanticSHA256 != p.Dataset.SemanticSHA256 ||
		d.CorpusCount != p.Dataset.Corpus || d.QuestionCount != p.Dataset.Questions ||
		d.QrelsCount != p.Dataset.Qrels || d.AnswerCount != p.Dataset.Answers {
		return errors.New("unified benchmark result dataset does not match final benchmark profile")
	}
	configured := []struct {
		label string
		got   *types.EvaluationConfiguredModelSnapshot
	}{
		{"embedding", result.Config.Models.Embedding},
		{"chat", result.Config.Models.Chat},
		{"summary", result.Config.Models.Summary},
		{"rerank", result.Config.Models.Rerank},
	}
	for _, model := range configured {
		if model.got == nil {
			return fmt.Errorf("unified benchmark result has no configured %s model", model.label)
		}
	}
	if mode == executionModeStrict {
		checks := []struct {
			label string
			want  profileModel
			got   *types.EvaluationConfiguredModelSnapshot
		}{
			{"embedding", p.Models.Embedding, result.Config.Models.Embedding},
			{"chat", p.Models.Chat, result.Config.Models.Chat},
			{"summary", p.Models.Chat, result.Config.Models.Summary},
			{"rerank", p.Models.Rerank, result.Config.Models.Rerank},
		}
		for _, check := range checks {
			if check.got.Name != check.want.Name || check.got.Type != check.want.Type ||
				check.got.Source != check.want.Source || check.got.Provider != check.want.Provider {
				return fmt.Errorf("unified benchmark result %s model does not match final benchmark profile", check.label)
			}
			if check.want.Dimension != 0 &&
				(check.got.Embedding == nil || check.got.Embedding.Dimension != check.want.Dimension) {
				return fmt.Errorf("unified benchmark result %s dimension does not match final benchmark profile", check.label)
			}
		}
	}
	if result.Config.Execution.WorkerLimit != p.Runtime.WorkerLimit {
		return errors.New("unified benchmark result worker limit does not match final benchmark profile")
	}
	r := result.Config.Retrieval
	gotRetrieval := profileRetrieval{
		r.VectorThreshold, r.KeywordThreshold, r.EmbeddingTopK,
		r.RerankTopK, r.RerankThreshold, r.RetrieveDriver,
	}
	if gotRetrieval != p.Runtime.Retrieval {
		return errors.New("unified benchmark result retrieval settings do not match final benchmark profile")
	}
	g := result.Config.Generation
	s := g.SummaryConfig
	// MaxInputChars is validated from config during preflight; the frozen v1.1
	// EvaluationGenerationSnapshot predates that field and cannot re-state it.
	gotGeneration := profileGeneration{
		g.MaxRounds, p.Runtime.Generation.MaxInputChars, s.MaxTokens,
		s.RepeatPenalty, s.TopK, s.TopP, s.FrequencyPenalty,
		s.PresencePenalty, s.Temperature, s.Seed, s.MaxCompletionTokens,
	}
	if gotGeneration != p.Runtime.Generation {
		return errors.New("unified benchmark result generation settings do not match final benchmark profile")
	}
	if result.ModelFacts == nil {
		return errors.New("unified benchmark result has no model usage facts")
	}
	if result.Run.Status != types.EvaluationStatueSuccess {
		return fmt.Errorf("unified benchmark result run is not successful (status=%d)", result.Run.Status)
	}
	if result.Quality.State != types.BenchmarkQualityStateComplete {
		return fmt.Errorf("unified benchmark result quality is not complete (state=%s)", result.Quality.State)
	}
	if err := validateCompleteMetrics(result.Quality); err != nil {
		return err
	}
	if result.Reproducibility != types.BenchmarkReproducibilityComplete {
		return fmt.Errorf("unified benchmark result reproducibility is not complete (state=%s)", result.Reproducibility)
	}
	return nil
}

func validateCompleteMetrics(quality types.BenchmarkQuality) error {
	if quality.Retrieval == nil {
		return errors.New("unified benchmark result retrieval metrics are missing")
	}
	retrieval := []struct {
		name  string
		value *float64
	}{
		{"Precision", quality.Retrieval.Precision},
		{"Recall", quality.Retrieval.Recall},
		{"NDCG@3", quality.Retrieval.NDCG3},
		{"NDCG@10", quality.Retrieval.NDCG10},
		{"MRR", quality.Retrieval.MRR},
		{"MAP", quality.Retrieval.MAP},
	}
	for _, metric := range retrieval {
		if metric.value == nil {
			return fmt.Errorf("unified benchmark result retrieval metric %s is missing", metric.name)
		}
	}
	if quality.Answer == nil {
		return errors.New("unified benchmark result answer metrics are missing")
	}
	answer := []struct {
		name  string
		value *float64
	}{
		{"BLEU-1", quality.Answer.BLEU1},
		{"BLEU-2", quality.Answer.BLEU2},
		{"BLEU-4", quality.Answer.BLEU4},
		{"ROUGE-1", quality.Answer.ROUGE1},
		{"ROUGE-2", quality.Answer.ROUGE2},
		{"ROUGE-L", quality.Answer.ROUGEL},
	}
	for _, metric := range answer {
		if metric.value == nil {
			return fmt.Errorf("unified benchmark result answer metric %s is missing", metric.name)
		}
	}
	return nil
}

func artifactModelFromSnapshot(m *types.EvaluationConfiguredModelSnapshot) artifactModel {
	if m == nil {
		return artifactModel{}
	}
	return artifactModel{Name: m.Name, Type: m.Type, Source: m.Source, Provider: m.Provider}
}

func writeFinalArtifacts(dir string, artifact finalArtifact) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create final artifact directory: %w", err)
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode final artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write final result.json: %w", err)
	}
	md := renderFinalMarkdown(artifact)
	if err := os.WriteFile(filepath.Join(dir, "result.md"), []byte(md), 0o644); err != nil {
		return fmt.Errorf("write final result.md: %w", err)
	}
	return nil
}

func renderFinalMarkdown(a finalArtifact) string {
	metric := func(v *float64) string {
		if v == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.6f", *v)
	}
	var b strings.Builder
	if a.ExecutionMode == executionModeCustom {
		b.WriteString("# Benchmark v1.1 Custom Verification Result\n\n")
		b.WriteString("WARNING: This run uses environment-specific models. " +
			"It verifies that the Benchmark pipeline is operational, but its quality metrics are NOT directly " +
			"comparable with the published Final Benchmark baseline.\n\n")
	} else {
		b.WriteString("# Benchmark v1.1 Final Result\n\n")
	}
	writeBuilder(
		&b,
		"Execution mode: %s  \nComparable to published baseline: %s  \nCommit: `%s`  \n"+
			"Evaluation run: `%s`  \nGenerated (UTC): `%s`  \nDataset SHA: `%s`\n\n",
		strings.ToUpper(string(a.ExecutionMode)), yesNo(a.ComparableToFinalBaseline), a.CommitSHA,
		a.EvaluationRunID, a.GeneratedAt.Format(time.RFC3339), a.Dataset.SemanticSHA256,
	)
	writeBuilder(
		&b,
		"Models: embedding `%s` (%s), chat `%s` (%s), summary `%s` (%s), rerank `%s` (%s)\n\n",
		a.Models["embedding"].Name, valueOrNA(a.Models["embedding"].ResolvedProvider),
		a.Models["chat"].Name, valueOrNA(a.Models["chat"].ResolvedProvider),
		a.Models["summary"].Name, valueOrNA(a.Models["summary"].ResolvedProvider),
		a.Models["rerank"].Name, valueOrNA(a.Models["rerank"].ResolvedProvider),
	)
	b.WriteString("## Quality\n\n| Metric | Value |\n| --- | ---: |\n")
	if a.Metrics.Retrieval != nil {
		r := a.Metrics.Retrieval
		writeBuilder(
			&b,
			"| Precision | %s |\n| Recall | %s |\n| NDCG@3 | %s |\n| NDCG@10 | %s |\n"+
				"| MRR | %s |\n| MAP | %s |\n",
			metric(r.Precision), metric(r.Recall), metric(r.NDCG3),
			metric(r.NDCG10), metric(r.MRR), metric(r.MAP),
		)
	}
	if a.Metrics.Answer != nil {
		q := a.Metrics.Answer
		writeBuilder(
			&b,
			"| BLEU-1 | %s |\n| BLEU-2 | %s |\n| BLEU-4 | %s |\n| ROUGE-1 | %s |\n"+
				"| ROUGE-2 | %s |\n| ROUGE-L | %s |\n",
			metric(q.BLEU1), metric(q.BLEU2), metric(q.BLEU4),
			metric(q.ROUGE1), metric(q.ROUGE2), metric(q.ROUGEL),
		)
	}
	b.WriteString("\n## Runtime / Usage\n\n")
	writeBuilder(&b, "- Worker limit: %d\n- Cache mode: %s\n", a.Runtime.WorkerLimit, a.Runtime.CacheMode)
	if a.Usage != nil {
		writeBuilder(
			&b, "- Model calls: %d\n- Average model latency: %s\n",
			a.Usage.Calls.Total, formatLatency(a.Usage.Latency.AverageMS),
		)
	}
	b.WriteString("\nRetrieval metrics are generally more stable. BLEU/ROUGE may vary slightly " +
		"because hosted model behavior is not bit-for-bit deterministic.\n")
	return b.String()
}

func writeBuilder(b *strings.Builder, format string, args ...any) {
	_, _ = fmt.Fprintf(b, format, args...)
}

func formatLatency(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f ms", *v)
}

func yesNo(value bool) string {
	if value {
		return "YES"
	}
	return "NO"
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func valueOrNA(value *string) string {
	if value == nil || *value == "" {
		return "n/a"
	}
	return *value
}
