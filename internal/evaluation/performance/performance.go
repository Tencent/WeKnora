// Package performance runs the isolated keyless evaluation performance workload.
package performance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/evaluation/reproduce"
	"github.com/Tencent/WeKnora/internal/modelcache"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ReportSchemaVersion identifies the machine-readable performance report contract.
const ReportSchemaVersion = 1

// Options defines the deterministic benchmark matrix and isolated output location.
type Options struct {
	DatasetPath   string
	Commit        string
	OutputRoot    string
	Sizes         []int
	Concurrencies []int
	Repetitions   int
	Seed          int64
}

// DefaultOptions returns the acceptance matrix of three sizes, three concurrency levels, and five repetitions.
func DefaultOptions() Options {
	return Options{
		DatasetPath: "dataset/golden/v1/dataset.json", Sizes: []int{10, 100, 1000},
		Concurrencies: []int{1, 4, 8}, Repetitions: 5, Seed: 42,
	}
}

// Report is the authoritative performance artifact.
type Report struct {
	SchemaVersion   int             `json:"schema_version"`
	Commit          string          `json:"commit"`
	Dataset         DatasetIdentity `json:"dataset"`
	Machine         Machine         `json:"machine"`
	Runtime         Runtime         `json:"runtime"`
	Database        Database        `json:"database"`
	Configuration   Configuration   `json:"configuration"`
	Results         []Result        `json:"results"`
	CacheValidation CacheValidation `json:"cache_validation"`
}

// DatasetIdentity pins the workload dataset and content hash.
type DatasetIdentity struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
}

// Machine records hardware facts that affect measurements.
type Machine struct {
	Hostname    string `json:"hostname"`
	CPU         string `json:"cpu"`
	LogicalCPUs int    `json:"logical_cpus"`
	MemoryBytes uint64 `json:"memory_bytes"`
}

// Runtime records Go and operating-system facts.
type Runtime struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Database records the isolated persistence engine and mode.
type Database struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
}

// Configuration records the complete benchmark matrix and pipeline.
type Configuration struct {
	Pipeline       []string `json:"pipeline"`
	Sizes          []int    `json:"sizes"`
	Concurrencies  []int    `json:"concurrencies"`
	CacheStates    []string `json:"cache_states"`
	Repetitions    int      `json:"repetitions"`
	Seed           int64    `json:"seed"`
	Network        string   `json:"network"`
	ExternalKeys   string   `json:"external_keys"`
	RegressionGate string   `json:"regression_gate"`
}

// Distribution summarizes repeated or per-sample measurements.
type Distribution struct {
	Minimum float64 `json:"minimum"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Maximum float64 `json:"maximum"`
}

// Result summarizes one size, concurrency, and cache-state group.
type Result struct {
	Size                     int          `json:"size"`
	Concurrency              int          `json:"concurrency"`
	CacheState               string       `json:"cache_state"`
	Repetitions              int          `json:"repetitions"`
	WallMS                   Distribution `json:"wall_ms"`
	ThroughputItemsPerSecond Distribution `json:"throughput_items_per_second"`
	SampleLatencyMS          Distribution `json:"sample_latency_ms"`
	FailureRate              float64      `json:"failure_rate"`
	PromptTokens             int64        `json:"prompt_tokens"`
	CompletionTokens         int64        `json:"completion_tokens"`
	TotalTokens              int64        `json:"total_tokens"`
	CostMicroUSD             int64        `json:"cost_micro_usd"`
	ProviderCalls            int64        `json:"provider_calls"`
	ProviderInputItems       int64        `json:"provider_input_items"`
	CacheHitRatio            float64      `json:"cache_hit_ratio"`
	AllocationBytes          Distribution `json:"allocation_bytes"`
	PeakRSSBytes             uint64       `json:"peak_rss_bytes"`
	PeakGoroutines           int64        `json:"peak_goroutines"`
	DatabaseBytes            Distribution `json:"database_bytes"`
	PersistedQuestions       int64        `json:"persisted_questions"`
	ListedQuestions          int64        `json:"listed_questions"`
	ExportBytes              int64        `json:"export_bytes"`
	ComparisonMetricCount    int          `json:"comparison_metric_count"`
}

// CacheValidation contains independently checked embedding and Wiki cache facts.
type CacheValidation struct {
	Embedding EmbeddingCacheValidation `json:"embedding"`
	Wiki      WikiCacheValidation      `json:"wiki_provider"`
}

// EmbeddingCacheValidation compares provider inputs for two identical corpus builds.
type EmbeddingCacheValidation struct {
	Status                   string  `json:"status"`
	CorpusItems              int64   `json:"corpus_items"`
	FirstProviderInputItems  int64   `json:"first_provider_input_items"`
	SecondProviderInputItems int64   `json:"second_provider_input_items"`
	ProviderCallReduction    float64 `json:"provider_call_reduction"`
}

// WikiCacheValidation records whether live provider cache telemetry was measured.
type WikiCacheValidation struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type measurement struct {
	wallMS, throughput, allocationBytes, databaseBytes float64
	latencies                                          []float64
	failures, prompt, completion, providerCalls        int64
	providerInputs, requestedEmbeddings                int64
	peakRSS                                            uint64
	peakGoroutines, persisted, listed, exportBytes     int64
	comparisonMetricCount                              int
}

// Build executes the complete isolated benchmark matrix.
func Build(ctx context.Context, options Options) (*Report, error) {
	if options.DatasetPath == "" || options.Commit == "" || options.OutputRoot == "" {
		return nil, errors.New("dataset path, commit, and isolated output root are required")
	}
	if len(options.Sizes) == 0 || len(options.Concurrencies) == 0 || options.Repetitions < 1 {
		return nil, errors.New("non-empty matrix and positive repetitions are required")
	}
	raw, err := os.ReadFile(options.DatasetPath)
	if err != nil {
		return nil, fmt.Errorf("read performance dataset: %w", err)
	}
	var dataset reproduce.Dataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return nil, fmt.Errorf("decode performance dataset: %w", err)
	}
	if len(dataset.Passages) == 0 || len(dataset.Samples) == 0 {
		return nil, errors.New("performance dataset requires passages and samples")
	}
	contentHash := datasetContentHash(&dataset)
	databaseVersion, err := sqliteVersion(options.OutputRoot)
	if err != nil {
		return nil, err
	}
	report := &Report{
		SchemaVersion: ReportSchemaVersion, Commit: options.Commit,
		Dataset: DatasetIdentity{
			ID: dataset.DatasetID, Version: dataset.SchemaVersion, ContentSHA256: contentHash,
		},
		Machine: machineFacts(),
		Runtime: Runtime{
			GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		},
		Database: Database{
			Engine: "SQLite", Version: databaseVersion,
			Mode: "isolated temporary WAL database per repetition",
		},
		Configuration: Configuration{
			Pipeline: []string{
				"data_load", "embedding_index", "query", "rerank", "fake_generation",
				"metric_calculation", "question_persistence", "question_list", "comparison", "export",
			},
			Sizes:         append([]int(nil), options.Sizes...),
			Concurrencies: append([]int(nil), options.Concurrencies...),
			CacheStates:   []string{"cold", "hot"}, Repetitions: options.Repetitions, Seed: options.Seed,
			Network: "disabled", ExternalKeys: "unused",
			RegressionGate: "informational; no single wall-clock threshold blocks pull requests",
		},
	}
	for _, size := range options.Sizes {
		if size < 1 {
			return nil, fmt.Errorf("invalid workload size %d", size)
		}
		for _, concurrency := range options.Concurrencies {
			if concurrency < 1 {
				return nil, fmt.Errorf("invalid concurrency %d", concurrency)
			}
			for _, cacheState := range []string{"cold", "hot"} {
				measurements := make([]measurement, 0, options.Repetitions)
				for repetition := 0; repetition < options.Repetitions; repetition++ {
					item, err := runCase(
						ctx, options.OutputRoot, &dataset, size, concurrency, cacheState,
					)
					if err != nil {
						return nil, fmt.Errorf(
							"run size=%d concurrency=%d cache=%s repetition=%d: %w",
							size, concurrency, cacheState, repetition+1, err,
						)
					}
					measurements = append(measurements, item)
				}
				report.Results = append(report.Results, summarize(size, concurrency, cacheState, measurements))
			}
		}
	}
	embeddingValidation, err := verifyEmbeddingSecondCorpus(ctx, options.OutputRoot, &dataset)
	if err != nil {
		return nil, err
	}
	report.CacheValidation = CacheValidation{Embedding: embeddingValidation, Wiki: verifyWikiAccounting()}
	return report, nil
}

func runCase(
	ctx context.Context,
	root string,
	dataset *reproduce.Dataset,
	size, concurrency int,
	cacheState string,
) (measurement, error) {
	caseRoot, err := os.MkdirTemp(root, "evaluation-performance-")
	if err != nil {
		return measurement{}, err
	}
	defer func() { _ = os.RemoveAll(caseRoot) }()
	databasePath := filepath.Join(caseRoot, "evaluation.db")
	db, err := openDatabase(databasePath)
	if err != nil {
		return measurement{}, err
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()

	provider := &countingEmbedder{}
	model := benchmarkModel()
	cached := modelcache.NewCoordinator(repository.NewEmbeddingCacheRepository(db)).Wrap(model, provider)
	benchmarkCtx := context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	corpus := expandedPassages(dataset, size)
	queries := expandedQuestions(dataset, size)
	if cacheState == "hot" {
		if _, err := cached.BatchEmbed(benchmarkCtx, corpus); err != nil {
			return measurement{}, err
		}
		if _, err := cached.BatchEmbed(benchmarkCtx, uniqueStrings(queries)); err != nil {
			return measurement{}, err
		}
		provider.reset()
	}

	registry, err := metricregistry.NewDefaultRegistry()
	if err != nil {
		return measurement{}, err
	}
	plan, err := registry.Resolve(metricregistry.DefaultSpecs())
	if err != nil {
		return measurement{}, err
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	if _, err := cached.BatchEmbed(benchmarkCtx, corpus); err != nil {
		return measurement{}, err
	}
	requestedEmbeddings := int64(len(corpus) + len(queries))

	jobs := make(chan int)
	latencies := make([]float64, size)
	metricRows := make([]*types.MetricResult, size)
	var failures, promptTokens, completionTokens atomic.Int64
	var persisted atomic.Int64
	var peakGoroutines atomic.Int64
	var persistMu sync.Mutex
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer workers.Done()
			updateMaximum(&peakGoroutines, int64(runtime.NumGoroutine()))
			for index := range jobs {
				sampleStarted := time.Now()
				if _, embedErr := cached.Embed(benchmarkCtx, queries[index]); embedErr != nil {
					failures.Add(1)
					continue
				}
				retrieved := []int{index % size, (index + 1) % size, (index + 2) % size}
				sort.Ints(retrieved[1:])
				generated := dataset.Samples[index%len(dataset.Samples)].GeneratedText
				metric, _, metricErr := plan.Compute(benchmarkCtx, &types.MetricInput{
					RetrievalGT: [][]int{{index % size}}, RetrievalGrades: map[int]int{index % size: 1},
					RetrievalLabelsAvailable: true, RetrievalIDs: retrieved,
					GeneratedTexts: generated, GeneratedGT: generated,
				})
				if metricErr != nil {
					failures.Add(1)
					continue
				}
				metricRows[index] = metric
				prompt := len(strings.Fields(queries[index])) + len(retrieved)
				completion := len(strings.Fields(generated))
				promptTokens.Add(int64(prompt))
				completionTokens.Add(int64(completion))
				elapsed := time.Since(sampleStarted)
				latencies[index] = durationMS(elapsed)
				totalMS := elapsed.Milliseconds()
				row := questionRow(index, queries[index], generated, metric, totalMS, prompt, completion)
				persistMu.Lock()
				persistErr := db.WithContext(benchmarkCtx).Create(row).Error
				persistMu.Unlock()
				if persistErr != nil {
					failures.Add(1)
					continue
				}
				persisted.Add(1)
			}
		}()
	}
	for index := 0; index < size; index++ {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	wall := time.Since(started)
	var rows []*types.EvaluationQuestionResultEntity
	if err := db.WithContext(benchmarkCtx).Order("sample_index ASC").Find(&rows).Error; err != nil {
		return measurement{}, err
	}
	aggregate := plan.Aggregate(metricRows)
	comparison := compareMetricRows(aggregate, aggregate)
	exported, err := json.Marshal(struct {
		Metrics   *types.MetricResult                     `json:"metrics"`
		Questions []*types.EvaluationQuestionResultEntity `json:"questions"`
	}{Metrics: aggregate, Questions: rows})
	if err != nil {
		return measurement{}, err
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	info, err := os.Stat(databasePath)
	if err != nil {
		return measurement{}, err
	}
	calls, inputs := provider.counts()
	return measurement{
		wallMS: durationMS(wall), throughput: float64(size) / wall.Seconds(), latencies: latencies,
		allocationBytes: float64(after.TotalAlloc - before.TotalAlloc), databaseBytes: float64(info.Size()),
		failures: failures.Load(), prompt: promptTokens.Load(), completion: completionTokens.Load(),
		providerCalls: calls, providerInputs: inputs, requestedEmbeddings: requestedEmbeddings,
		peakRSS: peakRSSBytes(), peakGoroutines: peakGoroutines.Load(), persisted: persisted.Load(),
		listed: int64(len(rows)), exportBytes: int64(len(exported)), comparisonMetricCount: comparison,
	}, nil
}

func questionRow(
	index int,
	question, generated string,
	metric *types.MetricResult,
	totalMS int64,
	prompt, completion int,
) *types.EvaluationQuestionResultEntity {
	encodedMetric, _ := json.Marshal(metric)
	totalTokens := prompt + completion
	now := time.Now().UTC()
	return &types.EvaluationQuestionResultEntity{
		TenantID: 1, TaskID: "performance", SampleIndex: index, QID: fmt.Sprintf("q-%d", index),
		Question: question, ReferenceAnswer: generated, GroundTruthPIDs: types.JSON(`[0]`),
		SearchResults: types.JSON(`[]`), RerankResults: types.JSON(`[]`), GenerationPIDs: types.JSON(`[0]`),
		GeneratedText: generated, PerSampleMetrics: types.JSON(encodedMetric), MetricObservations: types.JSON(`[]`),
		TotalMs: &totalMS, PromptTokens: &prompt, CompletionTokens: &completion, TotalTokens: &totalTokens,
		UsageReported: true, Status: types.EvaluationQuestionStatusSuccess,
		ResultHash: types.EvaluationQuestionResultHash(&types.EvaluationQuestionResultInput{
			SampleIndex: index, QID: fmt.Sprintf("q-%d", index), Question: question,
			GeneratedText: generated, Status: types.EvaluationQuestionStatusSuccess,
		}),
		CreatedAt: now, UpdatedAt: now,
	}
}

func compareMetricRows(left, right *types.MetricResult) int {
	if left == nil || right == nil {
		return 0
	}
	count := 0
	for key, leftScore := range left.Scores {
		if rightScore, exists := right.Scores[key]; exists && leftScore.Value != nil && rightScore.Value != nil {
			_ = *rightScore.Value - *leftScore.Value
			count++
		}
	}
	return count
}

func summarize(size, concurrency int, cacheState string, values []measurement) Result {
	walls := make([]float64, 0, len(values))
	throughputs := make([]float64, 0, len(values))
	allocations := make([]float64, 0, len(values))
	databases := make([]float64, 0, len(values))
	latencies := make([]float64, 0, size*len(values))
	result := Result{Size: size, Concurrency: concurrency, CacheState: cacheState, Repetitions: len(values)}
	var requested int64
	for _, value := range values {
		walls, throughputs = append(walls, value.wallMS), append(throughputs, value.throughput)
		allocations, databases = append(allocations, value.allocationBytes), append(databases, value.databaseBytes)
		latencies = append(latencies, value.latencies...)
		result.FailureRate += float64(value.failures) / float64(size)
		result.PromptTokens += value.prompt
		result.CompletionTokens += value.completion
		result.ProviderCalls += value.providerCalls
		result.ProviderInputItems += value.providerInputs
		requested += value.requestedEmbeddings
		result.PeakRSSBytes = max(result.PeakRSSBytes, value.peakRSS)
		result.PeakGoroutines = max(result.PeakGoroutines, value.peakGoroutines)
		result.PersistedQuestions += value.persisted
		result.ListedQuestions += value.listed
		result.ExportBytes += value.exportBytes
		result.ComparisonMetricCount = max(result.ComparisonMetricCount, value.comparisonMetricCount)
	}
	result.WallMS, result.ThroughputItemsPerSecond = distribution(walls), distribution(throughputs)
	result.SampleLatencyMS = distribution(latencies)
	result.AllocationBytes = distribution(allocations)
	result.DatabaseBytes = distribution(databases)
	result.FailureRate /= float64(len(values))
	result.TotalTokens = result.PromptTokens + result.CompletionTokens
	if requested > 0 {
		result.CacheHitRatio = float64(requested-result.ProviderInputItems) / float64(requested)
	}
	return result
}

func distribution(values []float64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return Distribution{
		Minimum: sorted[0], P50: percentile(sorted, 0.50), P95: percentile(sorted, 0.95),
		P99: percentile(sorted, 0.99), Maximum: sorted[len(sorted)-1],
	}
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}

func verifyEmbeddingSecondCorpus(
	ctx context.Context,
	root string,
	dataset *reproduce.Dataset,
) (EmbeddingCacheValidation, error) {
	caseRoot, err := os.MkdirTemp(root, "embedding-cache-validation-")
	if err != nil {
		return EmbeddingCacheValidation{}, err
	}
	defer func() { _ = os.RemoveAll(caseRoot) }()
	db, err := openDatabase(filepath.Join(caseRoot, "cache.db"))
	if err != nil {
		return EmbeddingCacheValidation{}, err
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()
	provider := &countingEmbedder{}
	cached := modelcache.NewCoordinator(repository.NewEmbeddingCacheRepository(db)).Wrap(benchmarkModel(), provider)
	benchmarkCtx := context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	corpus := expandedPassages(dataset, 10)
	if _, err := cached.BatchEmbed(benchmarkCtx, corpus); err != nil {
		return EmbeddingCacheValidation{}, err
	}
	_, firstInputs := provider.counts()
	provider.reset()
	if _, err := cached.BatchEmbed(benchmarkCtx, corpus); err != nil {
		return EmbeddingCacheValidation{}, err
	}
	_, secondInputs := provider.counts()
	status := "verified"
	if firstInputs != int64(len(corpus)) || secondInputs != 0 {
		status = "failed"
	}
	reduction := 0.0
	if firstInputs > 0 {
		reduction = float64(firstInputs-secondInputs) / float64(firstInputs)
	}
	return EmbeddingCacheValidation{
		Status: status, CorpusItems: int64(len(corpus)),
		FirstProviderInputItems: firstInputs, SecondProviderInputItems: secondInputs,
		ProviderCallReduction: reduction,
	}, nil
}

func verifyWikiAccounting() WikiCacheValidation {
	return WikiCacheValidation{
		Status: "not_measured",
		Reason: "the keyless benchmark does not call a live Wiki model provider",
	}
}

type countingEmbedder struct {
	mu         sync.Mutex
	calls      int64
	inputItems int64
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	rows, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.calls++
	e.inputItems += int64(len(texts))
	e.mu.Unlock()
	result := make([][]float32, len(texts))
	for index, text := range texts {
		value := float32((len(text)+index)%97) / 97
		result[index] = []float32{value, value + 0.1, value + 0.2, value + 0.3}
	}
	return result, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}
func (*countingEmbedder) GetModelName() string { return "deterministic-embedding" }
func (*countingEmbedder) GetDimensions() int   { return 4 }
func (*countingEmbedder) GetModelID() string   { return "performance-embedding" }
func (e *countingEmbedder) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls, e.inputItems = 0, 0
}

func (e *countingEmbedder) counts() (int64, int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls, e.inputItems
}

func benchmarkModel() *types.Model {
	return &types.Model{
		ID: "performance-embedding", TenantID: 1, Name: "deterministic-embedding",
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 4},
		},
	}
}

func openDatabase(path string) (*gorm.DB, error) {
	dsn := path + "?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(
		&types.EmbeddingCacheEntry{},
		&types.EmbeddingCacheLookupRecord{},
		&types.EvaluationQuestionResultEntity{},
	); err != nil {
		return nil, err
	}
	return db, nil
}

func sqliteVersion(root string) (string, error) {
	dir, err := os.MkdirTemp(root, "sqlite-version-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := openDatabase(filepath.Join(dir, "version.db"))
	if err != nil {
		return "", err
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()
	var version string
	if err := db.Raw("SELECT sqlite_version()").Scan(&version).Error; err != nil {
		return "", err
	}
	return version, nil
}

func datasetContentHash(dataset *reproduce.Dataset) string {
	input := &types.EvaluationDatasetVersionInput{}
	for _, passage := range dataset.Passages {
		input.Passages = append(input.Passages, types.EvaluationDatasetPassageInput{
			PID: passage.PID, Content: passage.Content,
		})
	}
	for _, question := range dataset.Questions {
		input.Questions = append(input.Questions, types.EvaluationDatasetQuestionInput{
			QID: question.QID, Question: question.Question, Answer: question.Answer,
		})
	}
	for _, edge := range dataset.Relevance {
		input.Relevance = append(input.Relevance, types.EvaluationDatasetRelevanceInput{
			QID: edge.QID, PID: edge.PID, Grade: edge.Grade,
		})
	}
	return types.CanonicalEvaluationDatasetContentSHA256(input)
}

func expandedPassages(dataset *reproduce.Dataset, size int) []string {
	values := make([]string, size)
	for index := range values {
		passage := dataset.Passages[index%len(dataset.Passages)]
		values[index] = passage.Content + "\nbenchmark-index=" + strconv.Itoa(index)
	}
	return values
}

func expandedQuestions(dataset *reproduce.Dataset, size int) []string {
	values := make([]string, size)
	for index := range values {
		question := dataset.Questions[index%len(dataset.Questions)]
		values[index] = question.Question
	}
	return values
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func updateMaximum(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current; current = target.Load() {
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

func durationMS(value time.Duration) float64 { return float64(value.Nanoseconds()) / 1e6 }

func machineFacts() Machine {
	hostname, _ := os.Hostname()
	return Machine{Hostname: hostname, CPU: cpuName(), LogicalCPUs: runtime.NumCPU(), MemoryBytes: memoryBytes()}
}

func cpuName() string {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unavailable"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 && strings.TrimSpace(parts[0]) == "model name" {
			return strings.TrimSpace(parts[1])
		}
	}
	return "unavailable"
}

func memoryBytes() uint64 {
	return procValueBytes("/proc/meminfo", "MemTotal")
}

func peakRSSBytes() uint64 {
	return procValueBytes("/proc/self/status", "VmHWM")
}

func procValueBytes(path, key string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.TrimSuffix(parts[0], ":") == key {
			value, _ := strconv.ParseUint(parts[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}

// JSON encodes the authoritative report with stable indentation.
func JSON(report *Report) ([]byte, error) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// Markdown renders a human-readable view from an in-memory authoritative report.
func Markdown(report *Report) []byte {
	var output strings.Builder
	fmt.Fprintf(&output, "# Evaluation performance report\n\n")
	fmt.Fprintf(
		&output,
		"The isolated keyless pipeline used dataset `%s` version %d at commit `%s`. "+
			"It ran on %s/%s with %s and SQLite %s.\n\n",
		report.Dataset.ID, report.Dataset.Version, report.Commit,
		report.Runtime.GOOS, report.Runtime.GOARCH, report.Runtime.GoVersion, report.Database.Version,
	)
	fmt.Fprintf(
		&output,
		"The table reports median wall time and throughput across %d repetitions. "+
			"Sample latency columns use all question observations from those repetitions.\n\n",
		report.Configuration.Repetitions,
	)
	fmt.Fprintf(
		&output,
		"| Size | Concurrency | Cache | Wall p50 (ms) | Throughput p50 (items/s) | "+
			"Latency p50/p95/p99 (ms) | Failures | Cache hit | Provider inputs |\n",
	)
	fmt.Fprintf(&output, "| ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, result := range report.Results {
		fmt.Fprintf(
			&output,
			"| %d | %d | %s | %.3f | %.2f | %.3f / %.3f / %.3f | %.4f | %.2f%% | %d |\n",
			result.Size, result.Concurrency, result.CacheState, result.WallMS.P50,
			result.ThroughputItemsPerSecond.P50, result.SampleLatencyMS.P50,
			result.SampleLatencyMS.P95, result.SampleLatencyMS.P99, result.FailureRate,
			result.CacheHitRatio*100, result.ProviderInputItems,
		)
	}
	fmt.Fprintf(
		&output,
		"\nThe embedding cache verification sent %d items to the provider for the first corpus "+
			"and %d for the identical second corpus, a %.2f%% reduction. Its status is `%s`.\n\n",
		report.CacheValidation.Embedding.FirstProviderInputItems,
		report.CacheValidation.Embedding.SecondProviderInputItems,
		report.CacheValidation.Embedding.ProviderCallReduction*100,
		report.CacheValidation.Embedding.Status,
	)
	fmt.Fprintf(
		&output,
		"Live Wiki provider cache telemetry is `%s`: %s.\n",
		report.CacheValidation.Wiki.Status, report.CacheValidation.Wiki.Reason,
	)
	return []byte(output.String())
}
