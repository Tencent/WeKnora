package rerank

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// pluginReranker adapts an external model plugin (gRPC ModelPlugin) to the host
// Reranker interface.
type pluginReranker struct {
	providerName string
	modelName    string
	modelID      string
}

func newPluginReranker(config *RerankerConfig) Reranker {
	return &pluginReranker{providerName: config.Provider, modelName: config.ModelName, modelID: config.ModelID}
}

func (p *pluginReranker) GetModelName() string { return p.modelName }
func (p *pluginReranker) GetModelID() string   { return p.modelID }

func (p *pluginReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.Rerank(callCtx, &pluginproto.ModelRerankRequest{Query: query, Documents: documents})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("plugin: %s", resp.GetError())
	}
	results := make([]RankResult, 0, len(resp.GetResults()))
	for _, r := range resp.GetResults() {
		result := RankResult{Index: int(r.GetIndex()), RelevanceScore: r.GetRelevanceScore()}
		if idx := result.Index; idx >= 0 && idx < len(documents) {
			result.Document = DocumentInfo{Text: documents[idx]}
		}
		results = append(results, result)
	}
	return results, nil
}
