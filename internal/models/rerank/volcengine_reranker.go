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
)

const (
	VolcengineRerankBaseURL = provider.VolcengineRerankBaseURL

	volcengineRerankPath               = "/api/knowledge/service/rerank"
	volcengineRerankDefaultModel       = "doubao-seed-rerank"
	volcengineRerankDefaultRegion      = "cn-beijing"
	volcengineRerankDefaultInstruction = "Whether the Document answers the Query or matches the content retrieval intent"
	volcengineRerankMaxDocuments       = 50
)

// VolcengineReranker calls the managed Knowledge Service Rerank API with AK/SK signing.
type VolcengineReranker struct {
	modelName   string
	instruction string
	modelID     string
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
		client:      client,
	}, nil
}

func (r *VolcengineReranker) Rerank(
	ctx context.Context, query string, documents []string,
) ([]RankResult, error) {
	if len(documents) == 0 {
		return []RankResult{}, nil
	}
	if len(documents) > volcengineRerankMaxDocuments {
		return nil, fmt.Errorf(
			"Volcengine rerank supports at most %d documents, got %d",
			volcengineRerankMaxDocuments,
			len(documents),
		)
	}

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
		buildRerankRequestDebug(r.modelName, VolcengineRerankBaseURL+volcengineRerankPath, query, documents),
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

	results := make([]RankResult, len(documents))
	for i, score := range response.Data.Scores {
		results[i] = RankResult{
			Index:          i,
			Document:       DocumentInfo{Text: documents[i]},
			RelevanceScore: score,
		}
	}
	return results, nil
}

func (r *VolcengineReranker) GetModelName() string {
	return r.modelName
}

func (r *VolcengineReranker) GetModelID() string {
	return r.modelID
}
