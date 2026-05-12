package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// agentHistoryFetchMultiplier controls how many raw DB messages to fetch
// when assembling history. Each turn contributes ~2 rows (user + assistant);
// we ask for a generous multiple so we never under-fetch when some pairs are
// incomplete (e.g. an in-flight turn).
const agentHistoryFetchMultiplier = 4

// agentHistoryFetchMin is the floor for the DB fetch limit, used when
// maxRounds is small or unset.
const agentHistoryFetchMin = 50

var agentHistoryThinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// LoadAgentHistory rebuilds the multi-turn LLM context for an Agent-mode
// session directly from the persistent messages table. The result is a
// chronologically ordered list of chat.Message entries suitable for prepending
// to the current turn (without system prompt; the engine adds that itself).
//
// For each historical turn it emits:
//  1. A user message (RenderedContent if present, else Content, plus any
//     image captions appended).
//  2. For each AgentStep with non-terminal tool calls (i.e. excluding
//     final_answer), an assistant message carrying the step's thought and
//     tool_calls, followed by one tool message per tool result.
//  3. A final assistant message with the canonical answer (msg.Content with
//     <think> blocks stripped).
//
// Turns lacking either user or assistant content are skipped. The newest
// maxRounds turns are returned in chronological order.
//
// DB is treated as the single source of truth — there is no Redis/in-memory
// cache layer above this function. Callers are expected to invoke it once
// per turn before handing the messages to the agent engine.
func LoadAgentHistory(
	ctx context.Context,
	messageRepo interfaces.MessageRepository,
	sessionID string,
	maxRounds int,
) ([]chat.Message, error) {
	if maxRounds <= 0 {
		return []chat.Message{}, nil
	}

	fetchLimit := maxRounds * agentHistoryFetchMultiplier
	if fetchLimit < agentHistoryFetchMin {
		fetchLimit = agentHistoryFetchMin
	}

	rows, err := messageRepo.GetRecentMessagesBySession(ctx, sessionID, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("load agent history: %w", err)
	}
	if len(rows) == 0 {
		return []chat.Message{}, nil
	}

	type pair struct {
		user      *types.Message
		assistant *types.Message
		createdAt time.Time
	}
	pairs := make(map[string]*pair)
	for _, msg := range rows {
		p, ok := pairs[msg.RequestID]
		if !ok {
			p = &pair{}
			pairs[msg.RequestID] = p
		}
		switch msg.Role {
		case "user":
			p.user = msg
			if p.createdAt.IsZero() || msg.CreatedAt.Before(p.createdAt) {
				p.createdAt = msg.CreatedAt
			}
		case "assistant":
			p.assistant = msg
		}
	}

	completePairs := make([]*pair, 0, len(pairs))
	for _, p := range pairs {
		if p.user != nil && p.assistant != nil && p.assistant.IsCompleted {
			completePairs = append(completePairs, p)
		}
	}

	sort.Slice(completePairs, func(i, j int) bool {
		return completePairs[i].createdAt.Before(completePairs[j].createdAt)
	})

	if len(completePairs) > maxRounds {
		completePairs = completePairs[len(completePairs)-maxRounds:]
	}

	out := make([]chat.Message, 0, len(completePairs)*4)
	for _, p := range completePairs {
		out = append(out, buildUserHistoryMessage(p.user))
		out = append(out, buildAssistantHistoryMessages(p.assistant)...)
	}
	return out, nil
}

// buildUserHistoryMessage converts a stored user message into the chat.Message
// form that should appear in LLM history. It prefers RenderedContent (which
// already contains any retrieval-context augmentation from prior turns) and
// falls back to Content. When the user uploaded images, the captions extracted
// during ingestion are appended so subsequent turns retain a textual reference
// to those images even after the binary payload is gone.
func buildUserHistoryMessage(m *types.Message) chat.Message {
	content := m.RenderedContent
	if content == "" {
		content = m.Content
	}
	if captions := extractImageCaptionsFromMessage(m.Images); captions != "" && m.RenderedContent == "" {
		content += "\n\n[用户上传图片内容]\n" + captions
	}
	return chat.Message{Role: "user", Content: content}
}

// buildAssistantHistoryMessages reconstructs the assistant side of one
// historical turn. It walks AgentSteps to expand intermediate tool calls into
// proper OpenAI-shaped assistant + tool messages, then emits the canonical
// final answer as a trailing assistant message.
//
// AgentSteps from KnowledgeQA-mode turns are empty, in which case the result
// is just the single final-answer assistant message — exactly mirroring how
// the KnowledgeQA pipeline replays history today.
func buildAssistantHistoryMessages(m *types.Message) []chat.Message {
	msgs := make([]chat.Message, 0, len(m.AgentSteps)*2+1)
	for _, step := range m.AgentSteps {
		nonTerminalCalls := filterNonTerminalToolCalls(step.ToolCalls)
		if len(nonTerminalCalls) == 0 {
			continue
		}
		assistantMsg := chat.Message{
			Role:      "assistant",
			Content:   step.Thought,
			ToolCalls: make([]chat.ToolCall, 0, len(nonTerminalCalls)),
		}
		for _, tc := range nonTerminalCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, chat.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: chat.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argsJSON),
				},
			})
		}
		msgs = append(msgs, assistantMsg)
		for _, tc := range nonTerminalCalls {
			msgs = append(msgs, chat.Message{
				Role:       "tool",
				Content:    toolCallOutput(tc),
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
		}
	}

	finalContent := agentHistoryThinkTagRegex.ReplaceAllString(m.Content, "")
	finalContent = strings.TrimSpace(finalContent)
	if finalContent != "" {
		msgs = append(msgs, chat.Message{Role: "assistant", Content: finalContent})
	}
	return msgs
}

// filterNonTerminalToolCalls drops final_answer entries since those are
// terminal signals — the canonical answer text is replayed via the trailing
// assistant message instead, so re-injecting final_answer here would either
// duplicate the answer or confuse the model into thinking the previous turn
// is still mid-flight.
func filterNonTerminalToolCalls(calls []types.ToolCall) []types.ToolCall {
	out := make([]types.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.Name == agenttools.ToolFinalAnswer {
			continue
		}
		out = append(out, tc)
	}
	return out
}

// toolCallOutput returns the textual content to use for a historical tool
// message: the recorded Output on success, or an "Error: …" line otherwise so
// the model can tell that an earlier tool call failed.
func toolCallOutput(tc types.ToolCall) string {
	if tc.Result == nil {
		return ""
	}
	if !tc.Result.Success {
		if tc.Result.Error != "" {
			return "Error: " + tc.Result.Error
		}
		return "Error: tool call failed"
	}
	return tc.Result.Output
}

// extractImageCaptionsFromMessage concatenates non-empty Caption fields from
// stored message images. Mirrors the helper used in chat_pipeline so both
// modes surface previous-turn image descriptions identically.
func extractImageCaptionsFromMessage(images types.MessageImages) string {
	var parts []string
	for _, img := range images {
		if img.Caption != "" {
			parts = append(parts, img.Caption)
		}
	}
	return strings.Join(parts, "\n")
}
