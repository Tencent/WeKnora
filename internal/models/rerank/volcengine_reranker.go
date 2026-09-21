package rerank

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/volcengine/vikingdb-go-sdk/knowledge"
	knowledgemodel "github.com/volcengine/vikingdb-go-sdk/knowledge/model"
)

const (
	volcengineRerankDefaultModel  = "doubao-seed-rerank"
	volcengineRerankDefaultRegion = "cn-beijing"
	// The console's default instruction, verbatim: "如需对齐控制台效果，请使用
	// 相同指令" (https://docs.volcengine.com/docs/vector_database_vikingdb/Rerank).
	volcengineRerankDefaultInstruction = "Whether the document answers the query " +
		"or matches the content retrieval intent"
)

// volcengineClient calls the managed Knowledge Service rerank through the
// vikingdb SDK, which owns the AK/SK signing. It implements api.Reranker and
// nothing else: the 200-document ceiling and the batch concurrency are
// declared on the vendor and enforced by protocolReranker.
type volcengineClient struct {
	modelName   string
	instruction string
	client      *knowledge.Client
}

func newVolcengineClient(config *RerankerConfig, resolved *catalog.Resolved) (api.Reranker, error) {
	accessKey := strings.TrimSpace(config.APIKey)
	secretKey := strings.TrimSpace(config.AppSecret)
	if secretKey == "" && config.ExtraConfig != nil {
		secretKey = strings.TrimSpace(config.ExtraConfig["secret_key"])
	}
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("access key and secret key are required for Volcengine rerank")
	}

	// The catalog supplies the vendor's Knowledge Service host when the row
	// names none.
	baseURL := resolved.BaseURL

	modelName := strings.TrimSpace(resolved.RemoteModel)
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
	return &volcengineClient{modelName: modelName, instruction: instruction, client: client}, nil
}

// Rerank scores one batch. Every document is paired with the same query and
// instruction, and the reply is a score list in input order.
func (r *volcengineClient) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	data := make([]knowledgemodel.RerankDataItem, len(documents))
	for i := range documents {
		data[i] = knowledgemodel.RerankDataItem{Query: query, Content: &documents[i]}
	}
	request := knowledgemodel.RerankRequest{
		Datas:             data,
		RerankModel:       &r.modelName,
		RerankInstruction: &r.instruction,
	}

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
			len(response.Data.Scores), len(documents),
		)
	}

	results := make([]api.RerankResult, len(documents))
	for i, score := range response.Data.Scores {
		results[i] = api.RerankResult{Index: i, Score: score, Text: documents[i]}
	}
	return results, nil
}
