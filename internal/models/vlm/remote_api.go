package vlm

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// defaultTimeout is the fallback HTTP timeout for a single VLM request.
	// Dense scanned-PDF OCR (full-page text + layout extraction) can take well
	// over a minute on slow endpoints, so this is intentionally generous and
	// can be raised further via VLM_HTTP_TIMEOUT_SECONDS.
	defaultTimeout = 180 * time.Second
	defaultMaxToks = 5000
	defaultTemp    = float32(0.1)
)

// vlmHTTPTimeout returns the HTTP client timeout for VLM requests, read from
// the VLM_HTTP_TIMEOUT_SECONDS env var when set (and positive), falling back to
// defaultTimeout otherwise. Shared by all OpenAI-compatible VLM backends.
func vlmHTTPTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VLM_HTTP_TIMEOUT_SECONDS")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultTimeout
}

// RemoteAPIVLM implements VLM on top of the catalog-driven chat client: a
// vision call is a chat call whose user turn carries images, so the vendor
// dialect (max_tokens field, sampling restrictions, protocol) is resolved
// exactly like it is for chat models.
type RemoteAPIVLM struct {
	modelName   string
	modelID     string
	chat        chat.Chat
	temperature float64
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	if err := validateVLMBaseURL(config.BaseURL); err != nil {
		return nil, err
	}

	temp := float64(defaultTemp)
	extra := make(map[string]string, len(config.Extra))
	for k, v := range config.Extra {
		if s, ok := v.(string); ok {
			extra[k] = s
		}
	}
	if raw, ok := extra["temperature"]; ok {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			temp = f
		}
	}

	client, err := chat.NewRemoteChat(&chat.ChatConfig{
		Source:        types.ModelSourceRemote,
		BaseURL:       config.BaseURL,
		ModelName:     config.ModelName,
		APIKey:        config.APIKey,
		ModelID:       config.ModelID,
		Provider:      config.Provider,
		ExtraConfig:   extra,
		CustomHeaders: config.CustomHeaders,
		AppID:         config.AppID,
		AppSecret:     config.AppSecret,
		Spec:          config.Spec,
	})
	if err != nil {
		return nil, err
	}
	return &RemoteAPIVLM{
		modelName:   config.ModelName,
		modelID:     config.ModelID,
		chat:        client,
		temperature: temp,
	}, nil
}

// Predict sends images with a text prompt through the chat client.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	parts := []chat.MessageContentPart{{Type: "text", Text: prompt}}
	totalImageSize := 0
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) == 0 {
			continue
		}
		totalImageSize += len(imgBytes)
		dataURI := fmt.Sprintf("data:%s;base64,%s",
			detectImageMIME(imgBytes), base64.StdEncoding.EncodeToString(imgBytes))
		parts = append(parts, chat.MessageContentPart{
			Type: "image_url", ImageURL: &chat.ImageURL{URL: dataURI, Detail: "auto"},
		})
	}
	logger.Infof(ctx, "[VLM] Calling chat protocol, model=%s, numImages=%d, totalImageSize=%d",
		v.modelName, len(imgBytesList), totalImageSize)

	ctx, cancel := context.WithTimeout(ctx, vlmHTTPTimeout())
	defer cancel()
	resp, err := v.chat.Chat(ctx, []chat.Message{{Role: "user", MultiContent: parts}}, &chat.ChatOptions{
		Temperature: v.temperature,
		MaxTokens:   defaultMaxToks,
	})
	if err != nil {
		return "", fmt.Errorf("VLM request: %w", err)
	}
	content := resp.Content
	if strings.TrimSpace(content) == "" && resp.FinishReason == "length" {
		// Reasoning models spend the completion budget on reasoning before any
		// visible output, so an exhausted budget yields an empty message rather
		// than an API error. Returning "" here would be recorded as
		// "no_extracted_content" and look identical to an image with no text.
		return "", fmt.Errorf(
			"VLM returned no content: completion truncated at %d tokens (finish_reason=length)",
			defaultMaxToks,
		)
	}
	logger.Infof(ctx, "[VLM] response received, len=%d", len(content))
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
