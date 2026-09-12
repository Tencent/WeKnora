// Package livecache implements a fixed, budgeted cache experiment. Network
// execution is opt-in; constructing and reviewing a plan needs no credentials.
package livecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// Fixed endpoint, model identities, request limits and prices define the reviewed plan.
const (
	Endpoint                    = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	ChatModel                   = "qwen3.7-flash"
	EmbeddingModel              = "qwen3.7-text-embedding"
	MaxOutputTokens             = 256
	MaxChatRequests             = 16
	MaxEmbeddingRequests        = 4
	tenantID             uint64 = 880008
	taskID                      = "x08-fixed-cache-v1"
	inputPrice           int64  = 200000
	outputPrice          int64  = 800000
	cacheReadPrice       int64  = 40000
	embeddingPrice       int64  = 500000
)

// Step freezes one logical invocation and its maximum budget reservation.
type Step struct {
	ID                    string            `json:"id"`
	Operation             string            `json:"operation"`
	Arm                   string            `json:"arm"`
	Round                 int               `json:"round"`
	Pair                  int               `json:"pair"`
	Input                 []string          `json:"input,omitempty"`
	Data                  map[string]string `json:"wiki_data,omitempty"`
	Messages              []chat.Message    `json:"messages,omitempty"`
	InputTokenUpperBound  int               `json:"input_token_upper_bound"`
	OutputTokenUpperBound int               `json:"output_token_upper_bound"`
	ReservationMicrounits int64             `json:"reservation_microunits"`
}

// Plan contains all inputs, model identities, parameters and spending limits for review.
type Plan struct {
	Version                           int                            `json:"version"`
	SHA256                            string                         `json:"sha256"`
	Endpoint                          string                         `json:"endpoint"`
	ChatIdentity                      *types.EvaluationModelSnapshot `json:"chat_identity"`
	EmbeddingIdentity                 *types.EvaluationModelSnapshot `json:"embedding_identity"`
	ChatRequestLimit                  int                            `json:"chat_request_limit"`
	EmbeddingRequestLimit             int                            `json:"embedding_request_limit"`
	InputTokenUpperBound              int                            `json:"input_token_upper_bound"`
	OutputTokenUpperBound             int                            `json:"output_token_upper_bound"`
	OriginalPriceUpperBoundMicrounits int64                          `json:"original_price_upper_bound_microunits"`
	Currency                          string                         `json:"currency"`
	SharedPrefixApproxTokens          int                            `json:"shared_prefix_approx_tokens"`
	PrefixEstimator                   string                         `json:"prefix_estimator"`
	CacheEligibility                  string                         `json:"cache_eligibility"`
	ChatInputPrice                    int64                          `json:"chat_input_microunits_per_million"`
	ChatOutputPrice                   int64                          `json:"chat_output_microunits_per_million"`
	ChatCacheReadPrice                int64                          `json:"chat_cache_read_microunits_per_million"`
	EmbeddingInputPrice               int64                          `json:"embedding_input_microunits_per_million"`
	ChatParameters                    map[string]any                 `json:"chat_parameters"`
	EmbeddingParameters               map[string]any                 `json:"embedding_parameters"`
	OmittedChatParameters             []string                       `json:"omitted_chat_parameters"`
	Steps                             []Step                         `json:"steps"`
}

func models() (*types.Model, *types.Model) {
	c := &types.Model{
		ID: "x08-chat", TenantID: tenantID, Name: ChatModel,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: Endpoint, Provider: "aliyun", InterfaceType: "openai", ContextWindow: 32768,
			MaxOutputTokens: MaxOutputTokens, MaxConcurrency: 1,
			ExtraConfig: map[string]string{chat.ExtraConfigThinkingControl: "enable_thinking"},
		},
	}
	e := &types.Model{
		ID: "x08-embedding", TenantID: tenantID, Name: EmbeddingModel,
		Type: types.ModelTypeEmbedding, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: Endpoint, Provider: "aliyun", InterfaceType: "openai", MaxConcurrency: 1,
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension: 1024, TruncatePromptTokens: 511, SupportsDimensionOverride: true,
			},
		},
	}
	return c, e
}

// BuildPlan uses the production Wiki renderer with a capture-only model. All
// text is synthetic and byte-stable. No HTTP clients or listeners are created.
func BuildPlan() (*Plan, error) {
	c, e := models()
	p := &Plan{
		Version: 3, Endpoint: Endpoint,
		ChatIdentity: types.EvaluationModelSnapshotFrom(c), EmbeddingIdentity: types.EvaluationModelSnapshotFrom(e),
		ChatRequestLimit: MaxChatRequests, EmbeddingRequestLimit: MaxEmbeddingRequests, Currency: "CNY",
		PrefixEstimator: "cl100k_base (approximation, not the Qwen tokenizer)",
		CacheEligibility: "Shared prefix exceeds 1024 estimated tokens; provider tokenization and " +
			"implicit cache hits remain unverified until execution.",
		ChatInputPrice: inputPrice, ChatOutputPrice: outputPrice,
		ChatCacheReadPrice: cacheReadPrice, EmbeddingInputPrice: embeddingPrice,
		ChatParameters: map[string]any{
			"temperature": 0.3, "enable_thinking": false, "stream": false, "max_completion_tokens": 256,
			"cache_retention": "none", "provider_cache_mode": "implicit",
		},
		EmbeddingParameters: map[string]any{
			"encoding_format": "float", "dimensions": 1024, "truncate_prompt_tokens": 511,
		},
		OmittedChatParameters: []string{"top_p", "seed", "tools", "max_tokens"},
	}
	texts := []string{}
	for i := 0; i < 4; i++ {
		texts = append(texts, fmt.Sprintf("Synthetic campus room %d contains %d seats. Its catalog identifier is "+
			"X08-%03d. The room opens at 09:00 and closes at 17:00.", i+1, 42+i, i+1))
	}
	for round := 1; round <= 2; round++ {
		for batch := 0; batch < 2; batch++ {
			s := Step{
				ID: fmt.Sprintf("embedding-r%d-b%d", round, batch+1), Operation: "embedding",
				Arm: "application-cache", Round: round, Pair: batch + 1,
				Input: append([]string(nil), texts[batch*2:batch*2+2]...),
			}
			b, _ := json.Marshal(s.Input)
			s.InputTokenUpperBound = len(b) + 2048
			s.ReservationMicrounits = ceilCost(s.InputTokenUpperBound, 0, embeddingPrice, 0)
			p.Steps = append(p.Steps, s)
		}
	}
	var shared strings.Builder
	for i := 0; i < 96; i++ {
		fmt.Fprintf(&shared, "Source context %03d: This synthetic campus catalog describes document scope "+
			"and provenance. Context identifiers are framing only, never factual "+
			"evidence.\n", i)
	}
	estimator, err := token.NewEstimator()
	if err != nil {
		return nil, err
	}
	p.SharedPrefixApproxTokens = estimator.EstimateString(shared.String())
	if p.SharedPrefixApproxTokens <= 2048 {
		return nil, fmt.Errorf("shared prefix estimate must exceed 2048 tokens")
	}
	for round := 1; round <= 2; round++ {
		for pair := 1; pair <= 4; pair++ {
			arms := []string{"stable-prefix", "page-first"}
			if (round+pair)%2 == 1 {
				arms[0], arms[1] = arms[1], arms[0]
			}
			for _, arm := range arms {
				data := map[string]string{
					"HasAdditions": "true", "SharedSourceContexts": shared.String(),
					"PageSlug":  fmt.Sprintf("synthetic-room-%d", pair),
					"PageTitle": fmt.Sprintf("Synthetic campus room %d", pair),
					"PageType":  "room", "ExistingContent": "", "NewContent": texts[pair-1],
					"AvailableSlugs": "", "Language": "English",
				}
				capture := &captureChat{}
				if _, err := service.NewWikiPageEvaluationRunner(capture)(context.Background(), data); err != nil {
					return nil, err
				}
				messages := orderMessages(capture.messages, arm)
				s := Step{
					ID: fmt.Sprintf("wiki-r%d-p%d-%s", round, pair, arm), Operation: "chat",
					Arm: arm, Round: round, Pair: pair, Data: data, Messages: messages,
					OutputTokenUpperBound: MaxOutputTokens,
				}
				b, _ := json.Marshal(messages)
				s.InputTokenUpperBound = len(b) + 2048
				if s.InputTokenUpperBound+MaxOutputTokens > 32768 {
					return nil, fmt.Errorf("planned input exceeds fixed price tier")
				}
				s.ReservationMicrounits = ceilCost(
					s.InputTokenUpperBound, s.OutputTokenUpperBound, inputPrice, outputPrice,
				)
				p.Steps = append(p.Steps, s)
			}
		}
	}
	for _, s := range p.Steps {
		p.InputTokenUpperBound += s.InputTokenUpperBound
		p.OutputTokenUpperBound += s.OutputTokenUpperBound
		p.OriginalPriceUpperBoundMicrounits += s.ReservationMicrounits
	}
	if p.OriginalPriceUpperBoundMicrounits >= 1000000 {
		return nil, fmt.Errorf("fixed plan exceeds CNY 1")
	}
	b, _ := json.Marshal(p)
	p.SHA256 = hash(b)
	return p, nil
}

func ceilCost(input, output int, inPrice, outPrice int64) int64 {
	return (int64(input)*inPrice + int64(output)*outPrice + 999999) / 1000000
}
func hash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

// The control moves the exact shared-context block behind page variables.
// Text, roles and parameters are otherwise byte-identical within a pair.
func orderMessages(input []chat.Message, arm string) []chat.Message {
	result := append([]chat.Message(nil), input...)
	if arm != "page-first" {
		return result
	}
	for i := range result {
		if result[i].Role == "user" {
			text := result[i].Content
			end := strings.Index(text, "</shared_source_contexts>")
			if strings.HasPrefix(text, "<shared_source_contexts>") && end >= 0 {
				end += len("</shared_source_contexts>")
				result[i].Content = text[end:] + text[:end]
			}
		}
	}
	return result
}

type captureChat struct{ messages []chat.Message }

func (c *captureChat) Chat(
	_ context.Context, m []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.messages = append([]chat.Message(nil), m...)
	return &types.ChatResponse{Content: "captured"}, nil
}

func (c *captureChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("capture model does not stream")
}
func (c *captureChat) GetModelName() string { return ChatModel }
func (c *captureChat) GetModelID() string   { return "x08-chat" }
