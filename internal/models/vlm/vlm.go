package vlm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrTruncatedCompletion means the model exhausted its output budget
// (finish_reason=length). Both the buffered and the streaming path surface it,
// so callers can errors.Is it regardless of which path served the request.
var ErrTruncatedCompletion = errors.New("VLM completion truncated")

// VLM defines the interface for Vision Language Model operations.
type VLM interface {
	// Predict sends one or more images with a text prompt to the VLM and returns the generated text.
	Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error)
	// PredictWithOptions sends the same request with per-call tunings that the
	// client-level config cannot express. Implementations must honour it; the
	// decorators below pass it through untouched.
	PredictWithOptions(ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions) (string, error)

	GetModelName() string
	GetModelID() string
}

// PredictOptions tunes a single image request. Every field is optional and nil
// means "whatever this client was built with", which is what keeps the model
// row, the knowledge base and the per-action switch from overriding each other
// by accident: an unset field never writes anything.
type PredictOptions struct {
	// Thinking pins the thinking switch for this call. Leaving a reasoning model
	// on costs a vision call more than it returns it: the completion budget goes
	// to reasoning, and at the vision default temperature the answer can come
	// back empty after the model has already listed the answer in its reasoning.
	// An OCR caller cannot tell that apart from an image with no text, so the
	// switch is per-action rather than per-model.
	Thinking *bool
	// Priority overrides the caller-derived priority for this call (rarely set;
	// the manager derives priority from the caller identity by default). A
	// higher value wins; 0 means "use the caller policy".
	Priority *int
	// OnChunk, when non-nil, receives streamed text chunks (text, done, err).
	// The manager always streams from the server for observation; OnChunk only
	// decides whether those chunks are forwarded to the caller. When nil, the
	// manager buffers the response and returns the full string — the caller is
	// unaffected. End-to-end UX rendering of these chunks is Phase 2.
	OnChunk func(text string, done bool, err error)
	// StatusSink, when non-nil, is notified on lifecycle phase transitions
	// (queued → admitted → inflight → first-token → done/failed). It lets a
	// caller show progress (e.g. "analysing image…") without owning the
	// admission logic.
	StatusSink func(phase Phase, info PhaseInfo)
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
	// MaxConcurrency caps concurrent background calls to this model; 0 falls
	// back to the process-wide default (see limiter.GateN).
	MaxConcurrency int
	// RequestsPerMinute caps the request rate to this model; 0 means
	// unlimited. Used by the vlm manager to smooth account-level bursts.
	RequestsPerMinute int
	// Spec carries per-row catalog overrides (protocol, compat, levels). VLM
	// calls now go through the chat factory, so the override has to travel
	// with them — otherwise /models reports capabilities computed WITH the
	// override while the actual request is built without it.
	Spec  *types.ModelSpecOverride
	Extra map[string]any
	// CustomHeaders 允许在调用远程 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string
	AppID         string
	AppSecret     string
}

// ConfigFromModel 根据 types.Model 构造 vlm.Config。
// 生产路径（从 DB 拉起）和测试连接路径（临时表单）共享这份映射。
// appID / appSecret 是已解密的 WeKnoraCloud 凭证，调用方负责传入。
// InterfaceType 会根据 source / 模型参数自动回退到合理默认值。
func ConfigFromModel(m *types.Model, appID, appSecret string) *Config {
	if m == nil {
		return nil
	}
	ifType := m.Parameters.InterfaceType
	if ifType == "" {
		if m.Source == types.ModelSourceLocal {
			ifType = "ollama"
		} else {
			ifType = "openai"
		}
	}
	return &Config{
		ModelID:           m.ID,
		APIKey:            m.Parameters.APIKey,
		BaseURL:           m.Parameters.BaseURL,
		ModelName:         m.Name,
		Source:            m.Source,
		InterfaceType:     ifType,
		Provider:          m.Parameters.Provider,
		MaxConcurrency:    m.Parameters.MaxConcurrency,
		RequestsPerMinute: m.Parameters.RequestsPerMinute,
		Spec:              m.Parameters.Spec,
		Extra:             stringMapToAnyMap(m.Parameters.ExtraConfig),
		CustomHeaders:     m.Parameters.CustomHeaders,
		AppID:             appID,
		AppSecret:         appSecret,
	}
}

func stringMapToAnyMap(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// UsageReporter is an optional sink for token usage the model returns. The vlm
// manager checks (via type assertion) whether the constructed inner exposes it,
// and forwards usage to it when present. It is the surfacing side of the
// token-accounting the manager collects for adaptive control.
type UsageReporter interface {
	Report(modelID string, usage types.TokenUsage)
}

// NewVLM creates a VLM instance based on the provided configuration.
func NewVLM(config *Config, ollamaService *ollama.OllamaService) (VLM, error) {
	v, err := newVLM(config, ollamaService)
	if err != nil {
		return v, err
	}
	if logger.LLMDebugEnabled() {
		v = &debugVLM{inner: v}
	}
	v, err = wrapVLMLangfuse(v, nil)
	// Outermost: the vlm manager holds the per-model concurrency slot, applies
	// priority / rate admission and adaptive control around the real provider
	// round-trip, so the wait is excluded from debug/langfuse timing.
	return wrapVLMManager(v, config, err)
}

func newVLM(config *Config, ollamaService *ollama.OllamaService) (VLM, error) {
	ifType := strings.ToLower(config.InterfaceType)

	if ifType == "ollama" || config.Source == types.ModelSourceLocal {
		return NewOllamaVLM(config, ollamaService)
	}

	providerID := config.Provider
	if providerID == "" {
		providerID = modelruntime.DetectByURL(config.BaseURL)
	}
	if providerID == providers.WeKnoraCloudID {
		return NewWeKnoraCloudVLM(config)
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
