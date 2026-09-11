package invoke

// llm_debug.go restores the v1 llm_debug落盘 (chat/llm_debug.go +
// chat/llm_debug_wrapper.go) at the entry: when LLM debug logging is enabled,
// every Chat/ChatStream call writes one record in the exact v1 format (same
// logger.LLMCallRecord, same section layout), so existing tooling and grep
// habits keep working. The v1 decorator-wrapper position is architecturally
// the entry itself in v2 — logging here covers every caller automatically.
// Disabled deployments pay one LLMDebugEnabled() check per call.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// buildLLMMessages maps the neutral message onto the logger view. Image parts
// become `[image_url: …]` content markers with the URL truncated (v1
// MultiContent semantics); a full data URI would otherwise flood the log.
func buildLLMMessages(messages []Message) []logger.LLMMessage {
	out := make([]logger.LLMMessage, 0, len(messages))
	for i := range messages {
		m := &messages[i]
		texts := make([]string, 0, len(m.Content))
		var imageMarkers []string
		for _, p := range m.Content {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
			if p.Image != nil {
				imageMarkers = append(imageMarkers, "[image_url: "+truncateForDebug(p.Image.URL, 120)+"]")
			}
		}
		content := strings.Join(texts, "\n")
		if len(imageMarkers) > 0 {
			content += "\n" + strings.Join(imageMarkers, "\n")
		}
		lm := logger.LLMMessage{
			Role:       string(m.Role),
			Content:    content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
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

// buildOptionsSection renders the call options. ThinkingLevel is a v2
// addition (the five-tier winner is exactly what a thinking debug needs).
func buildOptionsSection(opts *ChatOptions) string {
	if opts == nil {
		return ""
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("Temperature=%.2f", opts.Temperature))
	if opts.TopP > 0 {
		parts = append(parts, fmt.Sprintf("TopP=%.2f", opts.TopP))
	}
	if opts.MaxCompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("CompletionBudget=%d", opts.MaxCompletionTokens))
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
	if opts.ThinkingLevel != "" {
		parts = append(parts, "ThinkingLevel="+opts.ThinkingLevel)
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
		fmt.Fprintf(&b, "- %s: %s", t.Name, t.Description)
	}
	return b.String()
}

// responseToolCallInfos maps response tool calls onto the logger view (v1
// buildResponseToolCalls).
func responseToolCallInfos(tcs []types.LLMToolCall) []logger.LLMToolCallInfo {
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
	return fmt.Sprintf(
		"Prompt: %d, Completion: %d, Total: %d, CacheRead: %d, CacheWrite: %d, CacheMiss: %d, CacheStatus: %s",
		u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CacheReadTokens,
		u.CacheWriteTokens, u.CacheMissTokens, u.CacheStatus)
}

// logLLMDebugCall logs one completed non-stream chat call.
func logLLMDebugCall(
	ctx context.Context, model string, messages []Message, opts *ChatOptions,
	resp *ChatResponse, callErr error, dur time.Duration,
) {
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
		if tcs := responseToolCallInfos(toLLMToolCalls(resp.ToolCalls)); len(tcs) > 0 {
			respText.WriteString(logger.FormatToolCalls(tcs))
		}
		if respText.Len() > 0 {
			section := logger.RecordSection{Title: "Response", Content: respText.String()}
			record.Sections = append(record.Sections, section)
		}
		if u := resp.Usage.usageToTypes(); u != nil {
			record.Sections = append(record.Sections, logger.RecordSection{Title: "Usage", Content: usageString(*u)})
		}
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}

// logLLMDebugStream logs one completed stream chat call after all chunks have
// been observed (v1 wrapper semantics: accumulate answer text / tool calls /
// usage / error across emitted chunks, write one record at stream end).
func logLLMDebugStream(
	ctx context.Context, model string, messages []Message, opts *ChatOptions,
	fullContent string, toolCalls []types.LLMToolCall, usage *types.TokenUsage,
	callErr error, dur time.Duration,
) {
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
	if len(toolCalls) > 0 {
		respText.WriteString(logger.FormatToolCalls(responseToolCallInfos(toolCalls)))
	}
	if respText.Len() > 0 {
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Response", Content: respText.String()})
	}

	if usage != nil {
		section := logger.RecordSection{Title: "Usage", Content: usageString(*usage)}
		record.Sections = append(record.Sections, section)
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

// logEmbeddingDebug ports the v1 embedding debug record (embedding/llm_debug.go
// logEmbeddingDebug) byte-for-byte: CallType "Embedding", an Input section with
// per-text length + newline-escaped previews, an Output section with per-vector
// dims + first-3 values.
func logEmbeddingDebug(
	ctx context.Context, model string,
	opts *EmbeddingOptions, resp *EmbeddingResponse,
	callErr error, dur time.Duration,
) {
	if !logger.LLMDebugEnabled() {
		return
	}

	record := &logger.LLMCallRecord{
		CallType: "Embedding",
		Model:    model,
		Duration: dur,
	}

	var inputs []string
	if opts != nil {
		inputs = opts.Inputs
	}
	// Input section: show each text with a preview
	var inputBuf strings.Builder
	fmt.Fprintf(&inputBuf, "count=%d\n", len(inputs))
	for i, t := range inputs {
		preview := strings.ReplaceAll(t, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		fmt.Fprintf(&inputBuf, "[%d] (len=%d) %s\n", i, len([]rune(t)), preview)
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Input", Content: inputBuf.String()})

	// Output section
	if resp != nil && resp.Vectors != nil {
		var outBuf strings.Builder
		fmt.Fprintf(&outBuf, "count=%d\n", len(resp.Vectors))
		for i, vec := range resp.Vectors {
			if len(vec) > 0 {
				fmt.Fprintf(&outBuf, "[%d] dims=%d, first_3=[%.6f, %.6f, %.6f]\n", i, len(vec),
					safeVecIdx(vec, 0), safeVecIdx(vec, 1), safeVecIdx(vec, 2))
			} else {
				fmt.Fprintf(&outBuf, "[%d] empty\n", i)
			}
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Output", Content: outBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}

func safeVecIdx(v []float32, i int) float32 {
	if i < len(v) {
		return v[i]
	}
	return 0
}

// logRerankDebug ports the v1 rerank debug record (rerank/llm_debug.go)
// byte-for-byte: CallType "Rerank", Query / Documents / Results sections.
// The v1 Results section previewed the response-echoed document text; v2
// responses carry (index, score) only, so the preview re-derives the same
// text from the input documents by index (vendors echo the input back).
func logRerankDebug(
	ctx context.Context, model string,
	opts *RerankOptions, resp *RerankResponse,
	callErr error, dur time.Duration,
) {
	if !logger.LLMDebugEnabled() {
		return
	}

	record := &logger.LLMCallRecord{
		CallType: "Rerank",
		Model:    model,
		Duration: dur,
	}

	record.Sections = append(record.Sections, logger.RecordSection{
		Title:   "Query",
		Content: opts.Query,
	})

	var docBuf strings.Builder
	fmt.Fprintf(&docBuf, "count=%d\n", len(opts.Documents))
	for i, doc := range opts.Documents {
		preview := strings.ReplaceAll(doc, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		fmt.Fprintf(&docBuf, "[%d] (len=%d) %s\n", i, len([]rune(doc)), preview)
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Documents", Content: docBuf.String()})

	if resp != nil {
		var resBuf strings.Builder
		fmt.Fprintf(&resBuf, "count=%d\n", len(resp.Results))
		for _, r := range resp.Results {
			docText := ""
			if r.Index >= 0 && r.Index < len(opts.Documents) {
				docText = opts.Documents[r.Index]
			}
			docPreview := strings.ReplaceAll(docText, "\n", "\\n")
			docPreview = logger.TruncateRunes(docPreview, 200)
			fmt.Fprintf(&resBuf, "  [%d] score=%.6f  %s\n", r.Index, r.Score, docPreview)
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Results", Content: resBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}
