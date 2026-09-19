package vlm

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// pluginVLM adapts an external model plugin (gRPC ModelPlugin) to the host VLM
// interface.
type pluginVLM struct {
	providerName string
	modelName    string
	modelID      string
}

func newPluginVLM(config *Config) VLM {
	return &pluginVLM{providerName: config.Provider, modelName: config.ModelName, modelID: config.ModelID}
}

func (p *pluginVLM) GetModelName() string { return p.modelName }
func (p *pluginVLM) GetModelID() string   { return p.modelID }

func (p *pluginVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return "", err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.PredictVLM(callCtx, &pluginproto.ModelVLMRequest{Images: imgBytes, Prompt: prompt})
	if err != nil {
		return "", err
	}
	if resp.GetError() != "" {
		return "", fmt.Errorf("plugin: %s", resp.GetError())
	}
	return resp.GetText(), nil
}
