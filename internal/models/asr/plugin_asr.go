package asr

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// pluginASR adapts an external model plugin (gRPC ModelPlugin) to the host ASR
// interface.
type pluginASR struct {
	providerName string
	modelName    string
	modelID      string
}

func newPluginASR(config *Config) ASR {
	return &pluginASR{providerName: config.Provider, modelName: config.ModelName, modelID: config.ModelID}
}

func (p *pluginASR) GetModelName() string { return p.modelName }
func (p *pluginASR) GetModelID() string   { return p.modelID }

func (p *pluginASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.Transcribe(callCtx, &pluginproto.ModelASRRequest{Audio: audioBytes, FileName: fileName})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("plugin: %s", resp.GetError())
	}
	return &TranscriptionResult{Text: resp.GetText()}, nil
}
