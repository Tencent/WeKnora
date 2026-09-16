package livecache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/modelcache"
	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/models/call"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// Options carries explicit execution approval and a fresh artifact directory.
type Options struct {
	Execute           bool
	ConfirmPlanSHA256 string
	ApprovedCNY       string
	OutputDir         string
	// Optional separate embedding key, read only after execution approval.
	EmbeddingCredential func() (string, error)
}

// StepResult records one step's physical calls, usage, accounting and output evidence.
type StepResult struct {
	ID                   string `json:"id"`
	Operation            string `json:"operation"`
	Arm                  string `json:"arm"`
	Round                int    `json:"round"`
	Pair                 int    `json:"pair"`
	DurationMs           int64  `json:"duration_ms"`
	ProviderRequests     int    `json:"provider_requests"`
	LedgerRecords        int64  `json:"ledger_records"`
	CostMicrounits       *int64 `json:"cost_microunits,omitempty"`
	PromptTokens         *int   `json:"prompt_tokens,omitempty"`
	OutputTokens         *int   `json:"output_tokens,omitempty"`
	CacheReadTokens      *int   `json:"cache_read_tokens,omitempty"`
	VectorSHA256         string `json:"vector_sha256,omitempty"`
	AnswerSHA256         string `json:"answer_sha256,omitempty"`
	Answer               string `json:"answer,omitempty"`
	RequiredFactsPresent *bool  `json:"required_facts_present,omitempty"`
	Status               string `json:"status"`
}

// Result reports experiment status and observed outcomes without credential material.
type Result struct {
	Mode               string                       `json:"mode"`
	PlanSHA256         string                       `json:"plan_sha256"`
	Status             string                       `json:"status"`
	StopReason         string                       `json:"stop_reason,omitempty"`
	ChatRequests       int                          `json:"chat_requests"`
	EmbeddingRequests  int                          `json:"embedding_requests"`
	ReservedMicrounits int64                        `json:"reserved_microunits"`
	LedgerCost         *types.EvaluationRuntimeCost `json:"ledger_cost,omitempty"`
	EmbeddingOutcome   string                       `json:"embedding_outcome"`
	WikiOutcome        string                       `json:"wiki_outcome"`
	Steps              []StepResult                 `json:"steps"`
}

// Run never reads the credential callback unless both execution confirmations
// match the fixed plan. Its live client has one fixed HTTPS destination, no
// environment proxy, no redirect following and no automatic request retries.
func Run(ctx context.Context, opts Options, credential func() (string, error)) (*Result, error) {
	return run(ctx, opts, credential, nil, "live-provider")
}

func run(
	ctx context.Context, opts Options, credential func() (string, error), fixtureClient *http.Client, mode string,
) (*Result, error) {
	p, err := BuildPlan()
	if err != nil {
		return nil, err
	}
	approved, err := validateOptions(opts, p)
	if err != nil {
		return nil, err
	}
	if opts.OutputDir == "" {
		return nil, errors.New("a new output directory is required")
	}
	if err := os.Mkdir(opts.OutputDir, 0o700); err != nil {
		return nil, fmt.Errorf("create fresh output directory: %w", err)
	}
	if err := writeJSON(filepath.Join(opts.OutputDir, "plan.json"), p); err != nil {
		return nil, err
	}
	result := &Result{
		Mode: "dry-run", PlanSHA256: p.SHA256, Status: "not-executed", EmbeddingOutcome: "not-executed",
		WikiOutcome: "not-executed", Steps: []StepResult{},
	}
	if !opts.Execute {
		return result, saveReport(opts.OutputDir, p, result)
	}
	if credential == nil {
		return nil, errors.New("credential source is required")
	}
	key, err := credential()
	if err != nil || strings.TrimSpace(key) == "" {
		return nil, errors.New("execution credential unavailable")
	}
	if strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("invalid credential")
	}
	embeddingKey := key
	if opts.EmbeddingCredential != nil {
		embeddingKey, err = opts.EmbeddingCredential()
		if err != nil || strings.TrimSpace(embeddingKey) == "" || strings.ContainsAny(embeddingKey, "\r\n") {
			return nil, errors.New("embedding credential unavailable or invalid")
		}
	}
	// This command owns a process and does not initialize application telemetry.
	if langfuse.GetManager().Enabled() {
		return nil, errors.New("external tracing must be disabled")
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(opts.OutputDir, "x08.sqlite")),
		&gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}, &types.EmbeddingCacheEntry{},
		&types.EmbeddingCacheLookupRecord{}, &Reservation{}); err != nil {
		return nil, err
	}
	repo := repository.NewModelObservabilityRepository(db)
	cModel, eModel := models()
	readPrice := cacheReadPrice
	for _, price := range []*types.ModelPriceVersion{
		{
			TenantID: tenantID, ModelID: cModel.ID, ValidFrom: time.Now().UTC().Add(-time.Minute), Currency: "CNY",
			InputMicrounitsPerMillion: inputPrice, OutputMicrounitsPerMillion: outputPrice,
			CachePricing: &types.ModelCachePricing{Version: 1, ReadMicrounitsPerMillion: &readPrice},
		},
		{
			TenantID: tenantID, ModelID: eModel.ID, ValidFrom: time.Now().UTC().Add(-time.Minute), Currency: "CNY",
			InputMicrounitsPerMillion: embeddingPrice, OutputMicrounitsPerMillion: 0,
		},
	} {
		if err := repo.CreateModelPrice(ctx, price); err != nil {
			return nil, err
		}
	}
	client := fixtureClient
	if client == nil {
		client = newProviderClient()
	}
	gate := &gateway{
		db: db, client: client, key: key, embeddingKey: embeddingKey, nonce: uuid.NewString(),
		budget: min(approved, p.OriginalPriceUpperBoundMicrounits),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: gate, ReadHeaderTimeout: 5 * time.Second}
	defer func() { _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	// Whitelist only this process's loopback transport. The external destination
	// remains fixed inside gateway.ServeHTTP and never uses this SSRF exception.
	previous, had := os.LookupEnv("SSRF_WHITELIST")
	if err := os.Setenv("SSRF_WHITELIST", "127.0.0.1"); err != nil {
		return nil, err
	}
	defer func() {
		if had {
			_ = os.Setenv("SSRF_WHITELIST", previous)
		} else {
			_ = os.Unsetenv("SSRF_WHITELIST")
		}
	}()
	baseURL := "http://" + listener.Addr().String() + "/compatible-mode/v1"
	cConfig := chat.ConfigFromModel(cModel, "", "")
	cConfig.BaseURL = baseURL
	cConfig.APIKey = "x08-placeholder"
	cConfig.CustomHeaders = map[string]string{"X-X08-Gate": gate.nonce}
	chatProvider, err := chat.NewChat(cConfig, nil)
	if err != nil {
		return nil, err
	}
	eConfig := embedding.ConfigFromModel(eModel, "", "")
	eConfig.BaseURL = baseURL
	eConfig.APIKey = "x08-placeholder"
	eConfig.CustomHeaders = map[string]string{"X-X08-Gate": gate.nonce}
	embedProvider, err := embedding.NewEmbedder(eConfig, nil, nil)
	if err != nil {
		return nil, err
	}
	recorder := modelobs.NewRecorder(repo)
	embedder := modelcache.NewCoordinator(repository.NewEmbeddingCacheRepository(db)).Wrap(
		eModel, recorder.WrapEmbedder(eModel, embedProvider),
	)
	ordered := &orderedChat{inner: recorder.WrapChat(cModel, chatProvider)}
	wiki := service.NewWikiPageEvaluationRunner(ordered)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = modelobs.WithPurpose(ctx, modelobs.PurposeEvaluation, true)
	ctx = modelobs.WithEvaluationTask(ctx, taskID)
	result.Mode = mode
	result.Status = "running"
	for _, step := range p.Steps {
		stepCtx, cancel := context.WithTimeout(call.WithMetadata(ctx, map[string]any{
			"experiment_step": step.ID, "experiment_arm": step.Arm,
			"experiment_round": step.Round, "experiment_pair": step.Pair,
		}), 95*time.Second)
		if err := gate.arm(stepCtx, &step); err != nil {
			cancel()
			result.StopReason = err.Error()
			break
		}
		beforeChat, beforeEmbedding, _, _ := gate.state()
		var before int64
		_ = db.Model(&types.ModelCallRecord{}).Count(&before).Error
		start := time.Now()
		item := StepResult{
			ID: step.ID, Operation: step.Operation, Arm: step.Arm,
			Round: step.Round, Pair: step.Pair, Status: "success",
		}
		var callErr error
		if step.Operation == "embedding" {
			var vectors [][]float32
			vectors, callErr = embedder.BatchEmbed(stepCtx, step.Input)
			if callErr == nil {
				b, _ := json.Marshal(vectors)
				item.VectorSHA256 = hash(b)
			}
		} else {
			ordered.arm = step.Arm
			item.Answer, callErr = wiki(stepCtx, step.Data)
			item.AnswerSHA256 = hash([]byte(item.Answer))
			valid := strings.HasPrefix(item.Answer, "SUMMARY:") &&
				strings.Contains(item.Answer, strconv.Itoa(41+step.Pair)) &&
				strings.Contains(item.Answer, "09:00") && strings.Contains(item.Answer, "17:00")
			item.RequiredFactsPresent = &valid
		}
		item.DurationMs = time.Since(start).Milliseconds()
		cancel()
		afterChat, afterEmbedding, _, stop := gate.state()
		item.ProviderRequests = afterChat + afterEmbedding - beforeChat - beforeEmbedding
		var records []types.ModelCallRecord
		if err := db.Order("started_at ASC, id ASC").Find(&records).Error; err != nil {
			result.StopReason = "ledger_read_failed"
			break
		}
		item.LedgerRecords = int64(len(records)) - before
		if item.ProviderRequests == 1 && item.LedgerRecords == 1 {
			record := records[len(records)-1]
			item.CostMicrounits = record.CostMicrounits
			item.PromptTokens = record.PromptTokens
			item.OutputTokens = record.CompletionTokens
			item.CacheReadTokens = record.ProviderCacheReadTokens
			if !record.AccountingComplete && stop == "" {
				stop = "ledger_accounting_incomplete"
			}
			if record.CostMicrounits != nil &&
				(*record.CostMicrounits < 0 || *record.CostMicrounits > step.ReservationMicrounits) && stop == "" {
				stop = "ledger_cost_exceeds_reservation"
			}
		} else if item.ProviderRequests == 0 && item.LedgerRecords == 0 &&
			step.Operation == "embedding" && step.Round == 2 {
			zero := int64(0)
			item.CostMicrounits = &zero
		} else if stop == "" {
			stop = "request_ledger_cardinality_mismatch"
		}
		if callErr != nil && stop == "" {
			stop = "production_call_failed"
		}
		if types.ModelAccountingError(ctx) != nil && stop == "" {
			stop = "strict_accounting_failed"
		}
		if stop != "" {
			item.Status = "failed"
			result.StopReason = stop
		}
		result.Steps = append(result.Steps, item)
		if err := writeJSON(filepath.Join(opts.OutputDir, "steps.json"), result.Steps); err != nil {
			result.StopReason = "step_evidence_persistence_failed"
		}
		if result.StopReason != "" {
			break
		}
	}
	result.ChatRequests, result.EmbeddingRequests, result.ReservedMicrounits, _ = gate.state()
	result.LedgerCost, err = repo.EvaluationCost(context.WithoutCancel(ctx), tenantID, taskID)
	if err != nil {
		result.StopReason = "cost_summary_failed"
	}
	var reservations []Reservation
	if err := db.Order("prepared_at ASC").Find(&reservations).Error; err != nil {
		result.StopReason = "reservation_read_failed"
	}
	if err := writeJSON(filepath.Join(opts.OutputDir, "provider-usage.json"), reservations); err != nil {
		result.StopReason = "provider_evidence_persistence_failed"
	}
	result.EmbeddingOutcome, result.WikiOutcome = outcomes(result)
	result.Status = "completed"
	if result.StopReason != "" {
		result.Status = "stopped"
	} else if result.EmbeddingOutcome != "physical_calls_reduced" ||
		result.WikiOutcome != "observed_stable_prefix_advantage" {
		result.Status = "inconclusive"
	}
	if err := saveReport(opts.OutputDir, p, result); err != nil {
		return result, err
	}
	if result.Status != "completed" {
		return result, fmt.Errorf("experiment %s: %s; %s", result.Status, result.StopReason, result.WikiOutcome)
	}
	return result, nil
}

func newProviderClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil, MaxConnsPerHost: 1,
			TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 75 * time.Second,
		},
		Timeout:       90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func validateOptions(o Options, p *Plan) (int64, error) {
	if !o.Execute {
		return 0, nil
	}
	if o.ConfirmPlanSHA256 != p.SHA256 {
		return 0, errors.New("--confirm-plan-sha256 must match the reviewed dry-run plan")
	}
	amount, err := ParseCNY(o.ApprovedCNY)
	if err != nil {
		return 0, err
	}
	if amount < p.OriginalPriceUpperBoundMicrounits || amount > 3000000 {
		return 0, errors.New("approved CNY amount must cover the full plan and be at most 3.00")
	}
	return amount, nil
}

// ParseCNY converts a bounded unsigned decimal approval into integer CNY microunits.
func ParseCNY(value string) (int64, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("invalid CNY amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 6 {
		return 0, errors.New("CNY amount supports at most six decimal places")
	}
	for _, part := range parts {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, errors.New("CNY amount must be unsigned decimal")
			}
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 3 {
		return 0, errors.New("CNY approval exceeds 3.00")
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	micro, _ := strconv.ParseInt(fraction, 10, 64)
	return whole*1000000 + micro, nil
}

type orderedChat struct {
	inner chat.Chat
	arm   string
}

func (c *orderedChat) Chat(ctx context.Context, m []chat.Message, o *chat.ChatOptions) (*types.ChatResponse, error) {
	m = orderMessages(m, c.arm)
	options := *o
	options.CacheRetention = chat.CacheRetentionNone
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	ctx = types.WithLLMCallMetadata(ctx, purpose, chat.FingerprintPromptPrefix(m[0].Content, m[len(m)-1].Content))
	ctx = call.WithMetadata(ctx, map[string]any{"provider_cache_mode": "implicit"})
	return c.inner.Chat(ctx, m, &options)
}

func (c *orderedChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("experiment is unary only")
}
func (c *orderedChat) GetModelName() string { return c.inner.GetModelName() }
func (c *orderedChat) GetModelID() string   { return c.inner.GetModelID() }

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
