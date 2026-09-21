package openairesponses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

const defaultPath = "/responses"

// Client talks the Responses protocol to one endpoint.
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

// Settings exposes the resolved settings (diagnostics, tests).
func (c *Client) Settings() Config { return c.cfg }

func (c *Client) url() string { return c.cfg.Endpoint.Resolve(defaultPath) }

func (c *Client) send(
	ctx context.Context, messages []api.Message, opts *api.Options, stream bool,
) (*http.Response, []byte, error) {
	opts = api.WithSessionCacheKey(ctx, opts)
	body, err := c.BuildRequestBody(messages, opts, stream)
	if err != nil {
		return nil, nil, err
	}
	url := c.url()
	req, data, err := c.cfg.Endpoint.NewRequest(ctx, url, body, stream)
	if err != nil {
		return nil, nil, err
	}
	if c.cfg.Settings.PromptCacheKey {
		key := c.cfg.SessionID
		if opts != nil && opts.PromptCacheKey != "" {
			key = opts.PromptCacheKey
		}
		api.AttachSessionAffinityHeaders(req, api.ClampPromptCacheKey(key))
	}
	api.LogRequest(ctx, url, c.cfg.Endpoint.Model, data, stream)
	resp, err := c.cfg.Endpoint.Do(req)
	if err != nil {
		return nil, data, err
	}
	return resp, data, nil
}

// Chat performs a non-streaming response creation.
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
			return nil, fmt.Errorf("create response: %w", err)
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

// ChatStream performs a streaming response creation.
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
			return nil, fmt.Errorf("create response stream: %w", err)
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

type rawContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// rawOutputItem is the union of the output item shapes this package reads:
// message, function_call and reasoning.
type rawOutputItem struct {
	Type      string           `json:"type"`
	ID        json.RawMessage  `json:"id,omitempty"`
	Status    string           `json:"status,omitempty"`
	Role      string           `json:"role,omitempty"`
	Content   []rawContentPart `json:"content,omitempty"`
	CallID    string           `json:"call_id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
	Summary   []rawContentPart `json:"summary,omitempty"`
}

type rawUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
}

func (u rawUsage) toTokenUsage() types.TokenUsage {
	out := types.TokenUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if u.InputTokensDetails != nil {
		cached := u.InputTokensDetails.CachedTokens
		out.SetPromptCacheUsage(cached, 0, max(0, u.InputTokens-cached), true)
	} else {
		out.SetPromptCacheUsage(0, 0, 0, false)
	}
	return out
}

type rawError struct {
	Code    any    `json:"code,omitempty"`
	Message string `json:"message"`
}

type rawResponse struct {
	ID                string `json:"id,omitempty"`
	Status            string `json:"status,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
	Output []json.RawMessage `json:"output,omitempty"`
	Usage  *rawUsage         `json:"usage,omitempty"`
	Error  *rawError         `json:"error,omitempty"`
}

// finishReason maps the response status onto the Chat Completions
// vocabulary the rest of the engine understands.
func (r *rawResponse) finishReason(hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	if r != nil && r.Status == "incomplete" && r.IncompleteDetails != nil {
		switch r.IncompleteDetails.Reason {
		case "max_output_tokens":
			return "length"
		case "content_filter":
			return "content_filter"
		}
	}
	return "stop"
}

func decodeItem(raw json.RawMessage) (rawOutputItem, error) {
	var item rawOutputItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return rawOutputItem{}, fmt.Errorf("decode output item: %w", err)
	}
	return item, nil
}

// streamItem decodes the item carried by an output_item event. An event that
// carries no item at all is simply nothing to decode, but an item we cannot
// read is lost output, possibly a whole function_call, so the caller must
// surface it instead of moving on.
func streamItem(raw json.RawMessage) (rawOutputItem, bool, error) {
	if len(raw) == 0 {
		return rawOutputItem{}, false, nil
	}
	item, err := decodeItem(raw)
	if err != nil {
		return rawOutputItem{}, false, err
	}
	return item, true, nil
}

func itemText(item rawOutputItem) string {
	var b strings.Builder
	for _, part := range item.Content {
		if part.Type == "output_text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func itemSummary(item rawOutputItem) string {
	texts := make([]string, 0, len(item.Summary))
	for _, part := range item.Summary {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func toolCallMetadata(item rawOutputItem) types.ToolCallMetadata {
	if len(item.ID) == 0 || string(item.ID) == "null" {
		return nil
	}
	return types.ToolCallMetadata{metadataToolCallItemID: item.ID}
}

func reasoningMetadata(items []json.RawMessage) types.ProviderMetadata {
	if len(items) == 0 {
		return nil
	}
	merged, err := json.Marshal(items)
	if err != nil {
		return nil
	}
	return types.ProviderMetadata{metadataReasoningItems: merged}
}

func (c *Client) parseResponse(raw []byte) (*types.ChatResponse, error) {
	var env rawResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil && env.Error.Message != "" {
		return nil, fmt.Errorf("API error: %s", env.Error.Message)
	}
	if env.Status == "failed" {
		return nil, fmt.Errorf("API error: response failed")
	}

	result := &types.ChatResponse{}
	var content, reasoning []string
	var reasoningItems []json.RawMessage
	for _, rawItem := range env.Output {
		// An output item we cannot read may be the round's tool call. Dropping
		// it hands the agent what looks like a plain answer, so fail instead.
		item, err := decodeItem(rawItem)
		if err != nil {
			return nil, err
		}
		switch item.Type {
		case "message":
			content = append(content, itemText(item))
		case "function_call":
			result.ToolCalls = append(result.ToolCalls, types.LLMToolCall{
				ID:               item.CallID,
				Type:             "function",
				Function:         types.FunctionCall{Name: item.Name, Arguments: item.Arguments},
				ProviderMetadata: toolCallMetadata(item),
			})
		case "reasoning":
			if summary := itemSummary(item); summary != "" {
				reasoning = append(reasoning, summary)
			}
			reasoningItems = append(reasoningItems, rawItem)
		}
	}
	result.Content = strings.Join(content, "")
	result.ReasoningContent = strings.Join(reasoning, "\n")
	result.ReasoningMetadata = reasoningMetadata(reasoningItems)
	result.FinishReason = env.finishReason(len(result.ToolCalls) > 0)
	// Classify the cache status even without a usage block, so an empty
	// CacheStatus never reaches the usage dashboards as a silent miss.
	usage := rawUsage{}
	if env.Usage != nil {
		usage = *env.Usage
	}
	result.Usage = usage.toTokenUsage()
	return result, nil
}

// --- streaming ---

// rawStreamEvent is the union of the fields this package reads from the
// Responses SSE events. Every event carries "type".
type rawStreamEvent struct {
	Type         string          `json:"type"`
	Delta        string          `json:"delta,omitempty"`
	ItemID       string          `json:"item_id,omitempty"`
	OutputIndex  int             `json:"output_index,omitempty"`
	SummaryIndex int             `json:"summary_index,omitempty"`
	Item         json.RawMessage `json:"item,omitempty"`
	Response     *rawResponse    `json:"response,omitempty"`
	// Message/Code are set on the top-level "error" event.
	Message string `json:"message,omitempty"`
	Code    any    `json:"code,omitempty"`
}

// streamState tracks the function_call items seen so far so argument deltas
// can be routed to the assembler index that announced the call.
type streamState struct {
	toolIndex      map[string]int // item id -> assembler index
	argsSeen       map[string]bool
	nextToolIndex  int
	reasoningItems []json.RawMessage
}

func (st *streamState) indexFor(itemID string) int {
	if idx, ok := st.toolIndex[itemID]; ok {
		return idx
	}
	idx := st.nextToolIndex
	st.nextToolIndex++
	st.toolIndex[itemID] = idx
	return idx
}

func (c *Client) processStream(
	ctx context.Context, body io.Reader, ch chan<- types.StreamResponse, dumper *api.StreamPacketDumper,
) {
	assembler := api.NewStreamAssembler(ctx, c.cfg.Endpoint.Model)
	reader := api.NewSSEReader(body)
	st := &streamState{toolIndex: map[string]int{}, argsSeen: map[string]bool{}}

	finish := func() {
		assembler.ReasoningMetadata = reasoningMetadata(st.reasoningItems)
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
		if event == nil {
			continue
		}
		if event.Done {
			finish()
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

		var ev rawStreamEvent
		if err := json.Unmarshal(event.Data, &ev); err != nil {
			// An event whose *type* we do not know is routine and the switch
			// below already ignores it. A payload that is not JSON at all is
			// not: it is a truncated frame or a gateway error page, i.e. a
			// hole in the answer. Skipping it runs the loop to EOF, which the
			// caller cannot tell from a complete reply and then stores as the
			// model's answer. Fail, as the Completions loop does.
			assembler.Fail(ch, fmt.Errorf("decode stream chunk: %w", err))
			return
		}

		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				assembler.Process(ch, api.Delta{Content: ev.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta != "" {
				assembler.Process(ch, api.Delta{Reasoning: ev.Delta})
			}
		case "response.reasoning_summary_part.added":
			// Mirror the non-stream join of summary parts with a newline.
			if ev.SummaryIndex > 0 {
				assembler.Process(ch, api.Delta{Reasoning: "\n"})
			}
		case "response.output_item.added":
			item, ok, err := streamItem(ev.Item)
			if err != nil {
				assembler.Fail(ch, err)
				return
			}
			if !ok || item.Type != "function_call" {
				continue
			}
			itemID := streamItemID(item, ev.OutputIndex)
			idx := st.indexFor(itemID)
			assembler.Process(ch, api.Delta{ToolCalls: []api.ToolCallDelta{{
				Index: idx, ID: item.CallID, Type: "function", Name: item.Name,
			}}})
			assembler.SetToolCallMetadata(idx, toolCallMetadata(item))
		case "response.function_call_arguments.delta":
			if ev.Delta == "" {
				continue
			}
			// Correlate on the same key output_item.added registered. Vendors
			// that omit item ids would otherwise send every call's arguments
			// to the single index keyed by "", merging distinct tool calls.
			key := streamEventKey(ev.ItemID, ev.OutputIndex)
			st.argsSeen[key] = true
			assembler.Process(ch, api.Delta{ToolCalls: []api.ToolCallDelta{{
				Index: st.indexFor(key), Arguments: ev.Delta,
			}}})
		case "response.output_item.done":
			item, ok, err := streamItem(ev.Item)
			if err != nil {
				assembler.Fail(ch, err)
				return
			}
			if !ok {
				continue
			}
			switch item.Type {
			case "reasoning":
				st.reasoningItems = append(st.reasoningItems, ev.Item)
			case "function_call":
				// Providers that never stream argument deltas deliver the
				// arguments only on the done item.
				itemID := streamItemID(item, ev.OutputIndex)
				if !st.argsSeen[itemID] && item.Arguments != "" {
					st.argsSeen[itemID] = true
					assembler.Process(ch, api.Delta{ToolCalls: []api.ToolCallDelta{{
						Index: st.indexFor(itemID), ID: item.CallID, Type: "function",
						Name: item.Name, Arguments: item.Arguments,
					}}})
					assembler.SetToolCallMetadata(st.indexFor(itemID), toolCallMetadata(item))
				}
			}
		case "response.completed", "response.incomplete":
			c.completeStream(ch, assembler, st, ev.Response, ev.Type == "response.incomplete")
			return
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			assembler.Fail(ch, fmt.Errorf("API stream error: %s", msg))
			return
		case "error":
			assembler.Fail(ch, fmt.Errorf("API stream error: %s", orDefault(ev.Message, "unknown error")))
			return
		}
	}
}

// streamItemID returns the item id used to correlate added/delta/done
// events, falling back to the output index when the vendor omits ids.
func streamItemID(item rawOutputItem, outputIndex int) string {
	var id string
	if len(item.ID) > 0 {
		_ = json.Unmarshal(item.ID, &id)
	}
	return streamEventKey(id, outputIndex)
}

// streamEventKey is the correlation key for one output item: its id when the
// vendor sends one, otherwise its position in the output list.
func streamEventKey(itemID string, outputIndex int) string {
	if itemID != "" {
		return itemID
	}
	return fmt.Sprintf("output_index:%d", outputIndex)
}

// completeStream handles the terminal response.completed / response.incomplete
// event: usage, finish reason, reasoning artifacts and the closing chunks.
func (c *Client) completeStream(
	ch chan<- types.StreamResponse, assembler *api.StreamAssembler, st *streamState, resp *rawResponse, incomplete bool,
) {
	if resp != nil {
		if resp.Usage != nil {
			assembler.SetUsage(resp.Usage.toTokenUsage())
		}
		// Fall back to the final response body for reasoning items when the
		// vendor never emitted output_item.done for them.
		if len(st.reasoningItems) == 0 {
			for _, rawItem := range resp.Output {
				// Tolerated on purpose, unlike the item events above: the
				// deltas already carried the answer and its tool calls, so an
				// unreadable item here costs at most a reasoning artifact.
				if item, err := decodeItem(rawItem); err == nil && item.Type == "reasoning" {
					st.reasoningItems = append(st.reasoningItems, rawItem)
				}
			}
		}
	}
	finishReason := resp.finishReason(assembler.ToolCallCount() > 0)
	if incomplete && finishReason == "stop" {
		finishReason = "length"
	}
	assembler.Process(ch, api.Delta{FinishReason: finishReason})
	assembler.ReasoningMetadata = reasoningMetadata(st.reasoningItems)
	assembler.End(ch)
}
