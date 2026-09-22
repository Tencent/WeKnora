package rerank

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	lkeap "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/lkeap/v20240522"
)

const (
	// LKEAPRerankEndpoint 腾讯云知识引擎原子能力 Rerank API 域名
	LKEAPRerankEndpoint = "lkeap.tencentcloudapi.com"
	// LKEAPDefaultRegion RunRerank 支持的地域，默认广州
	LKEAPDefaultRegion = "ap-guangzhou"
	// LKEAPDefaultRerankModel 默认 rerank 模型名
	LKEAPDefaultRerankModel = "lke-reranker-base"
)

// lkeapEndpoint and lkeapScheme are where the SDK sends RunRerank: the public
// API host, as it has always been, whatever the row's base URL says. They are
// variables only so a test can point the SDK at a local server.
var (
	lkeapEndpoint = LKEAPRerankEndpoint
	lkeapScheme   = "HTTPS"
)

// lkeapClient calls Tencent Cloud's RunRerank action through the official
// SDK, which owns the TC3 signing this API requires. It implements
// api.Reranker and nothing else: the per-request ceilings (60 documents,
// 2000 characters for Query plus Docs together) are declared on the vendor
// and enforced by protocolReranker, like every other vendor's.
type lkeapClient struct {
	modelName string
	client    *lkeap.Client
}

func newLKEAPClient(config *RerankerConfig, resolved *catalog.Resolved) (api.Reranker, error) {
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
	cpf.HttpProfile.Endpoint = lkeapEndpoint
	cpf.HttpProfile.Scheme = lkeapScheme

	client, err := lkeap.NewClient(credential, region, cpf)
	if err != nil {
		return nil, fmt.Errorf("create LKEAP client: %w", err)
	}

	modelName := strings.TrimSpace(resolved.RemoteModel)
	if modelName == "" {
		modelName = LKEAPDefaultRerankModel
	}
	return &lkeapClient{modelName: modelName, client: client}, nil
}

// Rerank scores one batch. RunRerank answers a bare score list in input
// order rather than an indexed, sorted result set.
func (r *lkeapClient) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	req := lkeap.NewRunRerankRequest()
	req.Query = common.StringPtr(query)
	req.Docs = common.StringPtrs(documents)
	req.Model = common.StringPtr(r.modelName)

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

	results := make([]api.RerankResult, len(documents))
	for i, score := range scores {
		results[i] = api.RerankResult{Index: i, Text: documents[i]}
		if score != nil {
			results[i].Score = *score
		}
	}
	return results, nil
}
