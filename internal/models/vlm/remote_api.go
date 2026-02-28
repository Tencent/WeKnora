package vlm

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultMaxToks  = 5000
	defaultTemp     = float32(0.1)
)

// RemoteAPIVLM implements VLM via an OpenAI-compatible chat completions API.
type RemoteAPIVLM struct {
	modelName string
	modelID   string
	client    *openai.Client
	baseURL   string
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	apiCfg := openai.DefaultConfig(config.APIKey)
	if config.BaseURL != "" {
		apiCfg.BaseURL = config.BaseURL
	}
	apiCfg.HTTPClient = &http.Client{Timeout: defaultTimeout}

	return &RemoteAPIVLM{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		client:    openai.NewClientWithConfig(apiCfg),
		baseURL:   config.BaseURL,
	}, nil
}

// Predict sends an image with a text prompt to the OpenAI-compatible API.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
	mimeType := detectImageMIME(imgBytes)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)

	req := openai.ChatCompletionRequest{
		Model: v.modelName,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL:    dataURI,
							Detail: openai.ImageURLDetailAuto,
						},
					},
					{
						Type: openai.ChatMessagePartTypeText,
						Text: prompt,
					},
				},
			},
		},
		MaxTokens:   defaultMaxToks,
		Temperature: defaultTemp,
	}

	logger.Infof(ctx, "[VLM] Calling OpenAI-compatible API, model=%s, baseURL=%s, imageSize=%d",
		v.modelName, v.baseURL, len(imgBytes))

	resp, err := v.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI VLM request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI VLM returned no choices")
	}

	content := resp.Choices[0].Message.Content
	logger.Infof(ctx, "[VLM] OpenAI response received, len=%d", len(content))
	return content, nil
}

func (v *RemoteAPIVLM) GetModelName() string { return v.modelName }
func (v *RemoteAPIVLM) GetModelID() string   { return v.modelID }

// detectImageMIME returns the MIME type for the given image bytes.
func detectImageMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "image/png"
}
