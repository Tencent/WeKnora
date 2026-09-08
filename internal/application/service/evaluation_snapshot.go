package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var jiebaDictionaryFiles = []string{
	"jieba.dict.utf8",
	"hmm_model.utf8",
	"user.dict.utf8",
	"idf.utf8",
	"stop_words.utf8",
}

func evaluationTokenizerSnapshot() (types.EvaluationTokenizerSnapshot, error) {
	directory := os.Getenv("JIEBA_DICT_DIR")
	if directory == "" {
		return types.EvaluationTokenizerSnapshot{Name: "jieba", DictionaryMode: "builtin"}, nil
	}

	hash := sha256.New()
	for _, name := range jiebaDictionaryFiles {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return types.EvaluationTokenizerSnapshot{}, fmt.Errorf(
				"fingerprint custom jieba dictionary %s: %w", name, err,
			)
		}
		// Include the stable basename and byte length so concatenation cannot make
		// distinct file sets ambiguous. The absolute local path is never stored.
		if _, err := fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data)); err != nil {
			return types.EvaluationTokenizerSnapshot{}, err
		}
		if _, err := hash.Write(data); err != nil {
			return types.EvaluationTokenizerSnapshot{}, err
		}
	}
	return types.EvaluationTokenizerSnapshot{
		Name: "jieba", DictionaryMode: "custom", DictionaryFingerprint: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func loadEvaluationModelSnapshots(
	ctx context.Context,
	modelService interfaces.ModelService,
	embeddingModelID, chatModelID, rerankModelID, summaryModelID string,
) (types.EvaluationModelsSnapshot, error) {
	result := types.EvaluationModelsSnapshot{
		EmbeddingModelID: embeddingModelID,
		ChatModelID:      chatModelID,
		SummaryModelID:   summaryModelID,
	}
	if rerankModelID != "" {
		id := rerankModelID
		result.RerankModelID = &id
	}

	cache := make(map[string]*types.Model, 4)
	load := func(id string) (*types.Model, error) {
		if id == "" {
			return nil, nil
		}
		if model, ok := cache[id]; ok {
			return model, nil
		}
		model, err := modelService.GetModelByID(ctx, id)
		if err != nil {
			return nil, err
		}
		cache[id] = model
		return model, nil
	}

	embedding, err := load(embeddingModelID)
	if err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot embedding model: %w", err)
	}
	chat, err := load(chatModelID)
	if err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot chat model: %w", err)
	}
	rerank, err := load(rerankModelID)
	if err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot rerank model: %w", err)
	}
	summary, err := load(summaryModelID)
	if err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot summary model: %w", err)
	}

	if result.Embedding, err = configuredModelSnapshot(embedding, true); err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot embedding model endpoint: %w", err)
	}
	if result.Chat, err = configuredModelSnapshot(chat, false); err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot chat model endpoint: %w", err)
	}
	if result.Rerank, err = configuredModelSnapshot(rerank, false); err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot rerank model endpoint: %w", err)
	}
	if result.Summary, err = configuredModelSnapshot(summary, false); err != nil {
		return types.EvaluationModelsSnapshot{}, fmt.Errorf("snapshot summary model endpoint: %w", err)
	}
	return result, nil
}

func configuredModelSnapshot(
	model *types.Model, includeEmbeddingParameters bool,
) (*types.EvaluationConfiguredModelSnapshot, error) {
	if model == nil {
		return nil, nil
	}
	snapshot := &types.EvaluationConfiguredModelSnapshot{
		ID: model.ID, Name: model.Name, Type: string(model.Type), Source: string(model.Source),
		Provider: model.Parameters.Provider, InterfaceType: model.Parameters.InterfaceType,
	}
	if model.Source != types.ModelSourceLocal {
		fingerprint, err := modelEndpointFingerprint(model)
		if err != nil {
			return nil, err
		}
		snapshot.EndpointFingerprint = fingerprint
	}
	if includeEmbeddingParameters {
		params := model.Parameters.EmbeddingParameters
		snapshot.Embedding = &types.EvaluationEmbeddingSnapshot{
			Dimension: params.Dimension, TruncatePromptTokens: params.TruncatePromptTokens,
			SupportsDimensionOverride: params.SupportsDimensionOverride,
		}
	}
	return snapshot, nil
}

func modelEndpointFingerprint(model *types.Model) (string, error) {
	endpoint, err := effectiveModelBaseURL(model)
	if err != nil {
		return "", err
	}
	identity, err := normalizeEndpointIdentity(endpoint)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}

// effectiveModelBaseURL follows the common runtime resolution rule: an
// explicitly configured BaseURL wins; otherwise use the selected provider's
// per-model-type default. Generic remote implementations fall back to OpenAI.
func effectiveModelBaseURL(model *types.Model) (string, error) {
	if model == nil {
		return "", fmt.Errorf("model is nil")
	}
	if endpoint := strings.TrimSpace(model.Parameters.BaseURL); endpoint != "" {
		return endpoint, nil
	}

	providerName := provider.ProviderName(strings.ToLower(strings.TrimSpace(model.Parameters.Provider)))
	if providerName == "" || providerName == provider.ProviderGeneric {
		providerName = provider.ProviderOpenAI
	}
	selected, ok := provider.Get(providerName)
	if !ok {
		return "", fmt.Errorf(
			"remote model %q has no configured endpoint and unknown provider %q",
			model.ID, providerName,
		)
	}
	endpoint := strings.TrimSpace(selected.Info().GetDefaultURL(model.Type))
	if endpoint == "" {
		return "", fmt.Errorf("remote model %q has no effective endpoint for provider %q and type %q",
			model.ID, providerName, model.Type)
	}
	return endpoint, nil
}

// normalizeEndpointIdentity returns only a stable scheme/host/path identity.
// User info, query parameters, and fragments are intentionally discarded
// before hashing so credential-bearing URLs cannot leak through the snapshot.
func normalizeEndpointIdentity(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("endpoint must use http or https")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("endpoint must include a host")
	}
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	return scheme + "://" + host + path, nil
}
