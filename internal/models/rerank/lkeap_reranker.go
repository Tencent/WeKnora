package rerank

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	lkeap "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/lkeap/v20240522"
)

const (
	// LKEAPMaxDocumentsPerRequest is the maximum number of documents accepted by RunRerank.
	LKEAPMaxDocumentsPerRequest = 60
	// LKEAPMaxRequestCharacters is the maximum combined length of Query and Docs accepted by RunRerank.
	LKEAPMaxRequestCharacters = 2000
	// LKEAPRerankEndpoint 腾讯云知识引擎原子能力 Rerank API 域名
	LKEAPRerankEndpoint = "lkeap.tencentcloudapi.com"
	// LKEAPDefaultRegion RunRerank 支持的地域，默认广州
	LKEAPDefaultRegion = "ap-guangzhou"
	// LKEAPDefaultRerankModel 默认 rerank 模型名
	LKEAPDefaultRerankModel = "lke-reranker-base"
)

// LKEAPReranker 使用腾讯云知识引擎原子能力 RunRerank 接口进行重排序。
// 鉴权使用腾讯云 API 密钥：APIKey 为 SecretId，AppSecret 为 SecretKey。
type LKEAPReranker struct {
	modelName string
	modelID   string
	client    *lkeap.Client
}

// NewLKEAPReranker 创建 LKEAP rerank 客户端。
func NewLKEAPReranker(config *RerankerConfig) (*LKEAPReranker, error) {
	secretID := strings.TrimSpace(config.APIKey)
	secretKey := strings.TrimSpace(config.AppSecret)
	if secretKey == "" && config.ExtraConfig != nil {
		secretKey = strings.TrimSpace(config.ExtraConfig["secret_key"])
	}
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("secret_id and secret_key are required for LKEAP rerank (set API Key and Secret Key)")
	}

	region := LKEAPDefaultRegion
	if config.ExtraConfig != nil {
		if r := strings.TrimSpace(config.ExtraConfig["region"]); r != "" {
			region = r
		}
	}

	credential := common.NewCredential(secretID, secretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = LKEAPRerankEndpoint

	client, err := lkeap.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("create LKEAP client: %w", err)
	}

	modelName := strings.TrimSpace(config.ModelName)
	if modelName == "" {
		modelName = LKEAPDefaultRerankModel
	}

	return &LKEAPReranker{
		modelName: modelName,
		modelID:   config.ModelID,
		client:    client,
	}, nil
}

// Rerank 调用 RunRerank 对文档按与 query 的相关性打分。
func (r *LKEAPReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	if len(documents) == 0 {
		return []RankResult{}, nil
	}

	batches, err := lkeapRerankBatches(query, documents)
	if err != nil {
		return nil, err
	}

	results := make([]RankResult, 0, len(documents))
	for _, batch := range batches {
		batchResults, err := r.rerankBatch(ctx, query, batch.documents)
		if err != nil {
			return nil, err
		}
		for i := range batchResults {
			batchResults[i].Index += batch.start
		}
		results = append(results, batchResults...)
	}
	return results, nil
}

type lkeapRerankBatch struct {
	start     int
	documents []string
}

func lkeapRerankBatches(query string, documents []string) ([]lkeapRerankBatch, error) {
	queryLength := utf8.RuneCountInString(query)
	if queryLength >= LKEAPMaxRequestCharacters {
		return nil, fmt.Errorf("LKEAP rerank query is %d characters; Query and Docs together support at most %d characters", queryLength, LKEAPMaxRequestCharacters)
	}

	batches := make([]lkeapRerankBatch, 0, (len(documents)+LKEAPMaxDocumentsPerRequest-1)/LKEAPMaxDocumentsPerRequest)
	batchStart := 0
	batchLength := queryLength
	batchDocuments := make([]string, 0, LKEAPMaxDocumentsPerRequest)
	for index, document := range documents {
		documentLength := utf8.RuneCountInString(document)
		if queryLength+documentLength > LKEAPMaxRequestCharacters {
			return nil, fmt.Errorf("LKEAP rerank document at index %d is %d characters; Query and each document together support at most %d characters", index, documentLength, LKEAPMaxRequestCharacters)
		}
		if len(batchDocuments) == LKEAPMaxDocumentsPerRequest || batchLength+documentLength > LKEAPMaxRequestCharacters {
			batches = append(batches, lkeapRerankBatch{start: batchStart, documents: batchDocuments})
			batchStart = index
			batchLength = queryLength
			batchDocuments = make([]string, 0, LKEAPMaxDocumentsPerRequest)
		}
		batchDocuments = append(batchDocuments, document)
		batchLength += documentLength
	}
	if len(batchDocuments) > 0 {
		batches = append(batches, lkeapRerankBatch{start: batchStart, documents: batchDocuments})
	}
	return batches, nil
}

func (r *LKEAPReranker) rerankBatch(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	req := lkeap.NewRunRerankRequest()
	req.Query = common.StringPtr(query)
	req.Docs = common.StringPtrs(documents)
	req.Model = common.StringPtr(r.modelName)

	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, LKEAPRerankEndpoint, query, documents))

	resp, err := r.client.RunRerankWithContext(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LKEAP RunRerank: %w", err)
	}
	if resp == nil || resp.Response == nil || len(resp.Response.ScoreList) == 0 {
		return nil, fmt.Errorf("LKEAP rerank API returned empty score list")
	}

	scores := resp.Response.ScoreList
	if len(scores) != len(documents) {
		return nil, fmt.Errorf("LKEAP rerank score count mismatch: got %d scores for %d documents",
			len(scores), len(documents))
	}

	results := make([]RankResult, len(documents))
	for i, score := range scores {
		if score == nil {
			continue
		}
		results[i] = RankResult{
			Index: i,
			Document: DocumentInfo{
				Text: documents[i],
			},
			RelevanceScore: *score,
		}
	}
	return results, nil
}

// GetModelName returns the rerank model name.
func (r *LKEAPReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the model ID.
func (r *LKEAPReranker) GetModelID() string {
	return r.modelID
}
