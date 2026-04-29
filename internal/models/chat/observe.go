package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/observe"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// Unified debug + Langfuse decorator for Chat. Replaces three separate files
// (langfuse_wrapper.go, llm_debug.go, llm_debug_wrapper.go). Each decorator
// (debugChat, langfuseChat) is a thin shell that routes through observe.Wrap /
// observe.WrapStream; both no-op when their respective subsystem is disabled,
// so stacking them preserves the pre-refactor layering (NewChat applies debug
// then langfuse).

// chatInput bundles the arguments for Chat / ChatStream so we can pass a
// single struct through observe.Wrap's generic.
type chatInput struct {
	messages []Message
	opts     *ChatOptions
}

// streamAgg accumulates stream events for debug + Langfuse reporting.
type streamAgg struct {
	contentBuf   strings.Builder
	usage        *types.TokenUsage
	toolCalls    []types.LLMToolCall
	finishReason string
	streamErr    error
}

type debugChat struct {
	inner Chat
}

func (d *debugChat) GetModelName() string { return d.inner.GetModelName() }
func (d *debugChat) GetModelID() string   { return d.inner.GetModelID() }

func (d *debugChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	return runChat(ctx, d.inner, messages, opts)
}

func (d *debugChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	return runChatStream(ctx, d.inner, messages, opts)
}

type langfuseChat struct {
	inner Chat
}

func (l *langfuseChat) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseChat) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	return runChat(ctx, l.inner, messages, opts)
}

func (l *langfuseChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	return runChatStream(ctx, l.inner, messages, opts)
}

// runChat wraps a non-streaming Chat call with Langfuse + debug instrumentation.
func runChat(ctx context.Context, inner Chat, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	in := chatInput{messages: messages, opts: opts}
	call := observe.Call[chatInput, *types.ChatResponse]{
		Name:    "chat.completion",
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(v chatInput) any {
			return buildLangfuseMessages(v.messages)
		},
		LangfuseMetadata: func(v chatInput) map[string]any {
			return map[string]any{
				"model_id":  inner.GetModelID(),
				"streaming": false,
				"has_tools": v.opts != nil && len(v.opts.Tools) > 0,
			}
		},
		LangfuseParams: func(v chatInput) map[string]any {
			return buildLangfuseModelParams(v.opts)
		},
		LangfuseOutput: func(_ chatInput, resp *types.ChatResponse, _ error) any {
			if resp == nil {
				return nil
			}
			return map[string]any{
				"content":       resp.Content,
				"tool_calls":    resp.ToolCalls,
				"finish_reason": resp.FinishReason,
			}
		},
		Usage: func(_ chatInput, resp *types.ChatResponse) *langfuse.TokenUsage {
			if resp == nil {
				return nil
			}
			return convertUsage(&resp.Usage)
		},
		DebugRecord: func(v chatInput, resp *types.ChatResponse, callErr error, dur time.Duration) *logger.LLMCallRecord {
			return buildChatDebugRecord(inner.GetModelName(), v.messages, v.opts, resp, callErr, dur)
		},
	}
	return observe.Wrap(ctx, call, in, func(ctx context.Context, _ chatInput) (*types.ChatResponse, error) {
		return inner.Chat(ctx, messages, opts)
	})
}

// runChatStream wraps a streaming ChatStream call with Langfuse + debug
// instrumentation. The aggregator tracks content/usage/toolcalls/finish_reason
// as events flow through; observe.WrapStream calls markFirstToken when the
// first Answer token arrives so Langfuse can record TTFT.
func runChatStream(ctx context.Context, inner Chat, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	in := chatInput{messages: messages, opts: opts}
	call := observe.StreamCall[chatInput, types.StreamResponse, streamAgg]{
		Name:    "chat.completion.stream",
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(v chatInput) any {
			return buildLangfuseMessages(v.messages)
		},
		LangfuseMetadata: func(v chatInput) map[string]any {
			return map[string]any{
				"model_id":  inner.GetModelID(),
				"streaming": true,
				"has_tools": v.opts != nil && len(v.opts.Tools) > 0,
			}
		},
		LangfuseParams: func(v chatInput) map[string]any {
			return buildLangfuseModelParams(v.opts)
		},
		NewAgg: func() streamAgg { return streamAgg{} },
		Reduce: func(agg *streamAgg, ev types.StreamResponse, markFirstToken func()) {
			if ev.ResponseType == types.ResponseTypeAnswer && ev.Content != "" {
				markFirstToken()
				agg.contentBuf.WriteString(ev.Content)
			}
			if ev.ResponseType == types.ResponseTypeError {
				agg.streamErr = fmt.Errorf("%s", ev.Content)
			}
			if ev.Usage != nil {
				agg.usage = ev.Usage
			}
			if len(ev.ToolCalls) > 0 {
				agg.toolCalls = ev.ToolCalls
			}
			if ev.FinishReason != "" {
				agg.finishReason = ev.FinishReason
			}
		},
		LangfuseOutput: func(_ chatInput, agg streamAgg, _ error) any {
			return map[string]any{
				"content":       agg.contentBuf.String(),
				"tool_calls":    agg.toolCalls,
				"finish_reason": agg.finishReason,
			}
		},
		Usage: func(_ chatInput, agg streamAgg) *langfuse.TokenUsage {
			return convertUsage(agg.usage)
		},
		DebugRecord: func(v chatInput, agg streamAgg, _ error, dur time.Duration) *logger.LLMCallRecord {
			return buildChatStreamDebugRecord(inner.GetModelName(), v.messages, v.opts,
				agg.contentBuf.String(), agg.toolCalls, agg.usage, agg.streamErr, dur)
		},
	}
	return observe.WrapStream(ctx, call, in, func(ctx context.Context, _ chatInput) (<-chan types.StreamResponse, error) {
		return inner.ChatStream(ctx, messages, opts)
	})
}

// wrapChatDebug wraps c in the debug decorator when logger.LLMDebugEnabled().
// Called by NewChat before the langfuse wrapper.
func wrapChatDebug(c Chat, err error) (Chat, error) {
	if err != nil || !logger.LLMDebugEnabled() {
		return c, err
	}
	return &debugChat{inner: c}, nil
}

// wrapChatLangfuse wraps c in the Langfuse decorator when the manager is enabled.
func wrapChatLangfuse(c Chat, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	if !langfuse.GetManager().Enabled() {
		return c, nil
	}
	return &langfuseChat{inner: c}, nil
}

// --- Langfuse helpers (moved verbatim from langfuse_wrapper.go) ---

func buildLangfuseMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		entry := map[string]any{
			"role": m.Role,
		}
		if m.Content != "" {
			entry["content"] = m.Content
		}
		if len(m.MultiContent) > 0 {
			entry["content"] = m.MultiContent
		}
		if m.Name != "" {
			entry["name"] = m.Name
		}
		if m.ToolCallID != "" {
			entry["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			entry["tool_calls"] = m.ToolCalls
		}
		out = append(out, entry)
	}
	return out
}

func buildLangfuseModelParams(opts *ChatOptions) map[string]any {
	if opts == nil {
		return nil
	}
	params := map[string]any{}
	if opts.Temperature != 0 {
		params["temperature"] = opts.Temperature
	}
	if opts.TopP != 0 {
		params["top_p"] = opts.TopP
	}
	if opts.MaxTokens > 0 {
		params["max_tokens"] = opts.MaxTokens
	}
	if opts.MaxCompletionTokens > 0 {
		params["max_completion_tokens"] = opts.MaxCompletionTokens
	}
	if opts.FrequencyPenalty != 0 {
		params["frequency_penalty"] = opts.FrequencyPenalty
	}
	if opts.PresencePenalty != 0 {
		params["presence_penalty"] = opts.PresencePenalty
	}
	if opts.Seed != 0 {
		params["seed"] = opts.Seed
	}
	if opts.ToolChoice != "" {
		params["tool_choice"] = opts.ToolChoice
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func convertUsage(u *types.TokenUsage) *langfuse.TokenUsage {
	if u == nil {
		return nil
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	return &langfuse.TokenUsage{
		Input:  u.PromptTokens,
		Output: u.CompletionTokens,
		Total:  u.TotalTokens,
		Unit:   "TOKENS",
	}
}

// --- LLM debug helpers (moved verbatim from llm_debug.go) ---

func buildLLMMessages(messages []Message) []logger.LLMMessage {
	out := make([]logger.LLMMessage, 0, len(messages))
	for _, m := range messages {
		lm := logger.LLMMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			Images:     m.Images,
		}

		if lm.Content == "" && len(m.MultiContent) > 0 {
			var parts []string
			for _, mc := range m.MultiContent {
				switch mc.Type {
				case "text":
					parts = append(parts, mc.Text)
				case "image_url":
					if mc.ImageURL != nil {
						parts = append(parts, fmt.Sprintf("[image_url: %s]", truncateForDebug(mc.ImageURL.URL, 120)))
					}
				}
			}
			lm.Content = strings.Join(parts, "\n")
		}

		for _, tc := range m.ToolCalls {
			lm.ToolCalls = append(lm.ToolCalls, logger.LLMToolCallInfo{
				ID:        tc.ID,
				FuncName:  tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		out = append(out, lm)
	}
	return out
}

func buildOptionsSection(opts *ChatOptions) string {
	if opts == nil {
		return ""
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("Temperature=%.2f", opts.Temperature))
	if opts.TopP > 0 {
		parts = append(parts, fmt.Sprintf("TopP=%.2f", opts.TopP))
	}
	if opts.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("MaxTokens=%d", opts.MaxTokens))
	}
	if opts.MaxCompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("MaxCompletionTokens=%d", opts.MaxCompletionTokens))
	}
	if opts.FrequencyPenalty > 0 {
		parts = append(parts, fmt.Sprintf("FrequencyPenalty=%.2f", opts.FrequencyPenalty))
	}
	if opts.PresencePenalty > 0 {
		parts = append(parts, fmt.Sprintf("PresencePenalty=%.2f", opts.PresencePenalty))
	}
	if opts.ToolChoice != "" {
		parts = append(parts, fmt.Sprintf("ToolChoice=%s", opts.ToolChoice))
	}
	if len(opts.Format) > 0 {
		parts = append(parts, "ResponseFormat=json_object")
	}
	return strings.Join(parts, ", ")
}

func buildToolsSection(opts *ChatOptions) string {
	if opts == nil || len(opts.Tools) == 0 {
		return ""
	}
	var b strings.Builder
	for i, t := range opts.Tools {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s: %s", t.Function.Name, t.Function.Description)
	}
	return b.String()
}

func buildResponseToolCalls(tcs []types.LLMToolCall) []logger.LLMToolCallInfo {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]logger.LLMToolCallInfo, 0, len(tcs))
	for _, tc := range tcs {
		out = append(out, logger.LLMToolCallInfo{
			ID:        tc.ID,
			FuncName:  tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}

func usageString(u types.TokenUsage) string {
	return fmt.Sprintf("Prompt: %d, Completion: %d, Total: %d",
		u.PromptTokens, u.CompletionTokens, u.TotalTokens)
}

func buildChatDebugRecord(model string, messages []Message, opts *ChatOptions, resp *types.ChatResponse, callErr error, dur time.Duration) *logger.LLMCallRecord {
	record := &logger.LLMCallRecord{
		CallType: "Chat",
		Model:    model,
		Duration: dur,
	}

	record.Sections = append(record.Sections, logger.RecordSection{
		Title:   "Messages",
		Content: logger.FormatMessages(buildLLMMessages(messages)),
	})
	if s := buildOptionsSection(opts); s != "" {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Options", Content: s})
	}
	if s := buildToolsSection(opts); s != "" {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Tools", Content: s})
	}

	if resp != nil {
		var respText strings.Builder
		if resp.Content != "" {
			respText.WriteString("[assistant]\n")
			respText.WriteString(resp.Content)
			respText.WriteString("\n")
		}
		tcs := buildResponseToolCalls(resp.ToolCalls)
		if len(tcs) > 0 {
			respText.WriteString(logger.FormatToolCalls(tcs))
		}
		if respText.Len() > 0 {
			record.Sections = append(record.Sections, logger.RecordSection{Title: "Response", Content: respText.String()})
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Usage", Content: usageString(resp.Usage)})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	return record
}

func buildChatStreamDebugRecord(model string, messages []Message, opts *ChatOptions,
	fullContent string, toolCalls []types.LLMToolCall, usage *types.TokenUsage, callErr error, dur time.Duration) *logger.LLMCallRecord {
	record := &logger.LLMCallRecord{
		CallType: "Chat Stream",
		Model:    model,
		Duration: dur,
	}

	record.Sections = append(record.Sections, logger.RecordSection{
		Title:   "Messages",
		Content: logger.FormatMessages(buildLLMMessages(messages)),
	})
	if s := buildOptionsSection(opts); s != "" {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Options", Content: s})
	}
	if s := buildToolsSection(opts); s != "" {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Tools", Content: s})
	}

	var respText strings.Builder
	if fullContent != "" {
		respText.WriteString("[assistant]\n")
		respText.WriteString(fullContent)
		respText.WriteString("\n")
	}
	tcs := buildResponseToolCalls(toolCalls)
	if len(tcs) > 0 {
		respText.WriteString(logger.FormatToolCalls(tcs))
	}
	if respText.Len() > 0 {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Response", Content: respText.String()})
	}

	if usage != nil {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Usage", Content: usageString(*usage)})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	return record
}

func truncateForDebug(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + fmt.Sprintf("...(%d chars)", len(runes))
}
