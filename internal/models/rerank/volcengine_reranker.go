package rerank

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/volcengine/vikingdb-go-sdk/knowledge"
	knowledgemodel "github.com/volcengine/vikingdb-go-sdk/knowledge/model"
	"golang.org/x/sync/errgroup"
)

const (
	VolcengineRerankBaseURL = provider.VolcengineRerankBaseURL

	volcengineRerankPath               = "/api/knowledge/service/rerank"
	volcengineRerankDefaultModel       = "doubao-seed-rerank"
	volcengineRerankDefaultRegion      = "cn-beijing"
	volcengineRerankDefaultInstruction = "Whether the Document answers the Query or matches the content retrieval intent"
	volcengineRerankMaxDocuments       = 50
	// volcengineRerankMaxConcurrency bounds the number of in-flight batch
	// requests when the candidate set exceeds volcengineRerankMaxDocuments, so a
	// very large embedding_top_k cannot fan out into an unbounded burst of calls.
	volcengineRerankMaxConcurrency = 4
)

// VolcengineReranker calls the managed Knowledge Service Rerank API with AK/SK signing.
type VolcengineReranker struct {
	modelName   string
	instruction string
	modelID     string
	endpoint    string
	client      *knowledge.Client
}

func NewVolcengineReranker(config *RerankerConfig) (*VolcengineReranker, error) {
	accessKey := strings.TrimSpace(config.APIKey)
	secretKey := strings.TrimSpace(config.AppSecret)
	if secretKey == "" && config.ExtraConfig != nil {
		secretKey = strings.TrimSpace(config.ExtraConfig["secret_key"])
	}
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("access key and secret key are required for Volcengine rerank")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = VolcengineRerankBaseURL
	}
	if err := validateRerankBaseURL(baseURL); err != nil {
		return nil, err
	}

	modelName := strings.TrimSpace(config.ModelName)
	if modelName == "" {
		modelName = volcengineRerankDefaultModel
	}
	region := volcengineRerankDefaultRegion
	instruction := volcengineRerankDefaultInstruction
	if config.ExtraConfig != nil {
		if value := strings.TrimSpace(config.ExtraConfig["region"]); value != "" {
			region = value
		}
		if value := strings.TrimSpace(config.ExtraConfig["instruction"]); value != "" {
			instruction = value
		}
	}

	client, err := knowledge.New(
		knowledge.AuthIAM(accessKey, secretKey),
		knowledge.WithEndpoint(baseURL),
		knowledge.WithRegion(region),
		knowledge.WithTimeout(30*time.Second),
		knowledge.WithHTTPClient(newRerankHTTPClient(30*time.Second)),
		knowledge.WithMaxRetries(1),
	)
	if err != nil {
		return nil, fmt.Errorf("create Volcengine rerank client: %w", err)
	}

	return &VolcengineReranker{
		modelName:   modelName,
		instruction: instruction,
		modelID:     config.ModelID,
		endpoint:    baseURL,
		client:      client,
	}, nil
}

func (r *VolcengineReranker) Rerank(
	ctx context.Context, query string, documents []string,
) ([]RankResult, error) {
	if len(documents) == 0 {
		return []RankResult{}, nil
	}

	// The managed Knowledge Service Rerank API rejects requests carrying more
	// than volcengineRerankMaxDocuments items. Upstream callers (chat pipeline,
	// agent knowledge search, message search) feed in every retrieval candidate
	// and do not cap the count per provider, so a large embedding_top_k or a
	// multi-target search can exceed the limit. Each Data item is scored
	// independently against the same (query, instruction) pair, so the scores
	// are comparable across requests — we can split the documents into limit-
	// sized batches, rerank them concurrently, and merge without losing any
	// candidate (unlike truncation) or biasing the ranking.
	results := make([]RankResult, len(documents))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(volcengineRerankMaxConcurrency)
	for start := 0; start < len(documents); start += volcengineRerankMaxDocuments {
		start := start
		end := min(start+volcengineRerankMaxDocuments, len(documents))
		g.Go(func() error {
			scores, err := r.rerankBatch(gctx, query, documents[start:end])
			if err != nil {
				return err
			}
			for i, score := range scores {
				results[start+i] = RankResult{
					Index:          start + i,
					Document:       DocumentInfo{Text: documents[start+i]},
					RelevanceScore: score,
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// rerankBatch scores a single batch of documents (already sized within the API
// limit) and returns the per-document relevance scores in input order.
func (r *VolcengineReranker) rerankBatch(
	ctx context.Context, query string, documents []string,
) ([]float64, error) {
	data := make([]knowledgemodel.RerankDataItem, len(documents))
	for i := range documents {
		data[i] = knowledgemodel.RerankDataItem{
			Query:   query,
			Content: &documents[i],
		}
	}
	request := knowledgemodel.RerankRequest{
		Datas:             data,
		RerankModel:       &r.modelName,
		RerankInstruction: &r.instruction,
	}

	logger.Debugf(
		ctx,
		"%s",
		buildRerankRequestDebug(r.modelName, r.endpoint+volcengineRerankPath, query, documents),
	)
	response, err := r.client.Rerank(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("call Volcengine rerank: %w", err)
	}
	if response == nil || response.Data == nil {
		return nil, fmt.Errorf("Volcengine rerank returned an empty response")
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("Volcengine rerank API error %d: %s", response.Code, response.Message)
	}
	if len(response.Data.Scores) != len(documents) {
		return nil, fmt.Errorf(
			"Volcengine rerank score count mismatch: got %d scores for %d documents",
			len(response.Data.Scores),
			len(documents),
		)
	}
	return response.Data.Scores, nil
}

func (r *VolcengineReranker) GetModelName() string {
	return r.modelName
}

func (r *VolcengineReranker) GetModelID() string {
	return r.modelID
}
