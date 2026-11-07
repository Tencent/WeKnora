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
	// defaultMaxToks is the fallback completion budget for a single VLM
	// request. The budget covers hidden thinking tokens too, so a reasoning
	// vision model transcribing a dense page (TOC, formulas, large tables)
	// exhausts anything smaller and the page is dropped at
	// finish_reason=length. It is lowered to the model row's declared output
	// limit when the row knows one, and raised per model via
	// extra_config.max_tokens or process wide via VLM_MAX_TOKENS.
	defaultMaxToks = 16384
	// weKnoraCloudMaxToks keeps the hosted WeKnoraCloud VLM at its historical
	// budget: the managed endpoint documents its own limits, so raising the
	// self-hosted default must not silently change what those requests send.
	weKnoraCloudMaxToks = 5000
	defaultTemp         = float32(0.1)
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

// vlmMaxTokens reads the process-wide VLM_MAX_TOKENS override: a set and
// positive value wins, everything else reports 0 so the caller falls through
// to the remaining layers. The budget covers thinking tokens as well, so a
// reasoning vision model can exhaust the default on a dense page and fail OCR
// with finish_reason=length; this makes the cap adjustable without a rebuild.
func vlmMaxTokens() int {
	if v := strings.TrimSpace(os.Getenv("VLM_MAX_TOKENS")); v != "" {
		if tokens, err := strconv.Atoi(v); err == nil && tokens > 0 {
			return tokens
		}
	}
	return 0
}

// remoteAPIBudget resolves the completion budget for one remote VLM request,
// most specific layer first: a valid extra_config.max_tokens override (this
// model), a valid VLM_MAX_TOKENS value (this process), then the built-in
// default capped by the output limit the model row declares. Non-positive
// values mean "unset" at every layer, so an invalid override never disables a
// safer fallback below it.
func remoteAPIBudget(extraMaxToks, envMaxToks, modelLimit int) int {
	if extraMaxToks > 0 {
		return extraMaxToks
	}
	if envMaxToks > 0 {
		return envMaxToks
	}
	if modelLimit > 0 && modelLimit < defaultMaxToks {
		return modelLimit
	}
	return defaultMaxToks
}

// extraConfigMaxToks reads a positive integer override from the model's
// extra_config. A missing, non-numeric or non-positive value reports 0, which
// callers treat as "unset".
func extraConfigMaxToks(extra map[string]string, key string) int {
	if raw, ok := extra[key]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
			return n
		}
	}
	return 0
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
	// extraMaxToks is the parsed extra_config.max_tokens override for this
	// model; 0 when unset, so the budget falls through to the wider layers.
	extraMaxToks int
	// modelMaxOutputToks is the per-response output limit the model row
	// declares (spec override wins); 0 when unknown, which leaves the built-in
	// default uncapped.
	modelMaxOutputToks int
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
		modelName:          config.ModelName,
		modelID:            config.ModelID,
		chat:               client,
		temperature:        temp,
		extraMaxToks:       extraConfigMaxToks(extra, "max_tokens"),
		modelMaxOutputToks: config.MaxOutputTokens,
	}, nil
}

// Predict sends images with a text prompt through the chat client.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	return v.PredictWithOptions(ctx, imgBytesList, prompt, nil)
}

// PredictWithOptions sends the same request, carrying a per-call thinking
// switch into the chat client. The switch rides on Options.Thinking, which the
// protocol layer turns into whatever key this vendor uses, so a model that only
// understands chat_template_kwargs still gets that spelling rather than a
// bare enable_thinking the server would ignore.
func (v *RemoteAPIVLM) PredictWithOptions(
	ctx context.Context, imgBytesList [][]byte, prompt string, opts *PredictOptions,
) (string, error) {
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

	maxTokens := remoteAPIBudget(v.extraMaxToks, vlmMaxTokens(), v.modelMaxOutputToks)
	chatOpts := &chat.ChatOptions{
		Temperature: v.temperature,
		MaxTokens:   maxTokens,
	}
	// Only an explicit preference is forwarded. The protocol layer skips the
	// thinking fields entirely when nobody asked for a level, which is what
	// leaves an always-on reasoning model running as its vendor intends.
	if opts != nil && opts.Thinking != nil {
		chatOpts.Thinking = opts.Thinking
	}

	ctx, cancel := context.WithTimeout(ctx, vlmHTTPTimeout())
	defer cancel()
	resp, err := v.chat.Chat(ctx, []chat.Message{{Role: "user", MultiContent: parts}}, chatOpts)
	if err != nil {
		return "", fmt.Errorf("VLM request: %w", err)
	}
	content := resp.Content
	if resp.FinishReason == "length" {
		// Non-empty partial output is still incomplete and must not be indexed
		// as successful OCR. Returning an error also marks the model trace failed.
		return "", fmt.Errorf("%w at %d tokens (finish_reason=length)", ErrTruncatedCompletion, maxTokens)
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
