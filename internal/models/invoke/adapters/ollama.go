package adapters

// OllamaAdapter: native Ollama protocol (/api/chat + /api/tags), ported from
// v1 internal/models/chat/ollama.go (P1b strangler). v2 changes per design §6.1/§11:
// the call chain no longer goes through OllamaService — local Ollama is a
// regular provider behind the executor's unified SSRF gate, and the
// "missing model auto-pull" (EnsureModelAvailable) is retired; the new
// ListModels facet (§7.1) serves GET /api/tags.
// Wire types mirror the v1 ollama SDK struct field order so request bodies
// stay byte-identical with the recorded goldens.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
)

// OllamaAdapter serves the chat and list facets for provider "ollama".
type OllamaAdapter struct{}

var (
	_ invoke.ChatAdapter       = (*OllamaAdapter)(nil)
	_ invoke.ListModelsAdapter = (*OllamaAdapter)(nil)
)

func init() {
	if err := invoke.Default.Register(&OllamaAdapter{}); err != nil {
		panic(err) // three-lock #2 fail fast
	}
}

// Provider returns the canonical provider name.
func (a *OllamaAdapter) Provider() string { return "ollama" }

// Capabilities reports the provider's effective capabilities.
func (a *OllamaAdapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Common: provider.CommonCaps{
			Streaming:      true,
			HealthProbe:    true,
			UsageReporting: provider.UsageFull,
			ModelListing:   provider.ModelListingCaps{Supported: true}, // binds the ListModels facet
		},
		Chat: &provider.ChatCaps{
			InputModalities: []provider.Modality{provider.ModalityText, provider.ModalityImage},
			Protocol:        provider.ProtocolOllama,
		},
		Credentials: []provider.CredentialFieldSpec{{Key: provider.CredentialKeyAPIKey}}, // optional (§6.8)
	}
}

// --- Wire types (field order mirrors the v1 ollama SDK) ---

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   *bool           `json:"stream,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Options  map[string]any  `json:"options"`
	Think    *bool           `json:"think,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Images    [][]byte         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaToolFunc `json:"function"`
}

type ollamaToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Index     int            `json:"index,omitempty"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaChatResponse struct {
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Thinking  string           `json:"thinking,omitempty"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
	Done            bool `json:"done"`
}

// BuildChatRequest ports v1 buildChatRequest. Note the v1 quirks preserved:
// temperature is ALWAYS sent (even 0); num_predict maps the completion budget;
// Think passes opts.Thinking straight through (no level chain — ollama
// exposes a boolean, no levels).
func (a *OllamaAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	if strings.TrimSpace(ep.BaseURL) == "" {
		return nil, fmt.Errorf("ollama provider: base URL is required")
	}
	streamFlag := opts.Stream
	chatReq := ollamaChatRequest{
		Model:    model,
		Messages: a.convertMessages(opts.Messages),
		Stream:   &streamFlag,
		Options:  make(map[string]any),
	}
	chatReq.Options["temperature"] = opts.Temperature
	if opts.TopP > 0 {
		chatReq.Options["top_p"] = opts.TopP
	}
	if opts.MaxCompletionTokens > 0 {
		chatReq.Options["num_predict"] = opts.MaxCompletionTokens
	}
	if opts.Thinking != nil {
		think := *opts.Thinking
		chatReq.Think = &think
	}
	if len(opts.Format) > 0 {
		chatReq.Format = opts.Format
	}
	if len(opts.Tools) > 0 {
		chatReq.Tools = ollamaToolsFrom(opts.Tools)
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/x-ndjson") // ollama SDK sends this on every call
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    strings.TrimRight(ep.BaseURL, "/") + "/api/chat",
		Header: header,
		Body:   body,
		Stream: opts.Stream,
	}, nil
}

// convertMessages ports v1 convertMessages: tool role carries ToolName; user
// image parts are resolved to raw bytes (base64 on the wire).
func (a *OllamaAdapter) convertMessages(messages []invoke.Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(messages))
	for _, msg := range messages {
		m := ollamaMessage{
			Role:      string(msg.Role),
			Content:   textFromPartsRaw(msg.Content),
			ToolCalls: ollamaToolCallsFrom(msg.ToolCalls),
		}
		if msg.Role == "tool" {
			m.ToolName = msg.Name
		}
		if msg.Role == "user" {
			for _, part := range msg.Content {
				if part.Image == nil {
					continue
				}
				if data := resolveImageForOllama(part.Image.URL); data != nil {
					m.Images = append(m.Images, data)
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// textFromPartsRaw joins raw text parts (no trim — v1 sent msg.Content
// verbatim); single-part messages, the common case, come out unchanged.
func textFromPartsRaw(parts []invoke.Part) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func ollamaToolsFrom(tools []invoke.ToolDef) []ollamaTool {
	out := make([]ollamaTool, 0, len(tools))
	for _, tool := range tools {
		fn := ollamaToolFunc{Name: tool.Name, Description: tool.Description}
		if len(tool.Parameters) > 0 {
			_ = json.Unmarshal(tool.Parameters, &fn.Parameters)
		}
		out = append(out, ollamaTool{Type: "function", Function: fn})
	}
	return out
}

func ollamaToolCallsFrom(calls []invoke.ToolCall) []ollamaToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ollamaToolCall, 0, len(calls))
	for _, tc := range calls {
		var entry ollamaToolCall
		entry.Function.Index = tools2i(tc.ID)
		entry.Function.Name = tc.Function.Name
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &entry.Function.Arguments)
		}
		out = append(out, entry)
	}
	return out
}

func ollamaToolCallsTo(calls []ollamaToolCall) []invoke.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]invoke.ToolCall, 0, len(calls))
	for _, tc := range calls {
		args, _ := json.Marshal(tc.Function.Arguments)
		out = append(out, invoke.ToolCall{
			ID:   tooli2s(tc.Function.Index),
			Type: "function",
			Function: invoke.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: string(args),
			},
		})
	}
	return out
}

func tooli2s(i int) string { return strconv.Itoa(i) }

func tools2i(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// ParseChatResponse ports v1 OllamaChat.Chat result handling, including the
// v1 usage arithmetic quirk recorded in the golden: completion = eval_count -
// prompt_eval_count (can go negative on tiny responses; golden 如实录).
// Thinking text backs off to content when the model produced only thinking.
func (a *OllamaAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*invoke.ChatResponse, error) {
	var resp ollamaChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	content := resp.Message.Content
	if content == "" && resp.Message.Thinking != "" {
		content = resp.Message.Thinking
	}
	var promptTokens, completionTokens int
	if resp.EvalCount > 0 {
		promptTokens = resp.PromptEvalCount
		completionTokens = resp.EvalCount - promptTokens
	}
	return &invoke.ChatResponse{
		Content:   content,
		ToolCalls: ollamaToolCallsTo(resp.Message.ToolCalls),
		Usage: invoke.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}, nil
}

// --- ListModels facet (design §7.1, new in v2): GET /api/tags ---

// BuildListRequest describes the native model-list call. ollama records with
// a trailing /v1 on base_url are stripped upstream (shared constructor rule,
// §6.1) so this adapter always appends the native path.
func (a *OllamaAdapter) BuildListRequest(ep invoke.Endpoint) (*invoke.Request, error) {
	if strings.TrimSpace(ep.BaseURL) == "" {
		return nil, fmt.Errorf("ollama provider: base URL is required")
	}
	header := http.Header{}
	header.Set("Accept", "application/json")
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    strings.TrimRight(ep.BaseURL, "/") + "/api/tags",
		Header: header,
	}, nil
}

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// ParseListResponse maps /api/tags entries to RemoteModel: the qualified
// "model" tag (e.g. "qwen3:8b") is the catalog model ID, "name" is the
// display name; ollama exposes no context/output ceilings so they stay 0.
func (a *OllamaAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	var resp ollamaTagsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	models := make([]invoke.RemoteModel, 0, len(resp.Models))
	for _, m := range resp.Models {
		id := m.Model
		if id == "" {
			id = m.Name
		}
		models = append(models, invoke.RemoteModel{ID: id, DisplayName: m.Name})
	}
	return models, nil
}
