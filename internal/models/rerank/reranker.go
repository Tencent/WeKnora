package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

// ScoreFormat indicates the semantic type of RelevanceScore returned by a reranker.
// This is used to decide whether normalization (sigmoid) is needed.
type ScoreFormat string

const (
	// ScoreFormatProbability means the model already returns scores in [0, 1].
	// No normalization is applied.
	ScoreFormatProbability ScoreFormat = "probability"
	// ScoreFormatLogit means the model returns raw logits (-inf, +inf).
	// Scores are always converted via sigmoid.
	ScoreFormatLogit ScoreFormat = "logit"
	// ScoreFormatAuto uses runtime range detection:
	// values already in [0, 1] are passed through; values outside are sigmoid-converted.
	// This is a fallback for unknown/generic deployments.
	ScoreFormatAuto ScoreFormat = "auto"
)

// Reranker defines the interface for document reranking
type Reranker interface {
	// Rerank reranks documents based on relevance to the query
	Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error)

	// GetModelName returns the model name
	GetModelName() string

	// GetModelID returns the model ID
	GetModelID() string
}

type RankResult struct {
	Index          int          `json:"index"`
	Document       DocumentInfo `json:"document"`
	RelevanceScore float64      `json:"relevance_score"`
}

// Handles the RelevanceScore field by checking if RelevanceScore exists first, otherwise falls back to Score field
func (r *RankResult) UnmarshalJSON(data []byte) error {
	var temp struct {
		Index          int          `json:"index"`
		Document       DocumentInfo `json:"document"`
		RelevanceScore *float64     `json:"relevance_score"`
		Score          *float64     `json:"score"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal rank result: %w", err)
	}

	r.Index = temp.Index
	r.Document = temp.Document

	if temp.RelevanceScore != nil {
		r.RelevanceScore = *temp.RelevanceScore
	} else if temp.Score != nil {
		r.RelevanceScore = *temp.Score
	}

	return nil
}

type DocumentInfo struct {
	Text string `json:"text"`
}

// UnmarshalJSON handles both string and object formats for DocumentInfo
func (d *DocumentInfo) UnmarshalJSON(data []byte) error {
	// First try to unmarshal as a string
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		d.Text = text
		return nil
	}

	// If that fails, try to unmarshal as an object with text field
	var temp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal DocumentInfo: %w", err)
	}

	d.Text = temp.Text
	return nil
}

type RerankerConfig struct {
	APIKey      string
	BaseURL     string
	ModelName   string
	Source      types.ModelSource
	ModelID     string
	Provider    string // Provider identifier: openai, aliyun, zhipu, siliconflow, jina, generic
	ExtraConfig map[string]string
	AppID       string
	AppSecret   string      // 加密值，工厂函数调用方传入，使用前已解密
	Format      ScoreFormat // Score format: probability, logit, auto
}

// Sigmoid converts a logit to a probability in (0, 1).
func Sigmoid(score float64) float64 {
	return 1.0 / (1.0 + math.Exp(-score))
}

// AutoNormalizeScore uses runtime range detection:
// values already in [0, 1] are passed through; values outside are sigmoid-converted.
// NOTE: This can misclassify logits that happen to fall inside [0, 1].
// Prefer explicit ScoreFormat configuration when the model type is known.
func AutoNormalizeScore(score float64) float64 {
	if score >= 0 && score <= 1 {
		return score
	}
	return Sigmoid(score)
}

// NormalizeByFormat normalizes a score according to the specified ScoreFormat.
func NormalizeByFormat(score float64, format ScoreFormat) float64 {
	switch format {
	case ScoreFormatLogit:
		return Sigmoid(score)
	case ScoreFormatProbability:
		return score
	default: // ScoreFormatAuto and any unknown value
		return AutoNormalizeScore(score)
	}
}

// NewReranker creates a reranker based on the configuration.
// It also sets a default ScoreFormat per provider when config.Format is empty.
func NewReranker(config *RerankerConfig) (Reranker, error) {
	r, err := newReranker(config)
	if err != nil || !logger.LLMDebugEnabled() {
		return r, err
	}
	return &debugReranker{inner: r}, nil
}

func newReranker(config *RerankerConfig) (Reranker, error) {
	// Use provider field if set, otherwise detect from URL using provider registry
	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}

	// Set default ScoreFormat per provider when not explicitly configured
	if config.Format == "" {
		switch providerName {
		case provider.ProviderNvidia:
			config.Format = ScoreFormatLogit // Nvidia reranking API returns raw logits
		case provider.ProviderAliyun, provider.ProviderZhipu, provider.ProviderJina:
			config.Format = ScoreFormatProbability // Known to return probabilities in [0, 1]
		default:
			config.Format = ScoreFormatAuto // Generic / OpenAI-compatible: unknown, use auto-detect
		}
	}

	switch providerName {
	case provider.ProviderAliyun:
		return NewAliyunReranker(config)
	case provider.ProviderZhipu:
		return NewZhipuReranker(config)
	case provider.ProviderJina:
		return NewJinaReranker(config)
	case provider.ProviderNvidia:
		return NewNvidiaReranker(config)
	case provider.ProviderWeKnoraCloud:
		return NewWeKnoraCloudReranker(config)
	default:
		return NewOpenAIReranker(config)
	}
}
