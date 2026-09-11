// Command training-orchestration-preview runs the two-stage training
// orchestration against real acceptance data without creating a job or
// publishing a Wiki page.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/llm"
	"github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/service/trainingorchestration"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type callReport struct {
	Stage           string `json:"stage"`
	Batch           string `json:"batch"`
	Attempt         int    `json:"attempt"`
	CallNumber      int    `json:"call_number"`
	Budget          int    `json:"budget"`
	EstimatedTokens int    `json:"estimated_tokens"`
	DurationMs      int64  `json:"duration_ms"`
	EndReason       string `json:"end_reason"`
}

type previewReport struct {
	SchemaVersion               string                                    `json:"schema_version"`
	Mode                        string                                    `json:"mode"`
	Published                   bool                                      `json:"published"`
	WikiWrites                  int                                       `json:"wiki_writes"`
	StartedAt                   string                                    `json:"started_at"`
	FinishedAt                  string                                    `json:"finished_at"`
	Model                       string                                    `json:"model"`
	PromptVersion               string                                    `json:"prompt_version"`
	Snapshot                    trainingorchestration.CatalogSnapshot     `json:"snapshot"`
	Plan                        *trainingorchestration.PlanDraft          `json:"plan,omitempty"`
	Projection                  *trainingorchestration.ProjectionDocument `json:"projection,omitempty"`
	MaterializedClusters        int                                       `json:"materialized_clusters"`
	MaterializedSummaryBlocks   int                                       `json:"materialized_summary_blocks"`
	MaterializedEvidence        int                                       `json:"materialized_evidence"`
	RetrievedClusters           int                                       `json:"retrieved_clusters"`
	RetrievedEvidence           int                                       `json:"retrieved_evidence"`
	RetrievalDegraded           bool                                      `json:"retrieval_degraded"`
	RetrievalDegradationReasons []string                                  `json:"retrieval_degradation_reasons,omitempty"`
	GeneratedClusterParts       int                                       `json:"generated_cluster_parts"`
	GeneratedRelationCandidates int                                       `json:"generated_relation_candidates"`
	Calls                       []callReport                              `json:"calls"`
	Error                       string                                    `json:"error,omitempty"`
	ErrorCode                   string                                    `json:"error_code,omitempty"`
}

type recordingPlanner struct {
	inner trainingorchestration.Planner
	plan  *trainingorchestration.PlanDraft
}

func (r *recordingPlanner) Plan(ctx context.Context, snapshot trainingorchestration.CatalogSnapshot) (trainingorchestration.PlanDraft, error) {
	plan, err := r.inner.Plan(ctx, snapshot)
	if err == nil {
		r.plan = &plan
	}
	return plan, err
}

func (r *recordingPlanner) SetCompletionGate(gate *trainingorchestration.CompletionGate) {
	if setter, ok := r.inner.(interface {
		SetCompletionGate(*trainingorchestration.CompletionGate)
	}); ok {
		setter.SetCompletionGate(gate)
	}
}

type recordingMaterializer struct {
	inner     trainingorchestration.StageFourMaterializer
	materials []trainingorchestration.ClusterMaterial
}

type recordingMaterialRetriever struct {
	inner     trainingorchestration.StageFourMaterialRetriever
	materials []trainingorchestration.ClusterMaterial
}

func (r *recordingMaterialRetriever) Retrieve(ctx context.Context, cluster trainingorchestration.PlanCluster, material trainingorchestration.ClusterMaterial) (trainingorchestration.ClusterMaterial, error) {
	retrieved, err := r.inner.Retrieve(ctx, cluster, material)
	if err == nil {
		r.materials = append(r.materials, retrieved)
	}
	return retrieved, err
}

func (r *recordingMaterializer) Materialize(ctx context.Context, snapshot trainingorchestration.CatalogSnapshot, plan trainingorchestration.PlanDraft) ([]trainingorchestration.ClusterMaterial, error) {
	materials, err := r.inner.Materialize(ctx, snapshot, plan)
	if err == nil {
		r.materials = materials
	}
	return materials, err
}

type recordingClusterGenerator struct {
	inner     trainingorchestration.StageFourClusterGenerator
	generated int
}

func (r *recordingClusterGenerator) Generate(ctx context.Context, cluster trainingorchestration.PlanCluster, material trainingorchestration.ClusterMaterial) (trainingorchestration.ClusterGenerationDraft, error) {
	r.generated++
	return r.inner.Generate(ctx, cluster, material)
}

func (r *recordingClusterGenerator) SplitMaterial(cluster trainingorchestration.PlanCluster, material trainingorchestration.ClusterMaterial) ([]trainingorchestration.ClusterGenerationPart, error) {
	return r.inner.SplitMaterial(cluster, material)
}

func (r *recordingClusterGenerator) SetCompletionGate(gate *trainingorchestration.CompletionGate) {
	if setter, ok := r.inner.(interface {
		SetCompletionGate(*trainingorchestration.CompletionGate)
	}); ok {
		setter.SetCompletionGate(gate)
	}
}

type recordingRelationGenerator struct {
	inner     trainingorchestration.StageFourRelationGenerator
	relations int
}

func (r *recordingRelationGenerator) Generate(ctx context.Context, clusters []trainingorchestration.TopicCluster) ([]trainingorchestration.TopicClusterRelation, error) {
	relations, err := r.inner.Generate(ctx, clusters)
	if err == nil {
		r.relations += len(relations)
	}
	return relations, err
}

func (r *recordingRelationGenerator) SetCompletionGate(gate *trainingorchestration.CompletionGate) {
	if setter, ok := r.inner.(interface {
		SetCompletionGate(*trainingorchestration.CompletionGate)
	}); ok {
		setter.SetCompletionGate(gate)
	}
}

type recordingSnapshotReader struct {
	inner trainingorchestration.StageFourSnapshotReader
}

func (r recordingSnapshotReader) CollectCatalog(ctx context.Context) (trainingorchestration.CatalogSnapshot, error) {
	return r.inner.CollectCatalog(ctx)
}

func main() {
	outputPath := flag.String("output", "", "write the report to this path instead of stdout")
	flag.Parse()

	cfg := config.Load()
	started := time.Now().UTC()
	report := previewReport{
		SchemaVersion: "training-orchestration/stage-6-preview/v1",
		Mode:          "isolated_real_generation",
		Published:     false,
		WikiWrites:    0,
		StartedAt:     started.Format(time.RFC3339Nano),
		Model:         cfg.LLM.Model,
		PromptVersion: cfg.Training.PromptVersion,
		Calls:         []callReport{},
	}

	ctx := context.Background()
	timeout := time.Duration(cfg.Training.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	llmClient := llm.NewClient(cfg.LLM)
	roles, err := cfg.WeKnora.ResolveKnowledgeBaseRoles()
	if err != nil {
		finishWithError(&report, err)
		writeReport(*outputPath, report)
		os.Exit(1)
	}
	db, err := openDatabase(cfg)
	if err != nil {
		finishWithError(&report, err)
		writeReport(*outputPath, report)
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}

	minioClient, err := minio.New(cfg.MinIO)
	if err != nil {
		finishWithError(&report, err)
		writeReport(*outputPath, report)
		os.Exit(1)
	}
	evidenceClient := weknora.New(cfg.WeKnora.ForKnowledgeBase(roles.Evidence))
	knowledgeClient := weknora.New(cfg.WeKnora.ForKnowledgeBase(roles.Knowledge))
	wikiClient := weknora.NewWikiClient(cfg.WeKnora.ForKnowledgeBase(roles.Knowledge))
	collector := &trainingorchestration.Collector{
		DB: db, Wiki: wikiClient, SourceReader: knowledgeClient,
		TranscriptReader: transcriptservice.NewReader(db, evidenceClient),
		VideoAccess:      trainingorchestration.NewVideoAccessReader(minioClient),
		KnowledgeBaseID:  roles.Knowledge,
		OwnerScopeID:     cfg.Training.OwnerScopeID,
	}

	snapshot, err := collector.CollectCatalog(ctx)
	if err != nil {
		finishWithError(&report, err)
		writeReport(*outputPath, report)
		os.Exit(1)
	}
	report.Snapshot = snapshot

	gate := trainingorchestration.NewCompletionGate(trainingorchestration.CompletionGateConfig{
		RequestTimeout:      time.Duration(cfg.Training.RequestTimeoutSeconds) * time.Second,
		MaxConcurrent:       cfg.Training.MaxConcurrentCalls,
		MaxTotalCalls:       cfg.Training.MaxTotalCalls,
		MaxTotalInputTokens: cfg.Training.MaxInputTokens,
		Logger: func(entry trainingorchestration.CompletionLog) {
			report.Calls = append(report.Calls, callReport{
				Stage: entry.Stage, Batch: entry.Batch, Attempt: entry.Attempt,
				CallNumber: entry.CallNumber, Budget: entry.Budget,
				EstimatedTokens: entry.EstimatedTokens,
				DurationMs:      entry.Duration.Milliseconds(), EndReason: entry.EndReason,
			})
		},
	})
	planner := &trainingorchestration.StageOnePlanner{
		LLM:  llmClient,
		Gate: gate,
		Config: trainingorchestration.PlannerConfig{
			MaxInputTokens:      cfg.Training.PlanningMaxInputTokens,
			MergeMaxInputTokens: cfg.Training.PlanningMergeMaxInputTokens,
			MaxVideosPerBatch:   cfg.Training.PlanningMaxVideosPerBatch,
			MaxMergeItems:       cfg.Training.PlanningMaxMergeItems,
		},
		PromptVersion: cfg.Training.PromptVersion,
	}
	materializer := &trainingorchestration.Materializer{
		Wiki: wikiClient, Evidence: transcriptservice.NewReader(db, evidenceClient),
		KnowledgeBaseID: roles.Knowledge,
	}
	materialRetriever := &trainingorchestration.QueryMaterialRetriever{
		Evidence: transcriptservice.NewReader(db, evidenceClient),
	}
	clusterGenerator := &trainingorchestration.ClusterGenerator{
		LLM: llmClient, Gate: gate,
		MaxInputTokens: cfg.Training.ClusterGenerationMaxInputTokens,
		PromptVersion:  cfg.Training.PromptVersion,
	}
	relationGenerator := &trainingorchestration.RelationGenerator{
		LLM: llmClient, Gate: gate,
		MaxInputTokens:      cfg.Training.RelationMaxInputTokens,
		BatchMaxInputTokens: cfg.Training.RelationBatchMaxInputTokens,
	}
	assembler := &trainingorchestration.ProjectionAssembler{
		Fingerprint: func(snapshot trainingorchestration.CatalogSnapshot) (string, error) {
			return trainingorchestration.CatalogFingerprint(snapshot, llmClient.Model(), cfg.Training.PromptVersion)
		},
	}
	recordingPlanner := &recordingPlanner{inner: planner}
	recordingMaterializer := &recordingMaterializer{inner: materializer}
	recordingRetriever := &recordingMaterialRetriever{inner: materialRetriever}
	recordingClusterGenerator := &recordingClusterGenerator{inner: clusterGenerator}
	recordingRelationGenerator := &recordingRelationGenerator{inner: relationGenerator}
	orchestrator := &trainingorchestration.StageFourOrchestrator{
		Planner: recordingPlanner, Materializer: recordingMaterializer,
		MaterialRetriever: recordingRetriever,
		ClusterGenerator:  recordingClusterGenerator,
		RelationGenerator: recordingRelationGenerator,
		SnapshotReader:    recordingSnapshotReader{inner: collector},
		Assembler:         assembler, Gate: gate,
		MaxRecursionDepth: cfg.Training.MaxRecursionDepth,
		TaskTimeout:       timeout,
	}

	document, err := orchestrator.Run(ctx, snapshot)
	report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	report.MaterializedClusters = len(recordingMaterializer.materials)
	for _, material := range recordingMaterializer.materials {
		report.MaterializedSummaryBlocks += len(material.SummaryBlocks)
		report.MaterializedEvidence += len(material.Evidence)
	}
	report.RetrievedClusters = len(recordingRetriever.materials)
	reasons := make(map[string]struct{})
	for _, material := range recordingRetriever.materials {
		report.RetrievedEvidence += len(material.Evidence)
		if material.RetrievalDegraded {
			report.RetrievalDegraded = true
			reason := material.RetrievalDegradationReason
			if reason == "" {
				reason = "search_unavailable"
			}
			reasons[reason] = struct{}{}
		}
	}
	for reason := range reasons {
		report.RetrievalDegradationReasons = append(report.RetrievalDegradationReasons, reason)
	}
	sort.Strings(report.RetrievalDegradationReasons)
	report.GeneratedClusterParts = recordingClusterGenerator.generated
	report.GeneratedRelationCandidates = recordingRelationGenerator.relations
	if recordingPlanner.plan != nil {
		report.Plan = recordingPlanner.plan
	}
	if err != nil {
		finishWithError(&report, err)
		writeReport(*outputPath, report)
		os.Exit(1)
	}
	report.Projection = &document
	writeReport(*outputPath, report)
}

func finishWithError(report *previewReport, err error) {
	if report.FinishedAt == "" {
		report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	report.Error = err.Error()
	var generationErr *trainingorchestration.GenerationError
	if errors.As(err, &generationErr) {
		report.ErrorCode = generationErr.Code
	}
}

func writeReport(path string, report previewReport) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode preview report: %v\n", err)
		return
	}
	if path == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create preview report directory: %v\n", err)
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write preview report: %v\n", err)
	}
}

func openDatabase(cfg *config.Config) (*gorm.DB, error) {
	if cfg.Database.Driver == "sqlite" {
		return gorm.Open(sqlite.Open(cfg.Database.Path), &gorm.Config{})
	}
	return gorm.Open(postgres.Open(cfg.Database.DSN()), &gorm.Config{})
}
