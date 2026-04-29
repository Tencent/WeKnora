package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

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
		b.WriteString(fmt.Sprintf("- %s: %s", t.Function.Name, t.Function.Description))
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

// logLLMDebugCall logs a complete non-stream LLM chat call.
func logLLMDebugCall(ctx context.Context, model string, messages []Message, opts *ChatOptions, resp *types.ChatResponse, callErr error, dur time.Duration) {
	if !logger.LLMDebugEnabled() {
		return
	}

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
	logger.LLMDebugLog(ctx, record)
}

// logLLMDebugStream logs a complete stream LLM chat call after all chunks have been received.
func logLLMDebugStream(ctx context.Context, model string, messages []Message, opts *ChatOptions, fullContent string, toolCalls []types.LLMToolCall, usage *types.TokenUsage, callErr error, dur time.Duration) {
	if !logger.LLMDebugEnabled() {
		return
	}

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
	logger.LLMDebugLog(ctx, record)
}

func truncateForDebug(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + fmt.Sprintf("...(%d chars)", len(runes))
}
