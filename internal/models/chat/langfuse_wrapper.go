package chat

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// langfuseChat wraps a Chat implementation and emits a Langfuse generation
// observation for every Chat/ChatStream call, capturing prompt, response and
// token usage. The wrapper is only installed when the Langfuse manager is
// enabled, so there is no cost for deployments that don't use Langfuse.
type langfuseChat struct {
	inner Chat
}

func (l *langfuseChat) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseChat) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.Chat(ctx, messages, opts)
	}

	genCtx, gen := mgr.StartGeneration(
		ctx,
		buildLangfuseGenerationOptions(
			ctx,
			"chat.completion",
			l.inner.GetModelName(),
			l.inner.GetModelID(),
			messages,
			opts,
			false,
		),
	)

	resp, err := l.inner.Chat(genCtx, messages, opts)

	var usage *langfuse.TokenUsage
	var output interface{}
	if resp != nil {
		usage = convertUsage(&resp.Usage)
		output = buildLangfuseTraceOutput(
			ctx,
			resp.Content,
			resp.ReasoningContent,
			resp.FinishReason,
			resp.ToolCalls,
			false,
		)
	}
	gen.Finish(output, usage, buildLangfuseTraceError(ctx, err))
	return resp, err
}

func (l *langfuseChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.ChatStream(ctx, messages, opts)
	}

	genCtx, gen := mgr.StartGeneration(
		ctx,
		buildLangfuseGenerationOptions(
			ctx,
			"chat.completion.stream",
			l.inner.GetModelName(),
			l.inner.GetModelID(),
			messages,
			opts,
			true,
		),
	)

	ch, err := l.inner.ChatStream(genCtx, messages, opts)
	if err != nil {
		gen.Finish(nil, nil, buildLangfuseTraceError(ctx, err))
		return ch, err
	}
	if ch == nil {
		gen.Finish(nil, nil, nil)
		return nil, nil
	}

	wrapped := make(chan types.StreamResponse)
	go func() {
		defer close(wrapped)
		var contentBuf []byte
		var reasoningBuf []byte
		var usage *types.TokenUsage
		var toolCalls []types.LLMToolCall
		var finishReason string
		var firstToken bool
		var streamError bool

		for resp := range ch {
			if resp.ResponseType == types.ResponseTypeError {
				streamError = true
			}
			if resp.ResponseType == types.ResponseTypeThinking && resp.Content != "" {
				if !firstToken {
					gen.MarkCompletionStart(time.Now())
					firstToken = true
				}
				reasoningBuf = append(reasoningBuf, resp.Content...)
			}
			if resp.ResponseType == types.ResponseTypeAnswer && resp.Content != "" {
				if !firstToken {
					gen.MarkCompletionStart(time.Now())
					firstToken = true
				}
				contentBuf = append(contentBuf, resp.Content...)
			}
			if resp.Usage != nil {
				usage = resp.Usage
			}
			if len(resp.ToolCalls) > 0 {
				toolCalls = resp.ToolCalls
			}
			if resp.FinishReason != "" {
				finishReason = resp.FinishReason
			}
			wrapped <- resp
		}

		output := buildLangfuseTraceOutput(
			ctx,
			string(contentBuf),
			string(reasoningBuf),
			finishReason,
			toolCalls,
			streamError,
		)
		gen.Finish(output, convertUsage(usage), nil)
	}()
	return wrapped, nil
}

func buildLangfuseGenerationOptions(
	ctx context.Context,
	name string,
	modelName string,
	modelID string,
	messages []Message,
	opts *ChatOptions,
	streaming bool,
) langfuse.GenerationOptions {
	options := langfuse.GenerationOptions{
		Name:            name,
		Model:           modelName,
		Input:           buildLangfuseMessages(messages),
		ModelParameters: buildLangfuseModelParams(opts),
		Metadata: map[string]interface{}{
			"model_id":  modelID,
			"streaming": streaming,
			"has_tools": opts != nil && len(opts.Tools) > 0,
		},
	}
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		options.Model = "prepared-knowledge-model"
		options.Input = map[string]interface{}{
			"message_count":     len(messages),
			"scope_hash_prefix": hashPrefix,
		}
		options.ModelParameters = nil
		options.Metadata = map[string]interface{}{
			"streaming":         streaming,
			"has_tools":         opts != nil && len(opts.Tools) > 0,
			"scope_hash_prefix": hashPrefix,
		}
	}
	return options
}

func buildLangfuseTraceOutput(
	ctx context.Context,
	content string,
	reasoningContent string,
	finishReason string,
	toolCalls []types.LLMToolCall,
	streamError bool,
) map[string]interface{} {
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		return map[string]interface{}{
			"content_length":           len([]rune(content)),
			"reasoning_content_length": len([]rune(reasoningContent)),
			"tool_call_count":          len(toolCalls),
			"has_finish_reason":        finishReason != "",
			"stream_error":             streamError,
			"scope_hash_prefix":        hashPrefix,
		}
	}
	return buildLangfuseGenerationOutput(
		content,
		reasoningContent,
		finishReason,
		toolCalls,
	)
}

func buildLangfuseTraceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if _, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		return errors.New("prepared chat generation failed")
	}
	return err
}

func buildLangfuseMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		entry := map[string]interface{}{
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
		if m.ReasoningContent != "" {
			entry["reasoning_content"] = m.ReasoningContent
		}
		out = append(out, entry)
	}
	return out
}

func buildLangfuseGenerationOutput(
	content, reasoningContent, finishReason string,
	toolCalls []types.LLMToolCall,
) map[string]interface{} {
	output := map[string]interface{}{
		"content":       content,
		"tool_calls":    toolCalls,
		"finish_reason": finishReason,
	}
	if reasoningContent != "" {
		output["reasoning_content"] = reasoningContent
	}
	return output
}

func buildLangfuseModelParams(opts *ChatOptions) map[string]interface{} {
	if opts == nil {
		return nil
	}
	params := map[string]interface{}{}
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

// wrapChatLangfuse wraps a Chat in a Langfuse-aware decorator when the
// manager is enabled. Called from NewChat after the debug wrapper so both
// sinks observe the same call.
func wrapChatLangfuse(c Chat, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	if !langfuse.GetManager().Enabled() {
		return c, nil
	}
	return &langfuseChat{inner: c}, nil
}
