package chat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
)

type artifactStageContextKey struct{}

type ArtifactStage struct {
	Stage        string
	OutputSchema string
	Validate     func(string) error
}

func WithArtifactStage(ctx context.Context, stage ArtifactStage) context.Context {
	return context.WithValue(ctx, artifactStageContextKey{}, stage)
}

type ArtifactCacheConfig struct {
	TenantID  uint64
	Processor artifact.ProcessorIdentity
}

type artifactCachedChat struct {
	inner   Chat
	runtime *artifact.Runtime
	config  ArtifactCacheConfig
}

func ArtifactCacheConfigFromModel(model *types.Model, tenantID uint64) (ArtifactCacheConfig, bool) {
	if model == nil || tenantID == 0 || len(model.Parameters.CustomHeaders) > 0 {
		return ArtifactCacheConfig{}, false
	}
	endpoint := model.Parameters.BaseURL
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ArtifactCacheConfig{}, false
		}
	}
	extra := make(map[string]any, len(model.Parameters.ExtraConfig))
	for key, value := range model.Parameters.ExtraConfig {
		normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
		if strings.Contains(normalized, "key") ||
			strings.Contains(normalized, "token") ||
			strings.Contains(normalized, "secret") ||
			strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "signature") {
			return ArtifactCacheConfig{}, false
		}
		extra[key] = value
	}
	parameters := map[string]any{
		"interface_type":  model.Parameters.InterfaceType,
		"parameter_size":  model.Parameters.ParameterSize,
		"supports_vision": model.Parameters.SupportsVision,
	}
	if len(extra) > 0 {
		parameters["extra_config"] = extra
	}
	return ArtifactCacheConfig{
		TenantID: tenantID,
		Processor: artifact.ProcessorIdentity{
			ModelID:          model.ID,
			ModelName:        model.Name,
			Source:           string(model.Source),
			Provider:         model.Parameters.Provider,
			EndpointIdentity: endpoint,
			Parameters:       parameters,
		},
	}, true
}

func NewArtifactCachedChat(inner Chat, runtime *artifact.Runtime, config ArtifactCacheConfig) Chat {
	if inner == nil || runtime == nil || config.TenantID == 0 {
		return inner
	}
	return &artifactCachedChat{inner: inner, runtime: runtime, config: config}
}

func (c *artifactCachedChat) Chat(
	ctx context.Context,
	messages []Message,
	options *ChatOptions,
) (*types.ChatResponse, error) {
	stage, ok := ctx.Value(artifactStageContextKey{}).(ArtifactStage)
	if !ok || stage.Stage == "" || stage.OutputSchema == "" || !cacheableChatRequest(messages, options) {
		return c.inner.Chat(ctx, messages, options)
	}
	expected, err := c.expected(stage, messages, options)
	if err != nil {
		return c.inner.Chat(ctx, messages, options)
	}

	var providerResponse *types.ChatResponse
	value, err := c.runtime.LoadOrCompute(ctx, expected, func(ctx context.Context) ([]byte, error) {
		response, callErr := c.inner.Chat(ctx, messages, options)
		if callErr != nil {
			return nil, callErr
		}
		if response == nil {
			return nil, errors.New("chat provider returned a nil response")
		}
		if stage.Validate != nil {
			if err := stage.Validate(response.Content); err != nil {
				return nil, fmt.Errorf("validate chat stage %s output: %w", stage.Stage, err)
			}
		}
		if !utf8.ValidString(response.Content) {
			return nil, errors.New("chat provider response is not valid UTF-8")
		}
		providerResponse = response
		return []byte(response.Content), nil
	})
	if err != nil {
		return nil, err
	}
	if providerResponse == nil {
		return &types.ChatResponse{Content: string(value.Payload)}, nil
	}
	providerResponse.Content = string(value.Payload)
	return providerResponse, nil
}

func (c *artifactCachedChat) ChatStream(
	ctx context.Context,
	messages []Message,
	options *ChatOptions,
) (<-chan types.StreamResponse, error) {
	return c.inner.ChatStream(ctx, messages, options)
}

func (c *artifactCachedChat) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *artifactCachedChat) GetModelID() string {
	return c.inner.GetModelID()
}

func (c *artifactCachedChat) expected(
	stage ArtifactStage,
	messages []Message,
	options *ChatOptions,
) (artifact.Expected, error) {
	request := struct {
		Messages []Message    `json:"messages"`
		Options  *ChatOptions `json:"options"`
	}{
		Messages: messages,
		Options:  options,
	}
	directInputs := make([]artifact.DirectInput, 0, len(messages)+1)
	for index, message := range messages {
		canonical, err := artifact.CanonicalJSON(message)
		if err != nil {
			return artifact.Expected{}, err
		}
		directInputs = append(directInputs, artifact.DirectInput{
			Role:   fmt.Sprintf("message.%d", index),
			Digest: artifact.SHA256Hex(canonical),
		})
	}
	optionsCanonical, err := artifact.CanonicalJSON(options)
	if err != nil {
		return artifact.Expected{}, err
	}
	directInputs = append(directInputs, artifact.DirectInput{
		Role:   "options",
		Digest: artifact.SHA256Hex(optionsCanonical),
	})
	key, err := artifact.BuildKey(c.config.TenantID, artifact.KeyMaterial{
		KeyVersion:           1,
		Stage:                stage.Stage,
		DirectInputs:         directInputs,
		Processor:            c.config.Processor,
		RenderedRequest:      request,
		Options:              options,
		CanonicalizerVersion: artifact.CanonicalJSONVersion,
		OutputSchemaVersion:  stage.OutputSchema,
	})
	if err != nil {
		return artifact.Expected{}, err
	}
	return artifact.Expected{
		Key:   key,
		Codec: artifact.CodecTextUTF8V1,
		Validate: func(payload []byte) error {
			if !utf8.Valid(payload) {
				return errors.New("cached chat output is not valid UTF-8")
			}
			if stage.Validate != nil {
				return stage.Validate(string(payload))
			}
			return nil
		},
	}, nil
}

func cacheableChatRequest(messages []Message, options *ChatOptions) bool {
	if options == nil || len(options.Tools) > 0 || options.ToolChoice != "" {
		return false
	}
	for _, message := range messages {
		if len(message.MultiContent) > 0 ||
			len(message.Images) > 0 ||
			len(message.ToolCalls) > 0 ||
			message.ToolCallID != "" {
			return false
		}
	}
	return true
}
