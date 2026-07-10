package chat

import (
	"encoding/json"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

func estimateChatRequestTokens(modelName string, messages []Message, opts *ChatOptions) int {
	codec, err := tokenizer.ForModel(tokenizer.Model(strings.ToLower(strings.TrimSpace(modelName))))
	if err != nil {
		codec, _ = tokenizer.Get(tokenizer.Cl100kBase)
	}
	count := func(s string) int {
		if s == "" || codec == nil {
			return 0
		}
		ids, _, encodeErr := codec.Encode(s)
		if encodeErr != nil {
			return (len([]rune(s)) + 2) / 3
		}
		return len(ids)
	}
	total := 3
	for _, msg := range messages {
		total += 3 + count(msg.Role) + count(msg.Content) + count(msg.Name) + count(msg.ToolCallID) + count(msg.ReasoningContent)
		for _, part := range msg.MultiContent {
			total += count(part.Type) + count(part.Text)
			if part.ImageURL != nil {
				total += imageRequestCost(part.ImageURL.Detail)
			}
		}
		total += len(msg.Images) * 256
		for _, call := range msg.ToolCalls {
			total += 6 + count(call.ID) + count(call.Type) + count(call.Function.Name) + count(call.Function.Arguments)
			if b, marshalErr := json.Marshal(call.ProviderMetadata); marshalErr == nil {
				total += count(string(b))
			}
		}
	}
	reserved := 4096
	if opts != nil {
		if b, marshalErr := json.Marshal(opts.Tools); marshalErr == nil && len(opts.Tools) > 0 {
			total += count(string(b)) + 8
		}
		total += count(opts.ToolChoice)
		if len(opts.Format) > 0 {
			total += count(string(opts.Format)) + 4
		}
		if opts.MaxCompletionTokens > 0 {
			reserved = opts.MaxCompletionTokens
		} else if opts.MaxTokens > 0 {
			reserved = opts.MaxTokens
		}
	}
	return total + reserved
}

func imageRequestCost(detail string) int {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "low":
		return 85
	case "high":
		return 1024
	default:
		return 256
	}
}
