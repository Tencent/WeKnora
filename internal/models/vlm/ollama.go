package vlm

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	ollamaapi "github.com/ollama/ollama/api"
)

// OllamaVLM implements VLM via the local Ollama service.
type OllamaVLM struct {
	modelName     string
	modelID       string
	ollamaService *ollama.OllamaService
}

// NewOllamaVLM creates an Ollama-backed VLM instance.
func NewOllamaVLM(config *Config, ollamaService *ollama.OllamaService) (*OllamaVLM, error) {
	if ollamaService == nil {
		return nil, fmt.Errorf("ollama service is required for local VLM model")
	}
	return &OllamaVLM{
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		ollamaService: ollamaService,
	}, nil
}

// Predict sends an image with a text prompt to the Ollama vision model.
func (v *OllamaVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	streamFlag := false
	var images []ollamaapi.ImageData
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) > 0 {
			images = append(images, imgBytes)
		}
	}
	
	chatReq := &ollamaapi.ChatRequest{
		Model: v.modelName,
		Messages: []ollamaapi.Message{
			{
				Role:    "user",
				Content: prompt,
				Images:  images,
			},
		},
		Stream:  &streamFlag,
		Options: map[string]interface{}{"temperature": 0.1},
	}

	totalImageSize := 0
	for _, img := range imgBytesList {
		totalImageSize += len(img)
	}
	logger.Infof(ctx, "[VLM] Calling Ollama API, model=%s, numImages=%d, totalImageSize=%d", v.modelName, len(images), totalImageSize)

	var result string
	err := v.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		result = resp.Message.Content
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("Ollama VLM request: %w", err)
	}

	logger.Infof(ctx, "[VLM] Ollama response received, len=%d", len(result))
	return result, nil
}

func (v *OllamaVLM) GetModelName() string { return v.modelName }
func (v *OllamaVLM) GetModelID() string   { return v.modelID }
