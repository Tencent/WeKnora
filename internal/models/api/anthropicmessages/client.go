package anthropicmessages

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// defaultAnthropicVersion is the anthropic-version value used when the
// catalog supplies none.
const defaultAnthropicVersion = "2023-06-01"

// Client talks the Messages protocol to one endpoint.
type Client struct {
	cfg Config
}

// New creates a client.
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

// Settings exposes the resolved configuration.
func (c *Client) Settings() Config { return c.cfg }

// URL resolves the messages endpoint. Operators paste base URLs in three
// shapes: a host ("https://api.anthropic.com"), a versioned root
// (".../v1", ".../anthropic/v1") or the full messages URL.
func (c *Client) URL() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	base := strings.TrimRight(c.cfg.Endpoint.BaseURL, "/")
	u, err := url.Parse(base)
	path := ""
	if err == nil {
		path = strings.TrimRight(u.Path, "/")
	}
	switch {
	case strings.HasSuffix(path, "/messages"):
		return base
	case strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/v1beta"):
		return base + "/messages"
	default:
		return base + "/v1/messages"
	}
}

func (c *Client) send(
	ctx context.Context, messages []api.Message, opts *api.Options, stream bool,
) (*httpResponse, error) {
	body, err := c.BuildRequestBody(messages, opts, stream)
	if err != nil {
		return nil, err
	}
	target := c.URL()
	req, data, err := c.cfg.Endpoint.NewRequest(ctx, target, body, stream)
	if err != nil {
		return nil, err
	}
	// anthropic-version is mandatory; an empty value is rejected with 400, so
	// fall back to the baseline version when the catalog left it unset.
	version := c.cfg.Settings.Version
	if version == "" {
		version = defaultAnthropicVersion
	}
	req.Header.Set("anthropic-version", version)
	if beta := c.betaHeader(c.planThinking(opts), opts, req.Header.Get("anthropic-beta")); beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}
	api.LogRequest(ctx, target, c.cfg.Endpoint.Model, data, stream)
	resp, err := c.cfg.Endpoint.Do(req)
	if err != nil {
		return nil, err
	}
	return &httpResponse{body: resp.Body, contentType: resp.Header.Get("Content-Type"), data: data}, nil
}

type httpResponse struct {
	body        io.ReadCloser
	contentType string
	data        []byte
}

// Chat performs a non-streaming call.
func (c *Client) Chat(ctx context.Context, messages []api.Message, opts *api.Options) (*types.ChatResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultChatTimeout)
	defer cancel()
	resp, err := c.send(ctx, messages, opts, false)
	if err != nil {
		// Claude's own models all accept images, but this protocol is also
		// the compatibility surface of vendors whose text-only models do
		// not, and the other three clients already degrade this way.
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Request] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, false)
		}
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.body.Close() }()
	raw, err := io.ReadAll(resp.body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var result *types.ChatResponse
	if strings.Contains(strings.ToLower(resp.contentType), "text/event-stream") {
		// Some proxies answer non-stream calls with SSE; consume it fully.
		result, err = c.collectStream(ctx, strings.NewReader(string(raw)))
	} else {
		result, err = c.parseResponse(raw)
	}
	if err != nil {
		return nil, err
	}
	api.LogUsage(ctx, c.cfg.Endpoint.Model, &result.Usage)
	return result, nil
}

// ChatStream performs a streaming call.
func (c *Client) ChatStream(
	ctx context.Context, messages []api.Message, opts *api.Options,
) (<-chan types.StreamResponse, error) {
	ctx, cancel := api.WithLLMTimeout(ctx, api.DefaultStreamTimeout)
	resp, err := c.send(ctx, messages, opts, true)
	if err != nil {
		if api.IsMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Stream] Model %s does not support multimodal, retrying without images",
				c.cfg.Endpoint.Model)
			resp, err = c.send(ctx, api.StripImagesFromMessages(messages), opts, true)
		}
		if err != nil {
			cancel()
			return nil, err
		}
	}
	ch := make(chan types.StreamResponse)
	dumper := api.NewStreamPacketDumper(c.cfg.Endpoint.Model, json.RawMessage(resp.data))
	go func() {
		defer cancel()
		defer close(ch)
		defer func() { _ = resp.body.Close() }()
		if dumper != nil {
			defer dumper.Close()
		}
		c.processStream(ctx, resp.body, ch, dumper)
	}()
	return ch, nil
}

// --- decoding ---

type usageBlock struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

type responseBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
}

type response struct {
	Type       string          `json:"type"`
	Content    []responseBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      usageBlock      `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type streamEvent struct {
	Type         string         `json:"type"`
	Index        int            `json:"index"`
	ContentBlock *responseBlock `json:"content_block,omitempty"`
	Message      *struct {
		Usage usageBlock `json:"usage"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta,omitempty"`
	Usage *usageBlock `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) usageFrom(u usageBlock) types.TokenUsage {
	read := valueOrZero(u.CacheReadInputTokens)
	write := valueOrZero(u.CacheCreationInputTokens)
	prompt := u.InputTokens + read + write
	out := types.TokenUsage{
		PromptTokens: prompt, CompletionTokens: u.OutputTokens, TotalTokens: prompt + u.OutputTokens,
	}
	reported := u.CacheReadInputTokens != nil || u.CacheCreationInputTokens != nil
	if !reported && !c.cfg.Settings.PromptCacheAccounting {
		out.MarkPromptCacheUnsupported()
		return out
	}
	out.SetPromptCacheUsage(read, write, max(0, prompt-read), reported)
	return out
}

func (c *Client) parseResponse(raw []byte) (*types.ChatResponse, error) {
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}
	result := &types.ChatResponse{Usage: c.usageFrom(resp.Usage)}
	var text, thinking []string
	// Interleaved thinking puts several signed thinking blocks in one reply,
	// each signed over its own text, so they are kept separately for replay.
	// ReasoningContent / ReasoningSignature stay filled for readers and for
	// older stored turns, but they are no longer what goes back on the wire.
	var stored []thinkingBlock
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				text = append(text, block.Text)
			}
		case "thinking":
			thinking = append(thinking, block.Thinking)
			stored = append(stored, newThinkingBlock(block.Thinking, block.Signature))
			if block.Signature != "" {
				result.ReasoningSignature = api.TagSignature(api.APIAnthropicMessages, block.Signature)
			}
		case "redacted_thinking":
			stored = append(stored, newRedactedThinkingBlock(block.Data))
		case "tool_use":
			input := string(block.Input)
			if input == "" {
				input = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, types.LLMToolCall{
				ID: block.ID, Type: "function",
				Function: types.FunctionCall{Name: block.Name, Arguments: input},
			})
		}
	}
	result.Content = strings.Join(text, "")
	result.ReasoningContent = strings.Join(thinking, "")
	if blocks := thinkingBlocksMetadata(stored); blocks != nil {
		result.ReasoningMetadata = types.ProviderMetadata{MetadataThinkingBlocks: blocks}
	}
	result.FinishReason = mapStopReason(resp.StopReason, len(result.ToolCalls), false)
	return result, nil
}

// toolInput accumulates one streamed tool_use block.
type toolInput struct {
	call    types.LLMToolCall
	initial string
	json    strings.Builder
	closed  bool
}

// streamState tracks per-index content blocks of one streamed message.
type streamState struct {
	tools     map[int]*toolInput
	toolOrder []int
	blockType map[int]string
	signature string
	// thinking accumulates each thinking / redacted_thinking block by its
	// content-block index, and thinkingOrder keeps the order they started in.
	// Interleaved thinking sends several, each with its own signature_delta;
	// accumulating into one string would splice two signatures together and
	// produce one that verifies against nothing.
	thinking      map[int]*thinkingBlock
	thinkingOrder []int
	stop          string
	usage         *types.TokenUsage
}

func newStreamState() *streamState {
	return &streamState{
		tools:     map[int]*toolInput{},
		blockType: map[int]string{},
		thinking:  map[int]*thinkingBlock{},
	}
}

// thinkingAt returns the block being streamed at this content-block index,
// creating it on first use. A vendor that sends deltas without a
// content_block_start still gets a block rather than losing the text.
func (s *streamState) thinkingAt(index int, blockType string) *thinkingBlock {
	if b, ok := s.thinking[index]; ok {
		return b
	}
	b := &thinkingBlock{Type: blockType}
	s.thinking[index] = b
	s.thinkingOrder = append(s.thinkingOrder, index)
	return b
}

// thinkingBlocks returns the finished blocks in the order Claude sent them.
func (s *streamState) thinkingBlocks() []thinkingBlock {
	blocks := make([]thinkingBlock, 0, len(s.thinkingOrder))
	for _, index := range s.thinkingOrder {
		if b := s.thinking[index]; b != nil {
			blocks = append(blocks, *b)
		}
	}
	return blocks
}

func (s *streamState) calls() []types.LLMToolCall {
	indexes := make([]int, 0, len(s.tools))
	for index := range s.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var calls []types.LLMToolCall
	for _, index := range indexes {
		t := s.tools[index]
		if !t.closed {
			// A cut-off stream often starts the next tool_use with {}. Executing
			// that empty object is worse than omitting it.
			continue
		}
		call := t.call
		call.Function.Arguments = t.initial
		if t.json.Len() > 0 {
			call.Function.Arguments = t.json.String()
		}
		calls = append(calls, call)
	}
	return calls
}

func (s *streamState) incomplete() bool {
	for _, t := range s.tools {
		if !t.closed {
			return true
		}
	}
	return false
}

func (s *streamState) toolIndex(blockIndex int) int {
	for i, idx := range s.toolOrder {
		if idx == blockIndex {
			return i
		}
	}
	s.toolOrder = append(s.toolOrder, blockIndex)
	return len(s.toolOrder) - 1
}

// consume folds one event into the state and returns the protocol-neutral
// delta to hand to the assembler (may be empty).
func (c *Client) consume(s *streamState, ev streamEvent) api.Delta {
	var d api.Delta
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			u := c.usageFrom(ev.Message.Usage)
			s.usage = &u
		}
	case "content_block_start":
		if ev.ContentBlock == nil {
			break
		}
		s.blockType[ev.Index] = ev.ContentBlock.Type
		switch ev.ContentBlock.Type {
		case "tool_use":
			initial := string(ev.ContentBlock.Input)
			if initial == "" || initial == "null" {
				initial = "{}"
			}
			s.tools[ev.Index] = &toolInput{
				initial: initial,
				call: types.LLMToolCall{
					ID: ev.ContentBlock.ID, Type: "function",
					Function: types.FunctionCall{Name: ev.ContentBlock.Name},
				},
			}
			d.ToolCalls = []api.ToolCallDelta{{
				Index: s.toolIndex(ev.Index), ID: ev.ContentBlock.ID, Type: "function", Name: ev.ContentBlock.Name,
			}}
		case "redacted_thinking":
			s.thinkingAt(ev.Index, "redacted_thinking").Data = ev.ContentBlock.Data
		case "thinking":
			block := s.thinkingAt(ev.Index, "thinking")
			if ev.ContentBlock.Thinking != "" {
				block.Thinking += ev.ContentBlock.Thinking
				d.Reasoning = ev.ContentBlock.Thinking
			}
		case "text":
			if ev.ContentBlock.Text != "" {
				d.Content = ev.ContentBlock.Text
			}
		}
	case "content_block_delta":
		if ev.Delta == nil {
			break
		}
		switch ev.Delta.Type {
		case "text_delta":
			d.Content = ev.Delta.Text
		case "thinking_delta":
			d.Reasoning = ev.Delta.Thinking
			s.thinkingAt(ev.Index, "thinking").Thinking += ev.Delta.Thinking
		case "signature_delta":
			// The signature belongs to the block at this index; the top-level
			// field keeps carrying the last one for readers that still use it.
			block := s.thinkingAt(ev.Index, "thinking")
			block.Signature += ev.Delta.Signature
			s.signature = block.Signature
		case "input_json_delta":
			if t := s.tools[ev.Index]; t != nil {
				t.json.WriteString(ev.Delta.PartialJSON)
				d.ToolCalls = []api.ToolCallDelta{{Index: s.toolIndex(ev.Index), Arguments: ev.Delta.PartialJSON}}
			}
		}
	case "content_block_stop":
		if t := s.tools[ev.Index]; t != nil {
			t.closed = true
			if t.json.Len() == 0 {
				// No input_json_delta carried any payload — an argument-less
				// tool, or a vendor that puts the whole input on
				// content_block_start. The assembler only emits its early
				// ResponseTypeToolCall marker once a chunk updates the
				// arguments of an already-named call, so replay the initial
				// input here. Without it the UI never learns the call started.
				// The final arguments are unchanged: calls() uses t.initial too.
				d.ToolCalls = []api.ToolCallDelta{{Index: s.toolIndex(ev.Index), Arguments: t.initial}}
			}
		}
	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			s.stop = ev.Delta.StopReason
		}
		if ev.Usage != nil {
			u := c.usageFrom(*ev.Usage)
			if s.usage != nil {
				// message_delta carries cumulative output tokens; input
				// counters arrived on message_start.
				u.PromptTokens = max(u.PromptTokens, s.usage.PromptTokens)
				u.SetPromptCacheUsage(
					max(u.CacheReadTokens, s.usage.CacheReadTokens),
					max(u.CacheWriteTokens, s.usage.CacheWriteTokens),
					max(0, u.PromptTokens-max(u.CacheReadTokens, s.usage.CacheReadTokens)),
					u.CacheReported || s.usage.CacheReported,
				)
				u.TotalTokens = u.PromptTokens + u.CompletionTokens
			}
			s.usage = &u
		}
	}
	return d
}

func (c *Client) processStream(
	ctx context.Context, body io.Reader, ch chan<- types.StreamResponse, dumper *api.StreamPacketDumper,
) {
	assembler := api.NewStreamAssembler(ctx, c.cfg.Endpoint.Model)
	state := newStreamState()
	reader := api.NewSSEReader(body)
	finish := func() {
		calls := state.calls()
		assembler.ReplaceToolCalls(calls)
		assembler.ReasoningSignature = api.TagSignature(api.APIAnthropicMessages, state.signature)
		if blocks := thinkingBlocksMetadata(state.thinkingBlocks()); blocks != nil {
			assembler.ReasoningMetadata = types.ProviderMetadata{MetadataThinkingBlocks: blocks}
		}
		if state.usage != nil {
			assembler.SetUsage(*state.usage)
		}
		assembler.Process(ch, api.Delta{FinishReason: mapStopReason(state.stop, len(calls), state.incomplete())})
		assembler.End(ch)
	}
	for {
		// The consumer went away (client disconnect, aborted agent turn): stop
		// decoding a stream nobody will read instead of spinning to EOF.
		if assembler.Aborted() {
			return
		}
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				finish()
			} else {
				assembler.Fail(ch, err)
			}
			return
		}
		if event == nil || event.Done {
			if event != nil && event.Done {
				finish()
				return
			}
			continue
		}
		if len(event.Data) == 0 {
			continue
		}
		if dumper != nil {
			raw := make([]byte, len(event.Data))
			copy(raw, event.Data)
			dumper.WritePacketRaw(raw)
		}
		var ev streamEvent
		if err := json.Unmarshal(event.Data, &ev); err != nil {
			assembler.Fail(ch, fmt.Errorf("decode SSE response: %w", err))
			return
		}
		if ev.Error != nil && ev.Error.Message != "" {
			assembler.Fail(ch, fmt.Errorf("API stream error: %s", ev.Error.Message))
			return
		}
		delta := c.consume(state, ev)
		if delta.Content != "" || delta.Reasoning != "" || len(delta.ToolCalls) > 0 {
			assembler.Process(ch, delta)
		}
		if ev.Type == "message_stop" {
			finish()
			return
		}
	}
}

// collectStream folds an SSE body into one ChatResponse (used when a proxy
// answers a non-stream request with SSE).
func (c *Client) collectStream(ctx context.Context, body io.Reader) (*types.ChatResponse, error) {
	ch := make(chan types.StreamResponse, 64)
	go func() {
		defer close(ch)
		c.processStream(ctx, body, ch, nil)
	}()
	result := &types.ChatResponse{}
	var content, reasoning strings.Builder
	for chunk := range ch {
		switch chunk.ResponseType {
		case types.ResponseTypeError:
			return nil, fmt.Errorf("%s", chunk.Content)
		case types.ResponseTypeThinking:
			reasoning.WriteString(chunk.Content)
		case types.ResponseTypeAnswer:
			content.WriteString(chunk.Content)
			if len(chunk.ToolCalls) > 0 {
				result.ToolCalls = chunk.ToolCalls
			}
			if chunk.Usage != nil {
				result.Usage = *chunk.Usage
			}
			if chunk.FinishReason != "" {
				result.FinishReason = chunk.FinishReason
			}
			if sig, ok := chunk.Data["reasoning_signature"].(string); ok {
				result.ReasoningSignature = sig
			}
			if md, ok := chunk.Data["reasoning_metadata"].(types.ProviderMetadata); ok {
				result.ReasoningMetadata = md
			}
		}
	}
	result.Content = content.String()
	result.ReasoningContent = reasoning.String()
	return result, nil
}

func valueOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
