package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
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
// When a turn carries a context checkpoint (a compaction summary persisted by
// an earlier run), history starts from it: the summary replaces that turn and
// everything before it, and only later turns are replayed. Every replayed
// message is tagged with its turn's assistant message ID so this run's
// compaction can persist a new checkpoint in turn.
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

	turns := make(map[string]*agentHistoryTurn)
	for _, msg := range rows {
		t, ok := turns[msg.RequestID]
		if !ok {
			t = &agentHistoryTurn{}
			turns[msg.RequestID] = t
		}
		switch msg.Role {
		case "user":
			t.users = append(t.users, msg)
			if t.createdAt.IsZero() || msg.CreatedAt.Before(t.createdAt) {
				t.createdAt = msg.CreatedAt
			}
		case "assistant":
			t.assistant = msg
		}
	}

	completeTurns := make([]*agentHistoryTurn, 0, len(turns))
	for _, t := range turns {
		if len(t.users) > 0 && t.assistant != nil && t.assistant.IsCompleted {
			sort.SliceStable(t.users, func(i, j int) bool {
				return t.users[i].CreatedAt.Before(t.users[j].CreatedAt)
			})
			completeTurns = append(completeTurns, t)
		}
	}

	sort.Slice(completeTurns, func(i, j int) bool {
		return completeTurns[i].createdAt.Before(completeTurns[j].createdAt)
	})

	summary := ""
	if checkpoint := loadContextCheckpoint(ctx, messageRepo, sessionID); checkpoint != nil {
		completeTurns = turnsAfterCheckpoint(completeTurns, checkpoint)
		summary = checkpoint.ContextCheckpoint.Summary
		logger.Infof(ctx, "Agent history resumes from the context checkpoint on %s, %d turn(s) after it",
			checkpoint.ID, len(completeTurns))
	}

	if len(completeTurns) > maxRounds {
		completeTurns = completeTurns[len(completeTurns)-maxRounds:]
	}

	out := make([]chat.Message, 0, len(completeTurns)*4+1)
	if summary != "" {
		out = append(out, compaction.SummaryMessage(summary))
	}
	for _, t := range completeTurns {
		start := len(out)
		out = append(out, buildUserHistoryMessage(t.users[0]))
		out = append(out, buildTurnBodyMessages(t.assistant, t.users[1:])...)
		for i := start; i < len(out); i++ {
			out[i].TurnID = t.assistant.ID
		}
	}
	return out, nil
}

// agentHistoryTurn is one stored turn. A turn is not always one user message.
// Mid-run steering persists every injected message under the running turn's
// request ID, so a turn can be user → tools → user → tools → answer. Keeping
// only the last user row would drop the original question from the next
// turn's context.
type agentHistoryTurn struct {
	users     []*types.Message
	assistant *types.Message
	createdAt time.Time
}

// loadContextCheckpoint returns the session's newest usable checkpoint, or nil.
// A failed lookup degrades to history without one: the turn still runs, it
// just pays for compaction again.
func loadContextCheckpoint(
	ctx context.Context, messageRepo interfaces.MessageRepository, sessionID string,
) *types.Message {
	msg, err := messageRepo.GetLatestContextCheckpoint(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Failed to load the context checkpoint of session %s, "+
			"loading history without it: %v", sessionID, err)
		return nil
	}
	if msg == nil || msg.ContextCheckpoint == nil || strings.TrimSpace(msg.ContextCheckpoint.Summary) == "" {
		return nil
	}
	return msg
}

// turnsAfterCheckpoint drops the turns a checkpoint already covers: its own
// turn and every turn before it. A checkpoint older than the loaded window is
// matched by time instead, which keeps every loaded turn.
func turnsAfterCheckpoint(turns []*agentHistoryTurn, checkpoint *types.Message) []*agentHistoryTurn {
	for i, t := range turns {
		if t.assistant.ID == checkpoint.ID {
			return turns[i+1:]
		}
	}
	after := make([]*agentHistoryTurn, 0, len(turns))
	for _, t := range turns {
		if t.createdAt.After(checkpoint.CreatedAt) {
			after = append(after, t)
		}
	}
	return after
}

// messageCheckpointSink persists the engine's compaction checkpoints onto the
// session's assistant messages, where LoadAgentHistory reads them back.
type messageCheckpointSink struct {
	repo      interfaces.MessageRepository
	sessionID string
}

func (s messageCheckpointSink) SaveContextCheckpoint(
	ctx context.Context, turnMessageID string, checkpoint *types.ContextCheckpoint,
) error {
	return s.repo.UpdateMessageContextCheckpoint(ctx, s.sessionID, turnMessageID, checkpoint)
}

// buildTurnBodyMessages replays one turn's assistant work with any mid-run
// user messages put back where they happened. Steered messages arrive between
// tool rounds, so replaying them all up-front (or dropping them) would tell
// the model a different story than the one it lived through: it would look
// like the user asked for everything before any tool ran.
//
// Steps carry a timestamp; a user row belongs before the first step that
// starts after it. Anything left over lands just before the final answer.
func buildTurnBodyMessages(assistant *types.Message, midRunUsers []*types.Message) []chat.Message {
	if len(midRunUsers) == 0 {
		return buildAssistantHistoryMessages(assistant)
	}

	out := make([]chat.Message, 0, len(assistant.AgentSteps)*2+len(midRunUsers)+1)
	usersByID := make(map[string]*types.Message, len(midRunUsers))
	for _, user := range midRunUsers {
		usersByID[user.ID] = user
	}
	hasBoundaries := false
	for _, step := range assistant.AgentSteps {
		hasBoundaries = hasBoundaries || len(step.UserMessagesBefore) > 0
	}
	appendUser := func(user *types.Message) {
		msg := buildUserHistoryMessage(user)
		msg.Content = types.SteerMessageContent(msg.Content)
		out = append(out, msg)
		delete(usersByID, user.ID)
	}
	next := 0
	for _, step := range assistant.AgentSteps {
		for _, id := range step.UserMessagesBefore {
			if user := usersByID[id]; user != nil {
				appendUser(user)
			}
		}
		for !hasBoundaries && next < len(midRunUsers) &&
			!step.Timestamp.IsZero() &&
			midRunUsers[next].CreatedAt.Before(step.Timestamp) {
			appendUser(midRunUsers[next])
			next++
		}
		out = append(out, buildAgentStepMessages(step)...)
	}
	for _, user := range midRunUsers {
		if usersByID[user.ID] != nil {
			appendUser(user)
		}
	}

	if final := finalAnswerHistoryMessage(assistant); final != nil {
		out = append(out, *final)
	}
	return out
}

// buildUserHistoryMessage converts a stored user message into the chat.Message
// form that should appear in LLM history. It deliberately ignores
// RenderedContent: that field is a snapshot of the old prompt and retrieval
// context format, which must not be mixed into the current request protocol.
// Image captions and attachments are reconstructed from their canonical DB
// columns so useful user-provided context is retained without stale RAG data.
func buildUserHistoryMessage(m *types.Message) chat.Message {
	content := m.Content
	if captions := extractImageCaptionsFromMessage(m.Images); captions != "" {
		content += "\n\n[用户上传图片内容]\n" + captions
	}
	if len(m.Attachments) > 0 {
		content += m.Attachments.BuildPrompt()
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
		msgs = append(msgs, buildAgentStepMessages(step)...)
	}
	if final := finalAnswerHistoryMessage(m); final != nil {
		msgs = append(msgs, *final)
	}
	return msgs
}

// buildAgentStepMessages expands one persisted step into the OpenAI-shaped
// assistant + tool pair. Steps whose only calls were terminal or synthetic
// produce nothing, so callers can treat an empty result as "not a real round".
func buildAgentStepMessages(step types.AgentStep) []chat.Message {
	nonTerminalCalls := filterNonTerminalToolCalls(step.ToolCalls)
	if len(nonTerminalCalls) == 0 {
		if step.IntermediateAnswer && strings.TrimSpace(step.Thought) != "" {
			return []chat.Message{{Role: "assistant", Content: step.Thought, ReasoningContent: step.ReasoningContent}}
		}
		return nil
	}
	assistantMsg := chat.Message{
		Role:             "assistant",
		Content:          step.Thought,
		ReasoningContent: step.ReasoningContent,
		ToolCalls:        make([]chat.ToolCall, 0, len(nonTerminalCalls)),
	}
	for _, tc := range nonTerminalCalls {
		argsJSON, _ := json.Marshal(tc.Args)
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, chat.ToolCall{
			ID:               tc.ID,
			Type:             "function",
			ProviderMetadata: tc.ProviderMetadata,
			Function: chat.FunctionCall{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	msgs := make([]chat.Message, 0, len(nonTerminalCalls)+1)
	msgs = append(msgs, assistantMsg)
	for _, tc := range nonTerminalCalls {
		msgs = append(msgs, chat.Message{
			Role:       "tool",
			Content:    toolCallOutput(tc),
			ToolCallID: tc.ID,
			Name:       tc.Name,
		})
	}
	return msgs
}

// finalAnswerHistoryMessage is the canonical answer of a turn, or nil when the
// turn produced no text (stopped, or answered purely through tools). The
// generated-file markers belong to that turn, so they are relabeled before
// being replayed into a later turn's history.
func finalAnswerHistoryMessage(m *types.Message) *chat.Message {
	finalContent := agentHistoryThinkTagRegex.ReplaceAllString(m.Content, "")
	// Version clarification was written for that message's turn, not this one.
	finalContent = strings.NewReplacer(
		"\n\n本轮生成的文件: ![", "\n\n该历史消息生成的文件: ![",
		"\n\nFile generated this turn: ![", "\n\nFile generated in that historical turn: ![",
	).Replace(finalContent)
	finalContent = strings.TrimSpace(finalContent)
	if finalContent == "" {
		return nil
	}
	return &chat.Message{Role: "assistant", Content: finalContent}
}

// legacyFinalAnswerToolName is the name of the now-removed final_answer tool.
// It is retained here only to filter such calls out of OLD persisted agent
// histories: pre-existing conversations recorded a final_answer tool call as
// the terminal step, and the canonical answer text is replayed via the
// trailing assistant message instead. Re-injecting it would duplicate the
// answer or confuse the model into thinking the previous turn is mid-flight.
const legacyFinalAnswerToolName = "final_answer"

// filterNonTerminalToolCalls drops legacy final_answer entries from historical
// tool calls (see legacyFinalAnswerToolName), plus the pipeline stages a
// fast-answer turn records for its own timeline (see
// types.PipelineToolCallIDPrefix): the model never issued those, so replaying
// them would attribute calls to it that it cannot answer for.
func filterNonTerminalToolCalls(calls []types.ToolCall) []types.ToolCall {
	out := make([]types.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.Name == legacyFinalAnswerToolName || types.IsPipelineToolCallID(tc.ID) {
			continue
		}
		out = append(out, tc)
	}
	return out
}

// toolCallOutput returns the textual content to use for a historical tool
// message. Failures still go through CompactToolOutputForHistory so stdout
// from a crashed skill script is not dropped in favor of a one-line exit code.
func toolCallOutput(tc types.ToolCall) string {
	if tc.Result == nil {
		return ""
	}
	return agenttools.CompactToolOutputForHistory(tc.Name, tc.Result)
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
