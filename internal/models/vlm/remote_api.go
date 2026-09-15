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
	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	openai "github.com/sashabaranov/go-openai"
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

// RemoteAPIVLM implements VLM via an OpenAI-compatible chat completions API.
type RemoteAPIVLM struct {
	modelName   string
	modelID     string
	client      *openai.Client
	baseURL     string
	temperature float32
	imageDetail openai.ImageURLDetail
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	if err := validateVLMBaseURL(config.BaseURL); err != nil {
		return nil, err
	}

	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}

	var apiCfg openai.ClientConfig
	if providerName == provider.ProviderAzureOpenAI {
		apiCfg = openai.DefaultAzureConfig(config.APIKey, config.BaseURL)
		apiCfg.AzureModelMapperFunc = func(model string) string {
			return model
		}
		if config.Extra != nil {
			if v, ok := config.Extra["api_version"]; ok {
				if vs, ok := v.(string); ok && vs != "" {
					apiCfg.APIVersion = vs
				}
			}
		}
	} else {
		apiCfg = openai.DefaultConfig(config.APIKey)
		if config.BaseURL != "" {
			apiCfg.BaseURL = config.BaseURL
		}
	}
	httpClient := newVLMHTTPClient(vlmHTTPTimeout())

	// 注入用户自定义 HTTP header（类似 OpenAI Python SDK 的 extra_headers）
	if len(config.CustomHeaders) > 0 {
		apiCfg.HTTPClient = secutils.WrapHTTPClientWithHeaders(httpClient, config.CustomHeaders)
	} else {
		apiCfg.HTTPClient = httpClient
	}

	temp := defaultTemp
	if config.Extra != nil {
		if v, ok := config.Extra["temperature"]; ok {
			if vs, ok := v.(string); ok {
				if f, err := strconv.ParseFloat(vs, 32); err == nil {
					temp = float32(f)
				}
			}
		}
	}

	return &RemoteAPIVLM{
		modelName:   config.ModelName,
		modelID:     config.ModelID,
		client:      openai.NewClientWithConfig(apiCfg),
		baseURL:     config.BaseURL,
		temperature: temp,
		imageDetail: vlmImageDetail(providerName),
	}, nil
}

// Predict sends an image with a text prompt to the OpenAI-compatible API.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	var parts []openai.ChatMessagePart

	// Add text prompt first
	parts = append(parts, openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeText,
		Text: prompt,
	})

	// Add images
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) > 0 {
			mimeType := detectImageMIME(imgBytes)
			b64 := base64.StdEncoding.EncodeToString(imgBytes)
			dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    dataURI,
					Detail: v.imageDetail,
				},
			})
		}
	}

	req := openai.ChatCompletionRequest{
		Model: v.modelName,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:         openai.ChatMessageRoleUser,
				MultiContent: parts,
			},
		},
		MaxTokens:   defaultMaxToks,
		Temperature: v.temperature,
	}
	shapeReasoningVLMRequest(&req)

	totalImageSize := 0
	for _, img := range imgBytesList {
		totalImageSize += len(img)
	}
	logger.Infof(ctx, "[VLM] Calling OpenAI-compatible API, model=%s, baseURL=%s, numImages=%d, totalImageSize=%d",
		v.modelName, v.baseURL, len(imgBytesList), totalImageSize)

	resp, err := v.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI VLM request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI VLM returned no choices")
	}

	choice := resp.Choices[0]
	content := choice.Message.Content
	if strings.TrimSpace(content) == "" && choice.FinishReason == openai.FinishReasonLength {
		// Reasoning models spend max_completion_tokens on reasoning before any
		// visible output, so an exhausted budget yields an empty message rather
		// than an API error. Returning "" here would be recorded as
		// "no_extracted_content" and look identical to an image with no text.
		return "", fmt.Errorf(
			"OpenAI VLM returned no content: completion truncated at %d tokens (finish_reason=length)",
			defaultMaxToks,
		)
	}
	logger.Infof(ctx, "[VLM] OpenAI response received, len=%d", len(content))
	return content, nil
}

// shapeReasoningVLMRequest adapts an OpenAI-compatible VLM request for
// reasoning (o-series) and GPT-5 models, which reject `max_tokens` and every
// non-default sampling parameter.
//
// This mirrors shapeOpenAIReasoning in internal/models/chat, which fixed the
// same incompatibility on the chat path for issue #1283. The VLM path was
// never wired to it, so image OCR and captioning failed for every one of these
// models (issue #2537).
//
// Both quirks have to be handled together: migrating max_tokens alone still
// fails, because the VLM default temperature (0.1) is itself rejected.
func shapeReasoningVLMRequest(req *openai.ChatCompletionRequest) {
	if !provider.IsOpenAIReasoningOrGPT5Model(req.Model) {
		return
	}
	if req.MaxCompletionTokens == 0 && req.MaxTokens > 0 {
		req.MaxCompletionTokens = req.MaxTokens
	}
	req.MaxTokens = 0
	req.Temperature = 0
	req.TopP = 0
	req.FrequencyPenalty = 0
	req.PresencePenalty = 0
}

func (v *RemoteAPIVLM) GetModelName() string { return v.modelName }
func (v *RemoteAPIVLM) GetModelID() string   { return v.modelID }

// vlmImageDetail returns the provider-compatible image detail value.
//
// OpenAI accepts "auto", while MiniMax documents and validates
// low/default/high. Sending "auto" to MiniMax returns HTTP 400
// "invalid image detail: auto", which stalls multimodal processing through
// retries. Keep an env override for deployments that need provider-specific
// tuning without rebuilding.
func vlmImageDetail(providerName provider.ProviderName) openai.ImageURLDetail {
	if v := strings.TrimSpace(os.Getenv("VLM_IMAGE_DETAIL")); v != "" {
		return openai.ImageURLDetail(v)
	}
	if providerName == provider.ProviderMiniMax {
		return openai.ImageURLDetail("default")
	}
	return openai.ImageURLDetailAuto
}

// detectImageMIME returns the MIME type for the given image bytes.
func detectImageMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "image/png"
}
