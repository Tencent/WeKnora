// GeminiAdapter: the native generateContent protocol (P5-1, doc-driven —
// wire shapes from 模型服务商接口文档/Google Gemini/Generating content.md,
// v1beta). Replaces the openai-compat chat route: gemini rides its own
// protocol family (ProtocolGoogleGenai), while embedding stays on the native
// batchEmbedContents path it has used since P2 (buildGeminiEmbedding).
//
// Wire summary (doc §GenerateContentRequest/Response):
//   - POST {base}/models/{model}:generateContent | :streamGenerateContent?alt=sse
//   - auth via the x-goog-api-key HEADER (裁定 #23, not the ?key= query form —
//     the key must never land in URLs or access logs; the entry's isAuthHeader
//     permanent-protection set already covers this exact header)
//   - contents[].role is "user"|"model" only; system turns fold into
//     systemInstruction (Content form, text only)
//   - parts are mutually exclusive: text | inlineData | functionCall |
//     functionResponse | fileData
//   - generationConfig carries the sampling fields + thinkingConfig
//     (thinkingLevel enum for Gemini 3+, thinkingBudget for 2.5-series)

package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// compile-time lock #1 (design §6.2).
var (
	_ invoke.ChatAdapter       = (*GeminiAdapter)(nil)
	_ invoke.EmbeddingAdapter  = (*GeminiAdapter)(nil)
	_ invoke.ListModelsAdapter = (*GeminiAdapter)(nil)
)

// GeminiAdapter serves the native generateContent facets for provider "gemini"
// (chat + embedding + model listing). Embedding delegates to the P2 native
// batchEmbedContents pair — the wire does not change with this adapter.
type GeminiAdapter struct{}

func init() {
	if err := invoke.Default.Register(&GeminiAdapter{}); err != nil {
		panic(err) // three-lock #2 fail fast
	}
}

// Provider returns the canonical provider name.
func (a *GeminiAdapter) Provider() string { return string(invoke.ProviderGemini) }

// Capabilities reports the served shards (chat + embedding from the provider
// knowledge table) plus the model-listing flag this adapter serves.
// Registration lock #2: shard set == facet set.
func (a *GeminiAdapter) Capabilities() invoke.Capabilities {
	caps := invoke.Capabilities{
		Common: invoke.CommonCaps{ModelListing: invoke.ModelListingCaps{Supported: true}},
	}
	if info, ok := providerInfoFor(invoke.ProviderGemini); ok {
		eff := info.EffectiveCapabilities()
		caps.Chat = eff.Chat
		caps.Embedding = eff.Embedding
	}
	return caps
}

// --- embedding facet: the P2 native batchEmbedContents path, verbatim ---

// BuildEmbeddingRequest delegates to the P2 native pair (the wire does not
// change with this adapter; golden embedding_gemini keeps pinning it).
func (a *GeminiAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	return buildGeminiEmbedding(ep, model, opts)
}

// ParseEmbeddingResponse delegates to the P2 native pair.
func (a *GeminiAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	return parseGeminiEmbedding(status, header, body)
}

// --- wire types (field names pinned to the v1beta REST contract; golden
// request bodies are compared byte-for-byte after compact normalization) ---

type geminiRequest struct {
	Contents          []geminiChatContent     `json:"contents"`
	SystemInstruction *geminiChatContent      `json:"systemInstruction,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiChatContent struct {
	Role  string           `json:"role,omitempty"`
	Parts []geminiChatPart `json:"parts"`
}

type geminiChatPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiBlob             `json:"inlineData,omitempty"`
	FileData         *geminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type geminiFileData struct {
	FileURI  string `json:"fileUri"`
	MimeType string `json:"mimeType,omitempty"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float64              `json:"temperature,omitempty"`
	TopP             *float64              `json:"topP,omitempty"`
	MaxOutputTokens  int                   `json:"maxOutputTokens,omitempty"`
	Seed             *int32                `json:"seed,omitempty"`
	PresencePenalty  *float64              `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64              `json:"frequencyPenalty,omitempty"`
	ThinkingConfig   *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// geminiThinkingConfig maps the platform thinking vocabulary onto the two
// documented mechanisms (裁定 #24):
//   - enabled + level  → thinkingLevel enum ("LOW"/"MEDIUM"/"HIGH"; doc:
//     "Recommended for Gemini 3 or later models. Use with earlier models
//     results in an error") — MINIMAL stays unused (no platform level maps
//     to it today)
//   - disabled         → thinkingBudget: 0 — the only documented off switch
//     (2.5-series semantics; Gemini 3 rejects budget=0, so disabling a
//     Gemini 3 model degrades to a provider error rather than silent
//     thinking — fail loud beats guessing a MINIMAL floor)
//
// nil Thinking (model default) sends no thinkingConfig at all. The v1
// openai-compat route carried NO thinking mapping for gemini — this is a
// doc-driven addition, not a parity surface.
type geminiThinkingConfig struct {
	IncludeThoughts *bool  `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

// geminiGenerateResponse mirrors both the non-stream body and one SSE frame
// (each streamGenerateContent event is a complete GenerateContentResponse).
type geminiGenerateResponse struct {
	Candidates []struct {
		Content *struct {
			Role  string           `json:"role"`
			Parts []geminiRespPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
	ResponseID    string               `json:"responseId"`
}

type geminiRespPart struct {
	Text         string `json:"text"`
	Thought      bool   `json:"thought"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// usage maps onto the platform Usage: thoughts are billed ON TOP of candidate
// tokens (doc: total = prompt + thoughts + candidates), and the completion
// budget carves the thinking window out of the same output ceiling — so they
// ride CompletionTokens. Cached prompt tokens surface through the cache-read
// counter only when actually reported non-zero.
func (u *geminiUsageMetadata) usage() invoke.Usage {
	out := invoke.Usage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount,
		TotalTokens:      u.TotalTokenCount,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if u.CachedContentTokenCount > 0 {
		out.CacheReadTokens = u.CachedContentTokenCount
		out.CacheReported = true
	}
	return out
}

// BuildChatRequest builds the native generateContent call (doc §3.1).
func (a *GeminiAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	apiKey := strings.TrimSpace(ep.Credentials.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("gemini provider: API key is required")
	}
	base := geminiNativeBaseURL(ep.BaseURL)

	req := geminiRequest{Contents: make([]geminiChatContent, 0, len(opts.Messages))}
	var systemParts []string
	// functionCall names must echo back on functionResponse parts; the
	// neutral tool-result message carries only the call ID, so the name is
	// recovered from the assistant turn that issued the call.
	callNames := map[string]string{}

	for _, msg := range opts.Messages {
		// A new invoke.Role must be mapped here explicitly, never silently
		// fall through (the neutral vocabulary has no gemini "tool" role).
		//exhaustive:enforce
		switch msg.Role {
		case invoke.RoleSystem:
			if text := msg.Text(); text != "" {
				systemParts = append(systemParts, text)
			}
		case invoke.RoleUser:
			req.Contents = append(req.Contents, geminiChatContent{
				Role:  "user",
				Parts: geminiUserParts(msg.Content),
			})
		case invoke.RoleAssistant:
			parts := make([]geminiChatPart, 0, len(msg.Content)+len(msg.ToolCalls))
			if text := msg.Text(); text != "" {
				parts = append(parts, geminiChatPart{Text: text})
			}
			for _, call := range msg.ToolCalls {
				callNames[call.ID] = call.Function.Name
				args := json.RawMessage(call.Function.Arguments)
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				parts = append(parts, geminiChatPart{FunctionCall: &geminiFunctionCall{
					ID:   call.ID,
					Name: call.Function.Name,
					Args: args,
				}})
			}
			if len(parts) > 0 {
				req.Contents = append(req.Contents, geminiChatContent{Role: "model", Parts: parts})
			}
		case invoke.RoleTool:
			name := callNames[msg.ToolCallID]
			if name == "" {
				// The doc requires functionResponse.name; an unrecoverable
				// lookup means the history was truncated — fail loud.
				return nil, fmt.Errorf("gemini provider: tool result %q has no matching function call", msg.ToolCallID)
			}
			payload, err := json.Marshal(map[string]string{"output": msg.Text()})
			if err != nil {
				return nil, fmt.Errorf("marshal tool response: %w", err)
			}
			req.Contents = append(req.Contents, geminiChatContent{
				// The doc does not pin the role for functionResponse
				// contents; role=user is the SDK-standard carrier.
				Role: "user",
				Parts: []geminiChatPart{{FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: payload,
				}}},
			})
		}
	}
	if text := strings.Join(systemParts, "\n\n"); text != "" {
		req.SystemInstruction = &geminiChatContent{Parts: []geminiChatPart{{Text: text}}}
	}

	a.applyTools(&req, opts)
	req.GenerationConfig = a.generationConfig(opts)

	action := ":generateContent"
	if opts.Stream {
		// alt=sse is REQUIRED for SSE framing (doc §streamGenerateContent);
		// without it the response is a JSON array of frames.
		action = ":streamGenerateContent?alt=sse"
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-Goog-Api-Key", apiKey)
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    base + "/models/" + model + action,
		Header: header,
		Body:   data,
		Stream: opts.Stream,
	}, nil
}

// applyTools maps the neutral tool surface: parameters ride as the OpenAPI
// JSON Schema the openai-compat layer already carried (Gemini's schema subset
// accepts it verbatim per doc §FunctionDeclaration); tool_choice maps onto
// functionCallingConfig.mode.
func (a *GeminiAdapter) applyTools(req *geminiRequest, opts *invoke.ChatOptions) {
	if len(opts.Tools) == 0 {
		return
	}
	decls := make([]geminiFunctionDeclaration, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		decls = append(decls, geminiFunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	req.Tools = []geminiTool{{FunctionDeclarations: decls}}

	switch opts.ToolChoice {
	case "", "auto":
		// default mode — send nothing (doc default AUTO)
	case "required":
		req.ToolConfig = &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{Mode: "ANY"}}
	case "none":
		req.ToolConfig = &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{Mode: "NONE"}}
	default:
		// a specific tool name → ANY restricted to it
		req.ToolConfig = &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{opts.ToolChoice},
		}}
	}
}

// generationConfig folds the sampling surface; zero values mean "do not
// send" (invoke.ChatOptions semantics).
func (a *GeminiAdapter) generationConfig(opts *invoke.ChatOptions) *geminiGenerationConfig {
	cfg := &geminiGenerationConfig{}
	sent := false
	if opts.Temperature > 0 {
		t := opts.Temperature
		cfg.Temperature = &t
		sent = true
	}
	if opts.TopP > 0 {
		p := opts.TopP
		cfg.TopP = &p
		sent = true
	}
	if opts.MaxCompletionTokens > 0 {
		cfg.MaxOutputTokens = opts.MaxCompletionTokens
		sent = true
	}
	if opts.Seed != 0 {
		s := int32(opts.Seed)
		cfg.Seed = &s
		sent = true
	}
	if opts.PresencePenalty != 0 {
		p := opts.PresencePenalty
		cfg.PresencePenalty = &p
		sent = true
	}
	if opts.FrequencyPenalty != 0 {
		f := opts.FrequencyPenalty
		cfg.FrequencyPenalty = &f
		sent = true
	}
	if opts.Thinking != nil {
		tc := &geminiThinkingConfig{}
		if *opts.Thinking {
			level := invoke.ResolveThinkingLevel(opts.ThinkingLevel, "", nil,
				chatCapsFor(invoke.ProviderGemini).Thinking)
			tc.ThinkingLevel = geminiThinkingLevel(level)
		} else {
			zero := 0
			tc.ThinkingBudget = &zero
		}
		cfg.ThinkingConfig = tc
		sent = true
	}
	if !sent {
		return nil
	}
	return cfg
}

// geminiThinkingLevel maps the platform five-level vocabulary onto the
// documented ThinkingLevel enum (doc: UNSPECIFIED/MINIMAL/LOW/MEDIUM/HIGH).
// xhigh/max cap at HIGH — the enum has no higher rung.
func geminiThinkingLevel(level string) string {
	switch invoke.Level(level) {
	case invoke.LevelLow:
		return "LOW"
	case invoke.LevelMedium:
		return "MEDIUM"
	case invoke.LevelHigh, invoke.LevelXHigh, invoke.LevelMax:
		return "HIGH"
	default:
		return "MEDIUM"
	}
}

// geminiUserParts maps user content: data-URI images become inlineData
// (base64), any other URL becomes fileData (the Files-API URI form; the
// public API rejects arbitrary https sources — the provider errors loudly).
func geminiUserParts(parts []invoke.Part) []geminiChatPart {
	out := make([]geminiChatPart, 0, len(parts))
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, geminiChatPart{Text: p.Text})
		}
		if p.Image != nil {
			if mime, b64, ok := parseDataURI(p.Image.URL); ok {
				out = append(out, geminiChatPart{InlineData: &geminiBlob{MimeType: mime, Data: b64}})
			} else {
				out = append(out, geminiChatPart{FileData: &geminiFileData{FileURI: p.Image.URL}})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, geminiChatPart{Text: ""})
	}
	return out
}

// parseDataURI splits `data:<mime>;base64,<payload>`.
func parseDataURI(raw string) (mime, b64 string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(raw, prefix) {
		return "", "", false
	}
	rest := raw[len(prefix):]
	sep := strings.Index(rest, ";base64,")
	if sep < 0 {
		return "", "", false
	}
	return rest[:sep], rest[sep+len(";base64,"):], true
}

// geminiNativeBaseURL normalizes the configured base: the v1 dual-URL trap
// (chat on .../v1beta/openai, embedding on .../v1beta) must not leak into the
// native route, so a compat-layer suffix is stripped like the embedding path
// already does.
func geminiNativeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = invoke.GeminiBaseURL
	}
	base = strings.TrimSuffix(base, "/openai")
	return base
}

// ParseChatResponse decodes the non-stream generateContent body.
func (a *GeminiAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*invoke.ChatResponse, error) {
	var resp geminiGenerateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	// doc §PromptFeedback: no candidates at all means the prompt was blocked.
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" && len(resp.Candidates) == 0 {
		return nil, invoke.ClassifyError(fmt.Errorf("gemini: prompt blocked (%s)", resp.PromptFeedback.BlockReason))
	}
	out := &invoke.ChatResponse{}
	if len(resp.Candidates) == 0 {
		return out, nil
	}
	cand := resp.Candidates[0]
	var thinking strings.Builder
	if cand.Content != nil {
		for _, part := range cand.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				args := string(part.FunctionCall.Args)
				if args == "" {
					args = "{}"
				}
				id := part.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("gemini_call_%d", len(out.ToolCalls)+1)
				}
				out.ToolCalls = append(out.ToolCalls, invoke.ToolCall{
					ID:   id,
					Type: "function",
					Function: invoke.FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: args,
					},
				})
			case part.Thought:
				thinking.WriteString(part.Text)
			default:
				out.Content += part.Text
			}
		}
	}
	if thinking.Len() > 0 {
		t := thinking.String()
		out.Thinking = &t
	}
	out.FinishReason = geminiFinishReason(cand.FinishReason, len(out.ToolCalls) > 0)
	if resp.UsageMetadata != nil {
		out.Usage = resp.UsageMetadata.usage()
	}
	return out, nil
}

// geminiFinishReason folds the vendor enum onto the platform vocabulary the
// openai family emits (stop/length/content_filter/tool_calls); presence of
// tool calls outranks the stop reason (openai convention).
func geminiFinishReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	switch reason {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return "content_filter"
	case "STOP", "":
		return "stop"
	default:
		// RECITATION/LANGUAGE/OTHER/MALFORMED_FUNCTION_CALL/... — the
		// generation is over either way; "stop" keeps downstream handling
		// uniform while the raw enum stays visible in llm_debug output.
		return "stop"
	}
}

// --- model listing facet (doc Models.md: GET /v1beta/models, pageSize
// default 50 / max 1000, pageToken cursor) ---

// BuildListRequest describes the native model-list call.
func (a *GeminiAdapter) BuildListRequest(ep invoke.Endpoint) (*invoke.Request, error) {
	base := geminiNativeBaseURL(ep.BaseURL)
	header := http.Header{}
	apiKey := strings.TrimSpace(ep.Credentials.APIKey)
	if apiKey != "" {
		header.Set("X-Goog-Api-Key", apiKey)
	}
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    base + "/models?pageSize=1000",
		Header: header,
	}, nil
}

// ParseListResponse maps the Models.list page: bare ids (the "models/" resource
// prefix is stripped) so the frontend form receives the same vocabulary the
// chat model name takes.
func (a *GeminiAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	var parsed struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	models := make([]invoke.RemoteModel, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		if m.Name == "" {
			continue
		}
		models = append(models, invoke.RemoteModel{
			ID:          strings.TrimPrefix(m.Name, "models/"),
			DisplayName: m.DisplayName,
		})
	}
	return models, nil
}
