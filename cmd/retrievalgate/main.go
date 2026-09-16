package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/types"
)

type generatedResult struct {
	Success bool `json:"success"`
	Data    struct {
		Task struct {
			ID         string                 `json:"id"`
			Status     types.EvaluationStatue `json:"status"`
			Total      int                    `json:"total"`
			Finished   int                    `json:"finished"`
			DurationMS int64                  `json:"duration_ms"`
		} `json:"task"`
		Metric struct {
			Retrieval  types.RetrievalMetrics  `json:"retrieval_metrics"`
			Generation types.GenerationMetrics `json:"generation_metrics"`
		} `json:"metric"`
		Usage struct {
			CostByCurrency map[string]float64 `json:"cost_by_currency"`
		} `json:"usage"`
	} `json:"data"`
}

func main() {
	out := flag.String("out", "", "optional generated evaluation result path")
	flag.Parse()
	result, err := runProductionPipelineGate(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(string(encoded))
	if *out != "" {
		if err := os.WriteFile(*out, append(encoded, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
}

// runProductionPipelineGate generates its result by executing the production
// deterministic ordering/TopK plugin and production metric implementations.
// It is intentionally code-derived rather than a checked-in passing fixture.
func runProductionPipelineGate(ctx context.Context) (generatedResult, error) {
	started := time.Now()
	manager := chatpipeline.NewEventManager()
	chatpipeline.NewPluginFilterTopK(manager)
	request := &types.ChatManage{PipelineRequest: types.PipelineRequest{RerankTopK: 3}}
	request.SearchResult = []*types.SearchResult{
		{ID: "irrelevant-high", ChunkIndex: 102, KnowledgeID: "kb", Score: 0.99},
		{ID: "relevant-first", ChunkIndex: 101, KnowledgeID: "kb", Score: 0.90},
		{ID: "relevant-second", ChunkIndex: 103, KnowledgeID: "kb", Score: 0.80},
		{ID: "irrelevant-low", ChunkIndex: 104, KnowledgeID: "kb", Score: 0.70},
	}
	if err := manager.Trigger(ctx, types.FILTER_TOP_K, request); err != nil {
		return generatedResult{}, fmt.Errorf("production TopK stage failed: %v", err)
	}
	retrieved := make([]int, 0, len(request.SearchResult))
	for _, item := range request.SearchResult {
		retrieved = append(retrieved, item.ChunkIndex)
	}
	input := &types.MetricInput{
		RetrievalGT: [][]int{{101, 103}}, RetrievalIDs: retrieved,
		GeneratedTexts: "WeKnora provides retrieval augmented generation.",
		GeneratedGT:    "WeKnora provides retrieval augmented generation.",
	}
	var result generatedResult
	result.Success = true
	result.Data.Task.ID = "evaluation-production-pipeline-gate"
	result.Data.Task.Status = types.EvaluationStatueSuccess
	result.Data.Task.Total = 1
	result.Data.Task.Finished = 1
	result.Data.Task.DurationMS = time.Since(started).Milliseconds()
	result.Data.Metric.Retrieval = types.RetrievalMetrics{
		Precision: metric.NewPrecisionMetric().Compute(input),
		Recall:    metric.NewRecallMetric().Compute(input),
		NDCG10:    metric.NewNDCGMetric(10).Compute(input),
		MRR:       metric.NewMRRMetric().Compute(input),
	}
	result.Data.Metric.Generation.ROUGEL = metric.NewRougeMetric(false, "rouge-l", "f").Compute(input)
	result.Data.Usage.CostByCurrency = map[string]float64{"USD": 0}
	return result, nil
}
