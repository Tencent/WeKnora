package vlm

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
)

// VLM defines the interface for Vision Language Model operations.
type VLM interface {
	// Predict sends an image with a text prompt to the VLM and returns the generated text.
	Predict(ctx context.Context, imgBytes []byte, prompt string) (string, error)

	GetModelName() string
	GetModelID() string
}

// Config holds the configuration needed to create a VLM instance.
type Config struct {
	Source        types.ModelSource
	BaseURL       string
	ModelName     string
	APIKey        string
	ModelID       string
	InterfaceType string // "ollama" or "openai" (default)
	Provider      string
	Extra         map[string]any
}

// NewVLM creates a VLM instance based on the provided configuration.
func NewVLM(config *Config, ollamaService *ollama.OllamaService) (VLM, error) {
	ifType := strings.ToLower(config.InterfaceType)

	if ifType == "ollama" || config.Source == types.ModelSourceLocal {
		return NewOllamaVLM(config, ollamaService)
	}
	return NewRemoteAPIVLM(config)
}

// NewVLMFromLegacyConfig creates a VLM from a legacy VLMConfig (inline BaseURL/APIKey/ModelName).
func NewVLMFromLegacyConfig(vlmCfg types.VLMConfig, ollamaService *ollama.OllamaService) (VLM, error) {
	if !vlmCfg.IsEnabled() {
		return nil, fmt.Errorf("VLM config is not enabled")
	}

	ifType := vlmCfg.InterfaceType
	if ifType == "" {
		ifType = "openai"
	}

	source := types.ModelSourceRemote
	if strings.EqualFold(ifType, "ollama") {
		source = types.ModelSourceLocal
	}

	return NewVLM(&Config{
		Source:        source,
		BaseURL:       vlmCfg.BaseURL,
		ModelName:     vlmCfg.ModelName,
		APIKey:        vlmCfg.APIKey,
		InterfaceType: ifType,
	}, ollamaService)
}
