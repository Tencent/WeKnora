package googlegenai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	methodGenerate       = "generateContent"
	methodStreamGenerate = "streamGenerateContent"
)

// Client talks generateContent to one endpoint.
type Client struct {
	cfg Config
}

// New creates a client. The endpoint must already carry auth and headers.
func New(cfg Config) *Client {
	if cfg.ThinkingLevels == nil {
		cfg.ThinkingLevels = api.ThinkingLevelMap{}
	}
	return &Client{cfg: cfg}
}

// GetModelName returns the wire model name.
func (c *Client) GetModelName() string { return c.cfg.Endpoint.Model }

// GetModelID returns WeKnora's model identifier.
func (c *Client) GetModelID() string { return c.cfg.Endpoint.ModelID }

// requestURL builds {base}/models/{model}:generateContent or
// {base}/models/{model}:streamGenerateContent?alt=sse. A bare host base URL
// gets Settings.APIVersionPrefix appended; an already versioned base is used
// as-is. An explicit Endpoint.URL overrides the path entirely.
func (c *Client) requestURL(stream bool) string {
	ep := c.cfg.Endpoint
	if ep.URL == "" {
		base := strings.TrimRight(ep.BaseURL, "/")
		if prefix := strings.Trim(c.cfg.Settings.APIVersionPrefix, "/"); prefix != "" && isBareHost(base) {
			base += "/" + prefix
		}
		method := methodGenerate
		if stream {
			method = methodStreamGenerate
		}
		ep.URL = fmt.Sprintf("%s/models/%s:%s", base, ep.Model, method)
	} else if stream && strings.HasSuffix(ep.URL, ":"+methodGenerate) {
		// A vendor-supplied full URL names the non-streaming method; adding
		// alt=sse to it would still return one buffered JSON document.
		ep.URL = strings.TrimSuffix(ep.URL, ":"+methodGenerate) + ":" + methodStreamGenerate
	}
	if stream {
		query := make(map[string]string, len(ep.Query)+1)
		for k, v := range ep.Query {
			query[k] = v
		}
		query["alt"] = "sse"
		ep.Query = query
	}
	return ep.Resolve("")
}

func isBareHost(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), "/")
	}
	return u.Path == "" || u.Path == "/"
}

func (c *Client) send(
	ctx context.Context, messages []api.Message, opts *api.Options, stream bool,
) (*http.Response, []byte, error) {
	body, err := c.BuildRequestBody(messages, opts, stream)
	if err != nil {
		return nil, nil, err
	}
	target := c.requestURL(stream)
	req, data, err := c.cfg.Endpoint.NewRequest(ctx, target, body, stream)
	if err != nil {
		return nil, nil, err
	}
	api.LogRequest(ctx, target, c.cfg.Endpoint.Model, data, stream)
	resp, err := c.cfg.Endpoint.Do(req)
	if err != nil {
		return nil, data, err
	}
	return resp, data, nil
}

// Chat performs a non-streaming generation.
func (c *Client) Chat(ctx context.Context, messages []api.Message, opts *api.Options) (*types.ChatResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultChatTimeout)
	defer cancel()

	resp, _, err := c.send(ctx, messages, opts, false)
	if err != nil {
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Request] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, _, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, false)
		}
		if err != nil {
			return nil, fmt.Errorf("create chat completion: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	result, err := c.parseResponse(raw)
	if err != nil {
		return nil, err
	}
	api.LogUsage(ctx, c.cfg.Endpoint.Model, &result.Usage)
	return result, nil
}

// ChatStream performs a streaming generation over SSE.
func (c *Client) ChatStream(
	ctx context.Context, messages []api.Message, opts *api.Options,
) (<-chan types.StreamResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultStreamTimeout)

	resp, data, err := c.send(ctx, messages, opts, true)
	if err != nil {
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Stream] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, data, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, true)
		}
		if err != nil {
			cancel()
			return nil, fmt.Errorf("create chat completion stream: %w", err)
		}
	}

	ch := make(chan types.StreamResponse)
	dumper := api.NewStreamPacketDumper(c.cfg.Endpoint.Model, json.RawMessage(data))
	if dumper != nil {
		logger.Infof(ctx, "[LLM Stream Raw Dump] writing packets to %s", dumper.Path())
	}
	go func() {
		defer cancel()
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		if dumper != nil {
			defer dumper.Close()
		}
		c.processStream(ctx, resp.Body, ch, dumper)
	}()
	return ch, nil
}

// --- response decoding ---

type rawFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type rawPart struct {
	Text             string           `json:"text,omitempty"`
	Thought          bool             `json:"thought,omitempty"`
	ThoughtSignature string           `json:"thoughtSignature,omitempty"`
	FunctionCall     *rawFunctionCall `json:"functionCall,omitempty"`
}

type rawCandidate struct {
	Content struct {
		Role  string    `json:"role,omitempty"`
		Parts []rawPart `json:"parts,omitempty"`
	} `json:"content"`
	FinishReason string `json:"finishReason,omitempty"`
}

type rawUsage struct {
	PromptTokenCount        int  `json:"promptTokenCount"`
	CandidatesTokenCount    int  `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int  `json:"thoughtsTokenCount"`
	TotalTokenCount         int  `json:"totalTokenCount"`
	CachedContentTokenCount *int `json:"cachedContentTokenCount"`
}

type rawResponse struct {
	Candidates     []rawCandidate `json:"candidates,omitempty"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason,omitempty"`
	} `json:"promptFeedback,omitempty"`
	UsageMetadata *rawUsage `json:"usageMetadata,omitempty"`
	Error         *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func (c *Client) usage(u rawUsage) types.TokenUsage {
	out := types.TokenUsage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount,
		TotalTokens:      u.TotalTokenCount,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	switch {
	case u.CachedContentTokenCount != nil:
		cached := *u.CachedContentTokenCount
		out.SetPromptCacheUsage(cached, 0, max(0, u.PromptTokenCount-cached), true)
	case c.cfg.Settings.PromptCacheAcct:
		out.SetPromptCacheUsage(0, 0, 0, false)
	default:
		out.MarkPromptCacheUnsupported()
	}
	return out
}

// toolCall lifts a functionCall part into the neutral tool call, minting an
// id when the vendor sent none and keeping the thought signature as
// provider metadata so it can be replayed verbatim.
func (c *Client) toolCall(p rawPart) types.LLMToolCall {
	fc := p.FunctionCall
	id := fc.ID
	if id == "" {
		id = newCallID()
	}
	args := "{}"
	if len(fc.Args) > 0 && string(fc.Args) != "null" {
		args = string(fc.Args)
	}
	call := types.LLMToolCall{
		ID:       id,
		Type:     "function",
		Function: types.FunctionCall{Name: fc.Name, Arguments: args},
	}
	if p.ThoughtSignature != "" {
		sig, _ := json.Marshal(p.ThoughtSignature)
		call.ProviderMetadata = types.ToolCallMetadata{metadataThoughtSignature: sig}
	}
	return call
}

func newCallID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "call_000000000000"
	}
	return "call_" + hex.EncodeToString(b[:])
}

// mapFinishReason maps Gemini finish reasons onto the OpenAI vocabulary the
// rest of the system understands.
func mapFinishReason(reason string, hasToolCalls bool) string {
	switch reason {
	case "":
		return ""
	case "STOP":
		if hasToolCalls {
			return "tool_calls"
		}
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "content_filter"
	default:
		return strings.ToLower(reason)
	}
}

func (c *Client) parseResponse(raw []byte) (*types.ChatResponse, error) {
	var env rawResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil && env.Error.Message != "" {
		return nil, fmt.Errorf("API error: %s", env.Error.Message)
	}
	if len(env.Candidates) == 0 {
		if env.PromptFeedback != nil && env.PromptFeedback.BlockReason != "" {
			return nil, fmt.Errorf("blocked: %s", env.PromptFeedback.BlockReason)
		}
		return nil, fmt.Errorf("no response from API")
	}
	cand := env.Candidates[0]
	result := &types.ChatResponse{}
	var content, reasoning strings.Builder
	for _, p := range cand.Content.Parts {
		if p.ThoughtSignature != "" {
			result.ReasoningSignature = api.TagSignature(api.APIGoogleGenerativeAI, p.ThoughtSignature)
		}
		switch {
		case p.FunctionCall != nil:
			result.ToolCalls = append(result.ToolCalls, c.toolCall(p))
		case p.Thought:
			reasoning.WriteString(p.Text)
		default:
			content.WriteString(p.Text)
		}
	}
	result.Content = content.String()
	result.ReasoningContent = reasoning.String()
	result.FinishReason = mapFinishReason(cand.FinishReason, len(result.ToolCalls) > 0)
	// Classify the cache status even without usageMetadata: an empty
	// CacheStatus is indistinguishable from a miss downstream.
	usage := rawUsage{}
	if env.UsageMetadata != nil {
		usage = *env.UsageMetadata
	}
	result.Usage = c.usage(usage)
	return result, nil
}

// processStream decodes the alt=sse stream: every data line carries a full
// GenerateContentResponse chunk. Function calls arrive complete in one part;
// they are fed to the assembler as a name delta followed by an arguments
// delta so the early ResponseTypeToolCall notification fires as it does for
// the fragment-based protocols.
func (c *Client) processStream(
	ctx context.Context, body io.Reader, ch chan<- types.StreamResponse, dumper *api.StreamPacketDumper,
) {
	assembler := api.NewStreamAssembler(ctx, c.cfg.Endpoint.Model)
	reader := api.NewSSEReader(body)
	toolIndex := 0

	for {
		// The consumer went away (client disconnect, aborted agent turn): stop
		// decoding a stream nobody will read instead of spinning to EOF.
		if assembler.Aborted() {
			return
		}
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				assembler.End(ch)
			} else {
				assembler.Fail(ch, err)
			}
			return
		}
		if event == nil {
			continue
		}
		if event.Done {
			assembler.End(ch)
			return
		}
		if len(event.Data) == 0 {
			continue
		}
		if dumper != nil {
			raw := make([]byte, len(event.Data))
			copy(raw, event.Data)
			dumper.WritePacketRaw(raw)
		}

		var env rawResponse
		if err := json.Unmarshal(event.Data, &env); err != nil {
			assembler.Fail(ch, fmt.Errorf("decode stream chunk: %w", err))
			return
		}
		if env.Error != nil && env.Error.Message != "" {
			assembler.Fail(ch, fmt.Errorf("API stream error: %s", env.Error.Message))
			return
		}
		if env.UsageMetadata != nil {
			assembler.SetUsage(c.usage(*env.UsageMetadata))
		}
		if len(env.Candidates) == 0 {
			if env.PromptFeedback != nil && env.PromptFeedback.BlockReason != "" {
				assembler.Fail(ch, fmt.Errorf("blocked: %s", env.PromptFeedback.BlockReason))
				return
			}
			continue
		}

		cand := env.Candidates[0]
		var delta api.Delta
		var calls []rawPart
		for _, p := range cand.Content.Parts {
			if p.ThoughtSignature != "" {
				assembler.ReasoningSignature = api.TagSignature(api.APIGoogleGenerativeAI, p.ThoughtSignature)
			}
			switch {
			case p.FunctionCall != nil:
				calls = append(calls, p)
			case p.Thought:
				delta.Reasoning += p.Text
			default:
				delta.Content += p.Text
			}
		}
		if delta.Reasoning != "" || delta.Content != "" {
			assembler.Process(ch, delta)
		}
		for _, p := range calls {
			call := c.toolCall(p)
			idx := toolIndex
			toolIndex++
			assembler.Process(ch, api.Delta{ToolCalls: []api.ToolCallDelta{{
				Index: idx, ID: call.ID, Type: call.Type, Name: call.Function.Name,
			}}})
			if len(call.ProviderMetadata) > 0 {
				assembler.SetToolCallMetadata(idx, call.ProviderMetadata)
			}
			assembler.Process(ch, api.Delta{ToolCalls: []api.ToolCallDelta{{
				Index: idx, Arguments: call.Function.Arguments,
			}}})
		}
		if cand.FinishReason != "" {
			assembler.Process(ch, api.Delta{FinishReason: mapFinishReason(cand.FinishReason, toolIndex > 0)})
		}
	}
}
