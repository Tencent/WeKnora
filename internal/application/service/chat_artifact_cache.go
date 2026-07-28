package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const chatArtifactCodecVersion byte = 1

var (
	errChatArtifactProvider = errors.New("chat artifact provider failed")
	errChatArtifactStore    = errors.New("chat artifact store failed")
	errChatArtifactCodec    = errors.New("chat artifact codec failed")
)

func isChatArtifactPipelineError(err error) bool {
	return errors.Is(err, errChatArtifactStore) || errors.Is(err, errChatArtifactCodec)
}

type chatArtifactRequest struct {
	tenantID             uint64
	stage                string
	keyVersion           uint16
	model                chat.Chat
	modelRevision        string
	messages             []chat.Message
	options              *chat.ChatOptions
	promptVersion        string
	canonicalizerVersion string
	valuePolicy          chatArtifactValuePolicy
}

type chatArtifactValuePolicy struct {
	canonicalize func(string) (value string, cacheable bool, err error)
	validate     func(string) error
}

func newChatArtifactKey(request chatArtifactRequest) (types.ProcessingArtifactKey, error) {
	if err := validateChatArtifactRequest(request); err != nil {
		return types.ProcessingArtifactKey{}, err
	}

	messages, err := json.Marshal(request.messages)
	if err != nil {
		return types.ProcessingArtifactKey{}, fmt.Errorf("%w: encode messages: %w", errChatArtifactCodec, err)
	}
	options, err := json.Marshal(request.options)
	if err != nil {
		return types.ProcessingArtifactKey{}, fmt.Errorf("%w: encode options: %w", errChatArtifactCodec, err)
	}

	return types.NewProcessingArtifactKey(
		request.tenantID,
		request.stage,
		request.keyVersion,
		[]byte(request.model.GetModelID()),
		[]byte(request.model.GetModelName()),
		[]byte(request.modelRevision),
		messages,
		options,
		[]byte(request.promptVersion),
		[]byte(request.canonicalizerVersion),
	)
}

func validateChatArtifactRequest(request chatArtifactRequest) error {
	if strings.TrimSpace(request.stage) == "" {
		return errors.New("chat artifact stage must not be empty")
	}
	if request.keyVersion == 0 {
		return errors.New("chat artifact key version must not be zero")
	}
	if request.model == nil {
		return errors.New("chat artifact model must not be nil")
	}
	if strings.TrimSpace(request.modelRevision) == "" {
		return errors.New("chat artifact model revision must not be empty")
	}
	if request.options == nil {
		return errors.New("chat artifact options must not be nil")
	}
	if strings.TrimSpace(request.promptVersion) == "" {
		return errors.New("chat artifact prompt version must not be empty")
	}
	if strings.TrimSpace(request.canonicalizerVersion) == "" {
		return errors.New("chat artifact canonicalizer version must not be empty")
	}
	return nil
}

func chatArtifactModelRevision(model *types.Model) (string, error) {
	if model == nil || strings.TrimSpace(model.ID) == "" || strings.TrimSpace(model.Name) == "" || model.UpdatedAt.IsZero() {
		return "", errors.New("chat artifact model is incomplete")
	}
	endpoint, err := normalizeChatArtifactEndpoint(model.Parameters.BaseURL)
	if err != nil {
		return "", err
	}
	if len(model.Parameters.CustomHeaders) > 0 {
		return "", errors.New("chat artifact model has unsupported custom headers")
	}

	extra, err := canonicalChatArtifactExtraConfig(model.Parameters.ExtraConfig)
	if err != nil {
		return "", err
	}
	descriptor := strings.Join([]string{
		model.ID,
		model.Name,
		string(model.Type),
		strings.ToLower(string(model.Source)),
		endpoint,
		strings.ToLower(strings.TrimSpace(model.Parameters.InterfaceType)),
		strings.TrimSpace(model.Parameters.ParameterSize),
		strings.ToLower(strings.TrimSpace(model.Parameters.Provider)),
		fmt.Sprintf("%t", model.Parameters.SupportsVision),
		extra,
		model.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	digest := sha256.Sum256([]byte(descriptor))
	return hex.EncodeToString(digest[:]), nil
}

func normalizeChatArtifactEndpoint(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.TrimSpace(raw) != raw {
		return "", errors.New("invalid chat artifact endpoint")
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return "", errors.New("invalid chat artifact endpoint")
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", errors.New("invalid chat artifact endpoint")
	}
	endpoint.Host = strings.ToLower(endpoint.Host)
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	return endpoint.String(), nil
}

func canonicalChatArtifactExtraConfig(extraConfig map[string]string) (string, error) {
	if len(extraConfig) == 0 {
		return "", nil
	}
	allowed := map[string]struct{}{
		"api_version":                   {},
		"remote_model_name":             {},
		chat.ExtraConfigThinkingControl: {},
	}
	keys := make([]string, 0, len(extraConfig))
	for key := range extraConfig {
		if _, ok := allowed[key]; !ok {
			return "", errors.New("chat artifact model has unsupported extra config")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := extraConfig[key]
		if !utf8.ValidString(value) {
			return "", errors.New("chat artifact model has invalid extra config")
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, "\x00"), nil
}

func encodeChatArtifactCompletion(completion string) ([]byte, error) {
	if !utf8.ValidString(completion) {
		return nil, fmt.Errorf("%w: completion is not valid UTF-8", errChatArtifactCodec)
	}
	payload := make([]byte, 1+len(completion))
	payload[0] = chatArtifactCodecVersion
	copy(payload[1:], completion)
	return payload, nil
}

func decodeChatArtifactCompletion(payload []byte) (string, error) {
	if len(payload) == 0 || payload[0] != chatArtifactCodecVersion {
		return "", fmt.Errorf("%w: unsupported or missing version", errChatArtifactCodec)
	}
	completion := string(payload[1:])
	if !utf8.ValidString(completion) {
		return "", fmt.Errorf("%w: completion is not valid UTF-8", errChatArtifactCodec)
	}
	return completion, nil
}

func completeChatArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	request chatArtifactRequest,
) (string, bool, bool, error) {
	var key types.ProcessingArtifactKey
	if store != nil {
		var err error
		key, err = newChatArtifactKey(request)
		if err != nil {
			return "", false, false, err
		}
		payload, hit, err := store.Get(ctx, key)
		if err != nil {
			return "", false, false, fmt.Errorf("%w: get: %w", errChatArtifactStore, err)
		}
		if hit {
			completion, decodeErr := decodeChatArtifactCompletion(payload)
			if decodeErr == nil && validateChatArtifactValue(request.valuePolicy, completion) == nil {
				return completion, true, false, nil
			}
			if err := store.Invalidate(ctx, key, payload); err != nil {
				return "", false, false, fmt.Errorf("%w: invalidate: %w", errChatArtifactStore, err)
			}
		}
	}

	completion, err := callChatArtifactProvider(ctx, request)
	if err != nil {
		return "", false, true, err
	}
	completion, cacheable, err := canonicalizeChatArtifactValue(request.valuePolicy, completion)
	if err != nil {
		return "", false, true, err
	}
	if store == nil || !cacheable {
		return completion, false, true, nil
	}
	payload, err := encodeChatArtifactCompletion(completion)
	if err != nil {
		return "", false, true, err
	}
	canonical, _, err := store.PutIfAbsent(ctx, key, payload)
	if err != nil {
		return "", false, true, fmt.Errorf("%w: put: %w", errChatArtifactStore, err)
	}
	canonicalCompletion, err := decodeChatArtifactCompletion(canonical)
	if err != nil || validateChatArtifactValue(request.valuePolicy, canonicalCompletion) != nil {
		if invalidateErr := store.Invalidate(ctx, key, canonical); invalidateErr != nil {
			return "", false, true, fmt.Errorf("%w: invalidate: %w", errChatArtifactStore, invalidateErr)
		}
		return completion, false, true, nil
	}
	return canonicalCompletion, false, true, nil
}

func canonicalizeChatArtifactValue(policy chatArtifactValuePolicy, completion string) (string, bool, error) {
	if policy.canonicalize == nil {
		return completion, true, nil
	}
	value, cacheable, err := policy.canonicalize(completion)
	if err != nil {
		return "", false, fmt.Errorf("%w: canonicalize completion: %w", errChatArtifactCodec, err)
	}
	return value, cacheable, nil
}

func validateChatArtifactValue(policy chatArtifactValuePolicy, value string) error {
	if policy.validate == nil {
		return nil
	}
	return policy.validate(value)
}

func callChatArtifactProvider(ctx context.Context, request chatArtifactRequest) (string, error) {
	if request.model == nil {
		return "", fmt.Errorf("%w: model must not be nil", errChatArtifactProvider)
	}
	response, err := request.model.Chat(ctx, request.messages, request.options)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errChatArtifactProvider, err)
	}
	if response == nil {
		return "", fmt.Errorf("%w: model returned nil response", errChatArtifactProvider)
	}
	return response.Content, nil
}
