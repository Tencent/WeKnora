package adapters

// weknoracloud.go — WeKnoraCloud chat facet (design §6.2/§6.8). Behavior port
// of v1 chat/provider.go weKnoraCloudProvider (endpoint + ForceRawHTTP +
// TransformMessages multi-content downgrade + HMAC request signing) and the
// chat-side of v1 vlm/weknoracloud.go (same signed /api/v1/chat/completions
// route; the VLM facet rides Chat via InputModalities).
import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// weKnoraCloudAdapter composes the openai fallback (request funnel, parse,
// stream bridge) with the WeKnoraCloud deltas: signed endpoint, multi-content
// downgrade, body-HMAC auth.
type weKnoraCloudAdapter struct {
	openaiAdapter
}

// compile-time lock #1 (design §6.2).
var (
	_ invoke.ChatAdapter      = (*weKnoraCloudAdapter)(nil)
	_ invoke.EmbeddingAdapter = (*weKnoraCloudAdapter)(nil)
)

const (
	weKnoraCloudEmbedPath    = "/api/v1/embeddings"
	weKnoraCloudEmbedTimeout = 60 * time.Second
)

func newWeKnoraCloudAdapter() *weKnoraCloudAdapter {
	return &weKnoraCloudAdapter{openaiAdapter{
		name: provider.ProviderWeKnoraCloud,
		spec: openaiVendorSpec{
			// v1 weKnoraCloudProvider: ForceRawHTTP + multi-content downgrade.
			forceRaw:  true,
			sign:      true,
			transform: transformWeKnoraCloudMessages,
		},
		caps: chatCapsFor(provider.ProviderWeKnoraCloud),
	}}
}

// BuildChatRequest builds the openai-form body (downgraded messages, map-form
// roundtrip via spec.forceRaw) and then signs it: the HMAC depends on the
// final body bytes, so it must happen inside BuildChatRequest (design §6.2).
func (a *weKnoraCloudAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	req, err := a.openaiAdapter.BuildChatRequest(ep, model, opts)
	if err != nil {
		return nil, err
	}
	// v1 weKnoraCloudProvider.Auth (provider.go:93-99): sign over the final
	// body bytes with AppID/AppSecret (modelutils.Sign).
	requestID := uuid.NewString()
	for k, v := range modelutils.Sign(ep.Credentials.AppID, ep.Credentials.AppSecret, requestID, string(req.Body)) {
		req.Header.Set(k, v)
	}
	// Signature/auth-critical headers the entry must not let user custom
	// headers override (design §6.4 protected-header rule).
	req.ProtectedHeaders = append(req.ProtectedHeaders,
		"X-Appid", "X-Api-Key", "X-Request-Id", "X-Timestamp", "X-Nonce", "X-Signature")
	return req, nil
}

// transformWeKnoraCloudMessages ports weKnoraCloudProvider.TransformMessages
// (provider.go:103-120): downgrades MultiContent to newline-joined plain text
// while preserving tool_calls / tool_call_id / name so the function-calling
// protocol keeps working.
func transformWeKnoraCloudMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		msg := m
		if msg.Content == "" && len(msg.MultiContent) > 0 {
			var textParts []string
			for _, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText && part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			msg.MultiContent = nil
		}
		result = append(result, msg)
	}
	return result
}

func init() {
	if err := invoke.Default.Register(newWeKnoraCloudAdapter()); err != nil {
		panic(fmt.Sprintf("invoke/adapters: register weknoracloud adapter: %v", err))
	}
}

// --- Embedding facet (P2, port of v1 embedding/weknoracloud.go) ---

// weKnoraCloudEmbedRequest mirrors the v1 wire shape: openai-like but with no
// encoding_format / truncate_prompt_tokens (the v1 struct carried the token
// field but never set it).
type weKnoraCloudEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type weKnoraCloudEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Capabilities overrides the embedded declaration: both served shards.
func (a *weKnoraCloudAdapter) Capabilities() provider.Capabilities {
	caps := a.openaiAdapter.Capabilities()
	caps.Embedding = embeddingCapsFor(provider.ProviderWeKnoraCloud)
	return caps
}

// BuildEmbeddingRequest ports v1 NewWeKnoraCloudEmbedder + BatchEmbed: the
// signed /api/v1/embeddings route. The wire model name is the config-level
// remote_model_name override when set (the shared constructor folds it into
// ModelName, so `model` is already effective here).
func (a *weKnoraCloudAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	if ep.Credentials.AppID == "" {
		return nil, fmt.Errorf("WeKnoraCloud embedder: AppID is required")
	}
	if ep.Credentials.AppSecret == "" {
		return nil, fmt.Errorf("WeKnoraCloud embedder: AppSecret is required")
	}
	base := strings.TrimRight(ep.BaseURL, "/")
	if base == "" {
		base = provider.WeKnoraCloudBaseURL
	}
	reqBody := weKnoraCloudEmbedRequest{Model: model, Input: opts.Inputs}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		reqBody.Dimensions = opts.Dimensions
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: marshal: %w", err)
	}
	requestID := uuid.NewString()
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	for k, v := range modelutils.Sign(ep.Credentials.AppID, ep.Credentials.AppSecret, requestID, string(body)) {
		header.Set(k, v)
	}
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     base + weKnoraCloudEmbedPath,
		Header:  header,
		Body:    body,
		Timeout: weKnoraCloudEmbedTimeout,
		// Signature/auth-critical headers (design §6.4 protected-header rule).
		ProtectedHeaders: []string{
			"X-Appid", "X-Api-Key", "X-Request-Id", "X-Timestamp", "X-Nonce", "X-Signature",
		},
	}, nil
}

// ParseEmbeddingResponse ports the v1 index-integrity validation: every input
// index must appear exactly once.
func (a *weKnoraCloudAdapter) ParseEmbeddingResponse(
	_ int, _ http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	var embedResp weKnoraCloudEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("weknoracloud embedder: unmarshal: %w", err))
	}
	count := len(embedResp.Data)
	result := make([][]float32, count)
	seen := make([]bool, count)
	for _, item := range embedResp.Data {
		if item.Index < 0 || item.Index >= count {
			return nil, invoke.ClassifyError(fmt.Errorf(
				"weknoracloud embedder: response index %d out of range for %d inputs", item.Index, count))
		}
		if seen[item.Index] {
			return nil, invoke.ClassifyError(fmt.Errorf(
				"weknoracloud embedder: duplicate response index %d", item.Index))
		}
		result[item.Index] = item.Embedding
		seen[item.Index] = true
	}
	for index, found := range seen {
		if !found {
			return nil, invoke.ClassifyError(fmt.Errorf(
				"weknoracloud embedder: missing embedding for input index %d", index))
		}
	}
	return &invoke.EmbeddingResponse{Vectors: result}, nil
}
