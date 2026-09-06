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
	if strings.TrimSpace(env.CommitSHA) == "" {
		return selectedModels{}, errors.New("Git commit unreadable")
	}
	want, got := p.Dataset, env.Dataset
	if got.DatasetID != p.BenchmarkID {
		return selectedModels{}, fmt.Errorf("Benchmark dataset mismatch: expected %s, actual %s", p.BenchmarkID, got.DatasetID)
	}
	if got.DatasetSemanticSHA256 != want.SemanticSHA256 {
		return selectedModels{}, fmt.Errorf("Benchmark dataset semantic SHA mismatch: expected %s, actual %s", want.SemanticSHA256, got.DatasetSemanticSHA256)
	}
	if got.CorpusCount != want.Corpus || got.QuestionCount != want.Questions || got.QrelsCount != want.Qrels || got.AnswerCount != want.Answers {
		return selectedModels{}, fmt.Errorf("Benchmark dataset count mismatch: expected corpus/questions/qrels/answers %d/%d/%d/%d, actual %d/%d/%d/%d", want.Corpus, want.Questions, want.Qrels, want.Answers, got.CorpusCount, got.QuestionCount, got.QrelsCount, got.AnswerCount)
	}
	if env.CacheEnabled || !strings.EqualFold(p.Runtime.CacheMode, "off") {
		return selectedModels{}, errors.New("Embedding cache must be OFF for the final benchmark profile")
	}
	if env.WorkerLimit != p.Runtime.WorkerLimit {
		return selectedModels{}, fmt.Errorf("Worker limit mismatch: expected %d, actual %d (set GOMAXPROCS=%d)", p.Runtime.WorkerLimit, env.WorkerLimit, p.Runtime.WorkerLimit+1)
	}
	if env.Config == nil || env.Config.Conversation == nil || env.Config.Conversation.Summary == nil {
		return selectedModels{}, errors.New("Benchmark runtime conversation configuration unavailable")
	}
	if err := validateRuntime(p.Runtime, env); err != nil {
		return selectedModels{}, err
	}

	selected := selectedModels{}
	checks := []struct {
		label string
		want  profileModel
		id    *string
	}{{"embedding", p.Models.Embedding, &selected.EmbeddingID}, {"chat", p.Models.Chat, &selected.ChatID}, {"rerank", p.Models.Rerank, &selected.RerankID}}
	for _, check := range checks {
		matches := make([]*types.Model, 0, 1)
		actual := make([]string, 0)
		for _, model := range env.Models {
			if model == nil || string(model.Type) != check.want.Type || model.Status != types.ModelStatusActive {
				continue
			}
			actual = append(actual, model.Name)
			if model.Name == check.want.Name && string(model.Source) == check.want.Source && model.Parameters.Provider == check.want.Provider {
				matches = append(matches, model)
			}
		}
		sort.Strings(actual)
		// EvaluationService creates its temporary KB from the visible model list.
		// Requiring one active candidate per role makes that existing selection
		// deterministic without changing Evaluation semantics or freezing UUIDs.
		if len(matches) != 1 || len(actual) != 1 {
			return selectedModels{}, fmt.Errorf("Configured %s model does not match final benchmark profile. Expected: %s (%s/%s). Actual: %s. This run cannot be compared with the published final baseline", check.label, check.want.Name, check.want.Source, check.want.Provider, strings.Join(actual, ", "))
		}
		model := matches[0]
		if check.want.Dimension != 0 && model.Parameters.EmbeddingParameters.Dimension != check.want.Dimension {
			return selectedModels{}, fmt.Errorf("Configured %s model dimension mismatch: expected %d, actual %d", check.label, check.want.Dimension, model.Parameters.EmbeddingParameters.Dimension)
		}
		if model.Parameters.APIKey == "" && model.Parameters.AppSecret == "" {
			return selectedModels{}, fmt.Errorf("Required credential unavailable for %s model %s", check.label, check.want.Name)
		}
		*check.id = model.ID
	}
	return selected, nil
}

func validateRuntime(want profileRuntime, env preflightEnvironment) error {
	c := env.Config.Conversation
	gotRetrieval := profileRetrieval{c.VectorThreshold, c.KeywordThreshold, c.EmbeddingTopK, c.RerankTopK, c.RerankThreshold, env.RetrieveDriver}
	if gotRetrieval != want.Retrieval {
		return fmt.Errorf("Retrieval configuration does not match final benchmark profile: expected %+v, actual %+v", want.Retrieval, gotRetrieval)
	}
	s := c.Summary
	gotGeneration := profileGeneration{c.MaxRounds, s.MaxInputChars, s.MaxTokens, s.RepeatPenalty, s.TopK, s.TopP, s.FrequencyPenalty, s.PresencePenalty, s.Temperature, s.Seed, s.MaxCompletionTokens}
	if gotGeneration != want.Generation {
		return fmt.Errorf("Generation configuration does not match final benchmark profile: expected %+v, actual %+v", want.Generation, gotGeneration)
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
		return fmt.Errorf("Backend unavailable: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Backend unavailable at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Backend unavailable at %s: health returned HTTP %d", baseURL, resp.StatusCode)
	}
	return nil
}

func checkOutputWritable(path string) error {
	parent := filepath.Dir(path)
	for {
		info, err := os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("Output path cannot be written: %s is not a directory", parent)
			}
			f, err := os.CreateTemp(parent, ".benchmark-write-check-*")
			if err != nil {
				return fmt.Errorf("Output path cannot be written: %w", err)
			}
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("Output path cannot be written: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return errors.New("Output path cannot be written: no existing parent directory")
		}
		parent = next
	}
}

func loadDatasetIdentity(ctx context.Context, id string) (types.EvaluationDatasetIdentity, error) {
	dataset, err := service.NewDatasetService().GetDatasetByID(ctx, id)
	if err != nil {
		return types.EvaluationDatasetIdentity{}, fmt.Errorf("Benchmark dataset missing or invalid: %w", err)
	}
	return dataset.Identity, nil
}

func loadConfiguredModels(ctx context.Context, tenant uint64) ([]*types.Model, error) {
	driver := os.Getenv("DB_DRIVER")
	var dialector gorm.Dialector
	switch driver {
	case "postgres":
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=5", os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))
		dialector = postgres.Open(dsn)
	case "sqlite":
		path := os.Getenv("DB_PATH")
		if path == "" {
			path = "./data/weknora.db"
		}
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("PostgreSQL unavailable (configured sqlite database missing): %w", err)
		}
		dialector = sqlite.Open(path + "?mode=ro")
	default:
		return nil, fmt.Errorf("PostgreSQL unavailable: unsupported DB_DRIVER %q", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL unavailable: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL unavailable: %w", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("PostgreSQL unavailable: %w", err)
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
		return preflightEnvironment{}, fmt.Errorf("Git commit unreadable: %w", err)
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
		return preflightEnvironment{}, fmt.Errorf("Embedding cache mode cannot be confirmed: %v", cacheWarnings[0])
	}
	return preflightEnvironment{
		CommitSHA: sha, Dataset: identity, Models: models, Config: cfg,
		RetrieveDriver: os.Getenv("RETRIEVE_DRIVER"),
		WorkerLimit:    max(runtime.GOMAXPROCS(0)-1, 1),
		CacheEnabled:   cacheConfig.Enabled,
	}, nil
}

type artifactModel struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Source            string `json:"source"`
	Provider          string `json:"provider"`
	ResolvedProvider  string `json:"resolved_provider,omitempty"`
	ResolvedModelName string `json:"resolved_model_name,omitempty"`
}

type finalArtifact struct {
	SchemaVersion    int                                  `json:"schema_version"`
	BenchmarkID      string                               `json:"benchmark_id"`
	BenchmarkVersion string                               `json:"benchmark_version"`
	CommitSHA        string                               `json:"commit_sha"`
	GeneratedAt      time.Time                            `json:"generated_at"`
	Dataset          profileDataset                       `json:"dataset"`
	Models           map[string]artifactModel             `json:"models"`
	Runtime          profileRuntime                       `json:"runtime"`
	EvaluationRunID  string                               `json:"evaluation_run_id"`
	Metrics          types.BenchmarkQuality               `json:"metrics"`
	Usage            *types.EvaluationModelUsageAggregate `json:"usage"`
	Latency          artifactLatency                      `json:"latency"`
	Reproducibility  types.BenchmarkReproducibilityState  `json:"reproducibility"`
}

type artifactLatency struct {
	RunWallClockDurationMS *int64                 `json:"run_wall_clock_duration_ms"`
	ModelCalls             types.LatencyAggregate `json:"model_calls"`
}

func buildFinalArtifact(p finalProfile, commit string, now time.Time, result *types.BenchmarkResult) (finalArtifact, error) {
	if result == nil || result.Run.EvaluationRunID == "" {
		return finalArtifact{}, errors.New("cannot build final artifact from empty unified benchmark result")
	}
	if err := validateResultProfile(p, result); err != nil {
		return finalArtifact{}, err
	}
	models := map[string]artifactModel{
		"embedding": artifactModelFromSnapshot(result.Config.Models.Embedding),
		"chat":      artifactModelFromSnapshot(result.Config.Models.Chat),
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
			m.ResolvedProvider = observed.ResolvedProvider
			if observed.ResolvedModelName != nil {
				m.ResolvedModelName = *observed.ResolvedModelName
			}
			models[key] = m
		}
	}
	return finalArtifact{1, p.BenchmarkID, p.BenchmarkVersion, commit, now.UTC(), p.Dataset, models, p.Runtime, result.Run.EvaluationRunID, result.Quality, result.ModelFacts, artifactLatency{result.RunWallClockDurationMS, latency}, result.Reproducibility}, nil
}

func validateResultProfile(p finalProfile, result *types.BenchmarkResult) error {
	if result.BenchmarkVersion != p.BenchmarkVersion {
		return fmt.Errorf("unified benchmark result version mismatch: expected %s, actual %s", p.BenchmarkVersion, result.BenchmarkVersion)
	}
	d := result.Config.Dataset
	if d.DatasetID != p.BenchmarkID || d.DatasetSemanticSHA256 != p.Dataset.SemanticSHA256 || d.CorpusCount != p.Dataset.Corpus || d.QuestionCount != p.Dataset.Questions || d.QrelsCount != p.Dataset.Qrels || d.AnswerCount != p.Dataset.Answers {
		return errors.New("unified benchmark result dataset does not match final benchmark profile")
	}
	checks := []struct {
		label string
		want  profileModel
		got   *types.EvaluationConfiguredModelSnapshot
	}{{"embedding", p.Models.Embedding, result.Config.Models.Embedding}, {"chat", p.Models.Chat, result.Config.Models.Chat}, {"rerank", p.Models.Rerank, result.Config.Models.Rerank}}
	for _, check := range checks {
		if check.got == nil || check.got.Name != check.want.Name || check.got.Type != check.want.Type || check.got.Source != check.want.Source || check.got.Provider != check.want.Provider {
			return fmt.Errorf("unified benchmark result %s model does not match final benchmark profile", check.label)
		}
		if check.want.Dimension != 0 && (check.got.Embedding == nil || check.got.Embedding.Dimension != check.want.Dimension) {
			return fmt.Errorf("unified benchmark result %s dimension does not match final benchmark profile", check.label)
		}
	}
	if result.Config.Execution.WorkerLimit != p.Runtime.WorkerLimit {
		return errors.New("unified benchmark result worker limit does not match final benchmark profile")
	}
	r := result.Config.Retrieval
	if (profileRetrieval{r.VectorThreshold, r.KeywordThreshold, r.EmbeddingTopK, r.RerankTopK, r.RerankThreshold, r.RetrieveDriver}) != p.Runtime.Retrieval {
		return errors.New("unified benchmark result retrieval settings do not match final benchmark profile")
	}
	g := result.Config.Generation
	s := g.SummaryConfig
	// MaxInputChars is validated from config during preflight; the frozen v1.1
	// EvaluationGenerationSnapshot predates that field and cannot re-state it.
	if (profileGeneration{g.MaxRounds, p.Runtime.Generation.MaxInputChars, s.MaxTokens, s.RepeatPenalty, s.TopK, s.TopP, s.FrequencyPenalty, s.PresencePenalty, s.Temperature, s.Seed, s.MaxCompletionTokens}) != p.Runtime.Generation {
		return errors.New("unified benchmark result generation settings do not match final benchmark profile")
	}
	if result.ModelFacts == nil {
		return errors.New("unified benchmark result has no model usage facts")
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
	fmt.Fprintf(&b, "# Benchmark v1.1 Final Result\n\nCommit: `%s`  \nEvaluation run: `%s`  \nGenerated (UTC): `%s`  \nDataset SHA: `%s`\n\n", a.CommitSHA, a.EvaluationRunID, a.GeneratedAt.Format(time.RFC3339), a.Dataset.SemanticSHA256)
	fmt.Fprintf(&b, "Models: embedding `%s` (%s), chat `%s` (%s), rerank `%s` (%s)\n\n", a.Models["embedding"].Name, a.Models["embedding"].ResolvedProvider, a.Models["chat"].Name, a.Models["chat"].ResolvedProvider, a.Models["rerank"].Name, a.Models["rerank"].ResolvedProvider)
	b.WriteString("## Quality\n\n| Metric | Value |\n| --- | ---: |\n")
	if a.Metrics.Retrieval != nil {
		r := a.Metrics.Retrieval
		fmt.Fprintf(&b, "| Precision | %s |\n| Recall | %s |\n| NDCG@3 | %s |\n| NDCG@10 | %s |\n| MRR | %s |\n| MAP | %s |\n", metric(r.Precision), metric(r.Recall), metric(r.NDCG3), metric(r.NDCG10), metric(r.MRR), metric(r.MAP))
	}
	if a.Metrics.Answer != nil {
		q := a.Metrics.Answer
		fmt.Fprintf(&b, "| BLEU-1 | %s |\n| BLEU-2 | %s |\n| BLEU-4 | %s |\n| ROUGE-1 | %s |\n| ROUGE-2 | %s |\n| ROUGE-L | %s |\n", metric(q.BLEU1), metric(q.BLEU2), metric(q.BLEU4), metric(q.ROUGE1), metric(q.ROUGE2), metric(q.ROUGEL))
	}
	b.WriteString("\n## Runtime / Usage\n\n")
	fmt.Fprintf(&b, "- Worker limit: %d\n- Cache mode: %s\n", a.Runtime.WorkerLimit, a.Runtime.CacheMode)
	if a.Usage != nil {
		fmt.Fprintf(&b, "- Model calls: %d\n- Average model latency: %.2f ms\n", a.Usage.Calls.Total, valueOrZero(a.Usage.Latency.AverageMS))
	}
	b.WriteString("\nRetrieval metrics are generally more stable. BLEU/ROUGE may vary slightly because hosted model behavior is not bit-for-bit deterministic.\n")
	return b.String()
}

func valueOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
