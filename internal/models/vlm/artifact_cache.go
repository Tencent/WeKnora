package vlm

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

// ArtifactStage explicitly opts an ingestion VLM call into artifact reuse.
// Interactive VLM calls do not carry this marker and always reach the provider.
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

type artifactCachedVLM struct {
	inner   VLM
	runtime *artifact.Runtime
	config  ArtifactCacheConfig
}

// ArtifactCacheConfigFromModel keeps only effective, non-secret provider
// identity. Unknown provider options and custom headers disable reuse because
// they cannot safely be classified as semantic or credential-only.
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

func NewArtifactCachedVLM(inner VLM, runtime *artifact.Runtime, config ArtifactCacheConfig) VLM {
	if inner == nil || runtime == nil || config.TenantID == 0 {
		return inner
	}
	return &artifactCachedVLM{inner: inner, runtime: runtime, config: config}
}

func (v *artifactCachedVLM) Predict(
	ctx context.Context,
	images [][]byte,
	prompt string,
) (string, error) {
	stage, ok := ctx.Value(artifactStageContextKey{}).(ArtifactStage)
	if !ok || stage.Stage == "" || stage.OutputSchema == "" || len(images) == 0 {
		return v.inner.Predict(ctx, images, prompt)
	}
	expected, err := v.expected(stage, images, prompt)
	if err != nil {
		return v.inner.Predict(ctx, images, prompt)
	}

	value, err := v.runtime.LoadOrCompute(ctx, expected, func(ctx context.Context) ([]byte, error) {
		output, callErr := v.inner.Predict(ctx, images, prompt)
		if callErr != nil {
			return nil, callErr
		}
		if !utf8.ValidString(output) {
			return nil, errors.New("VLM provider response is not valid UTF-8")
		}
		if stage.Validate != nil {
			if err := stage.Validate(output); err != nil {
				return nil, fmt.Errorf("validate VLM stage %s output: %w", stage.Stage, err)
			}
		}
		return []byte(output), nil
	})
	if err != nil {
		return "", err
	}
	return string(value.Payload), nil
}

func (v *artifactCachedVLM) GetModelName() string {
	return v.inner.GetModelName()
}

func (v *artifactCachedVLM) GetModelID() string {
	return v.inner.GetModelID()
}

func (v *artifactCachedVLM) expected(
	stage ArtifactStage,
	images [][]byte,
	prompt string,
) (artifact.Expected, error) {
	type imageDescriptor struct {
		Digest string `json:"digest"`
		Bytes  int    `json:"bytes"`
	}
	descriptors := make([]imageDescriptor, len(images))
	directInputs := make([]artifact.DirectInput, 0, len(images)+1)
	for index, image := range images {
		digest := artifact.SHA256Hex(image)
		descriptors[index] = imageDescriptor{Digest: digest, Bytes: len(image)}
		directInputs = append(directInputs, artifact.DirectInput{
			Role:   fmt.Sprintf("image.%d", index),
			Digest: digest,
		})
	}
	directInputs = append(directInputs, artifact.DirectInput{
		Role:   "prompt",
		Digest: artifact.SHA256Hex([]byte(prompt)),
	})
	key, err := artifact.BuildKey(v.config.TenantID, artifact.KeyMaterial{
		KeyVersion:   1,
		Stage:        stage.Stage,
		DirectInputs: directInputs,
		Processor:    v.config.Processor,
		RenderedRequest: map[string]any{
			"images": descriptors,
			"prompt": prompt,
		},
		Options:              map[string]any{},
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
				return errors.New("cached VLM output is not valid UTF-8")
			}
			if stage.Validate != nil {
				return stage.Validate(string(payload))
			}
			return nil
		},
	}, nil
}
