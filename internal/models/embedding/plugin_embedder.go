package embedding

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// pluginEmbedder adapts an external model plugin (gRPC ModelPlugin) to the host
// Embedder interface.
type pluginEmbedder struct {
	providerName string
	modelName    string
	modelID      string
	dimensions   int
}

func newPluginEmbedder(config *Config) Embedder {
	return &pluginEmbedder{
		providerName: config.Provider,
		modelName:    config.ModelName,
		modelID:      config.ModelID,
		dimensions:   config.Dimensions,
	}
}

func (p *pluginEmbedder) GetModelName() string { return p.modelName }
func (p *pluginEmbedder) GetModelID() string   { return p.modelID }
func (p *pluginEmbedder) GetDimensions() int   { return p.dimensions }

func (p *pluginEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.Embed(callCtx, &pluginproto.ModelEmbedRequest{Text: text})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("plugin: %s", resp.GetError())
	}
	return resp.GetVector(), nil
}

func (p *pluginEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.BatchEmbed(callCtx, &pluginproto.ModelBatchEmbedRequest{Texts: texts})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("plugin: %s", resp.GetError())
	}
	vectors := make([][]float32, 0, len(resp.GetVectors()))
	for _, v := range resp.GetVectors() {
		vectors = append(vectors, v.GetValues())
	}
	return vectors, nil
}

// BatchEmbedWithPool satisfies EmbedderPooler. External plugins have no host-side
// pooling, so it degrades to a plain BatchEmbed.
func (p *pluginEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	return p.BatchEmbed(ctx, texts)
}
