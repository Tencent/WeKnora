// Package volcengineknowledge implements Volcengine Knowledge Service rerank through its official SDK.
package volcengineknowledge

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/volcengine/vikingdb-go-sdk/knowledge"
	knowledgemodel "github.com/volcengine/vikingdb-go-sdk/knowledge/model"
)

// SDK defaults follow the managed rerank service.
const (
	DefaultModel  = "doubao-seed-rerank"
	DefaultRegion = "cn-beijing"
	// DefaultInstruction is the console's default instruction, verbatim: "如需对齐控制台效果，请使用
	// 相同指令" (https://docs.volcengine.com/docs/vector_database_vikingdb/Rerank).
	DefaultInstruction = "Whether the document answers the query " +
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

// Config contains resolved connection parameters for the SDK client.
type Config struct {
	AccessKey, SecretKey, BaseURL, Model, Region, Instruction string
	Client                                                    *http.Client
}

// New creates an authenticated rerank protocol client.
func New(config Config) (api.Reranker, error) {
	accessKey, secretKey := strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("access key and secret key are required for Volcengine rerank")
	}
	baseURL := config.BaseURL
	modelName := strings.TrimSpace(config.Model)
	if modelName == "" {
		modelName = DefaultModel
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = DefaultRegion
	}
	instruction := strings.TrimSpace(config.Instruction)
	if instruction == "" {
		instruction = DefaultInstruction
	}
	if config.Client == nil {
		config.Client = api.HTTPClient
	}

	client, err := knowledge.New(
		knowledge.AuthIAM(accessKey, secretKey),
		knowledge.WithEndpoint(baseURL),
		knowledge.WithRegion(region),
		knowledge.WithTimeout(30*time.Second),
		knowledge.WithHTTPClient(config.Client),
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
		return nil, fmt.Errorf("volcengine rerank returned an empty response")
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("volcengine rerank API error %d: %s", response.Code, response.Message)
	}
	if len(response.Data.Scores) != len(documents) {
		return nil, fmt.Errorf(
			"volcengine rerank score count mismatch: got %d scores for %d documents",
			len(response.Data.Scores), len(documents),
		)
	}

	results := make([]api.RerankResult, len(documents))
	for i, score := range response.Data.Scores {
		results[i] = api.RerankResult{Index: i, Score: score, Text: documents[i]}
	}
	return results, nil
}
