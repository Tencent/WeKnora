package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// agentHistoryPageSize is how many rows one backwards page reads. A turn is
// two rows plus one per steered message.
const agentHistoryPageSize = 200

// agentHistoryMaxRows bounds one load however the session is shaped. The token
// budget normally stops the read long before this; a run of turns that never
// completed contributes no tokens and would otherwise page the whole session.
const agentHistoryMaxRows = 5000

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
// Turns lacking either user or assistant content are skipped. Turns are
// returned in chronological order.
//
// History is sized in tokens, not turns. When a turn carries a context
// checkpoint (a compaction summary persisted by an earlier run), history starts
// from it: the summary replaces that turn and everything before it. The newest
// turns after that are replayed while they fit tokenBudget. A turn left out
// is gone (neither replayed nor summarized), so the budget is the whole window
// (agent.HistoryTokenBudget), past the compaction threshold: overflow reaches
// the first round's compaction, which summarizes it into a new checkpoint.
// Compaction, not a turn count, keeps a long session in the window. Every
// replayed message is tagged with its turn's assistant message ID so this
// run's compaction can persist that checkpoint.
//
// Rows are read backwards a page at a time and stop at the checkpoint or once
// the budget is full, so a long session is never read whole. Turns are priced
// as the engine sends them, so retainRetrievalHistory must be the run's
// setting, and in the provider's tokens: estimates are scaled by the newest
// turn's measured ContextTokenScale, which is also returned so the engine's
// first compaction check uses the same scale. Budget and trigger must agree,
// or turns the loader drops can sit below a trigger that never fires.
//
// DB is treated as the single source of truth — there is no Redis/in-memory
// cache layer above this function. Callers are expected to invoke it once
// per turn before handing the messages to the agent engine.
func LoadAgentHistory(
	ctx context.Context,
	messageRepo interfaces.MessageRepository,
	sessionID string,
	tokenBudget int,
	retainRetrievalHistory bool,
) ([]chat.Message, float64, error) {
	if tokenBudget <= 0 {
		return []chat.Message{}, 0, nil
	}
	estimator, err := agenttoken.NewEstimator()
	if err != nil {
		return nil, 0, fmt.Errorf("load agent history: %w", err)
	}

	checkpoint := loadContextCheckpoint(ctx, messageRepo, sessionID)
	out := []chat.Message{}
	if checkpoint != nil {
		out = append(out, compaction.SummaryMessage(checkpoint.ContextCheckpoint.Summary))
	}

	var (
		// rows keeps only what grouping and ordering need; the full rows wait
		// in replay until their turn is replayed, then are released. A page of
		// stored rows can hold whole wiki pages that go out as one line each.
		rows         []*types.Message
		turns        []*agentHistoryTurn
		dropped      bool
		before       time.Time
		beforeID     string
		reachedStart bool
		scale        float64
		used         int
		replay       = newHistoryReplay(estimator, 0, retainRetrievalHistory)
	)
	for len(rows) < agentHistoryMaxRows {
		page, err := messageRepo.ListMessagesBySessionBeforeCursor(
			ctx, sessionID, before, beforeID, agentHistoryPageSize,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("load agent history: %w", err)
		}
		if len(rows) == 0 {
			// Price everything in the provider's tokens, with the scale the
			// engine will start from, or loading and the first compaction
			// check disagree on where the window ends.
			scale = latestContextTokenScale(page)
			estimator.SetScale(scale)
			used = estimator.EstimateMessages(out)
			replay.budget = tokenBudget - used
		}
		for _, msg := range page {
			rows = append(rows, replay.track(msg))
		}
		reachedStart = len(page) < agentHistoryPageSize
		turns, dropped = replay.newestWithin(turnsAfterCheckpoint(completeHistoryTurns(rows, reachedStart), checkpoint))
		if reachedStart || dropped {
			break
		}
		oldest := page[len(page)-1]
		if checkpoint != nil && sortsAtOrBefore(oldest, checkpoint) {
			break
		}
		before, beforeID = oldest.CreatedAt, oldest.ID
	}

	for _, t := range turns {
		out = append(out, replay.messages(t)...)
		used += replay.tokens[t.assistant.ID]
	}
	checkpointID := ""
	if checkpoint != nil {
		checkpointID = checkpoint.ID
	}
	logger.Infof(ctx, "Agent history: %d turn(s), ~%d of %d tokens (scale %.2f), %d row(s) read, "+
		"checkpoint=%q, older turns dropped for the budget=%v",
		len(turns), used, tokenBudget, estimator.Scale(), len(rows), checkpointID, dropped)
	if checkpoint != nil && dropped {
		// Turns between the summary and the kept ones are missing. Normal
		// compaction keeps this from happening; it takes several turns in a
		// row whose compactions could not be persisted.
		logger.Warnf(ctx, "Agent history after checkpoint %s exceeds the budget; "+
			"older turns after it were dropped", checkpointID)
	}
	return out, scale, nil
}

// latestContextTokenScale is the scale the newest calibrated turn among rows
// measured (TokenUsage.ContextTokenScale), or 0 when none did. The first page
// holds the newest turns, so a session that calibrates at all has one there.
func latestContextTokenScale(rows []*types.Message) float64 {
	var newest *types.Message
	for _, msg := range rows {
		if msg.Role != "assistant" || msg.Usage == nil || msg.Usage.ContextTokenScale <= 0 {
			continue
		}
		if newest == nil || sortsAtOrBefore(newest, msg) {
			newest = msg
		}
	}
	if newest == nil {
		return 0
	}
	return newest.Usage.ContextTokenScale
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

// completeHistoryTurns groups rows into completed turns, oldest first. Unless
// the read reached the session's first row, the turn owning the oldest row
// read may be missing its earlier rows — its original question above all — so
// it is left out. When that oldest row is itself a user message the turn is
// whole: steered messages are written after the turn's assistant row, so a
// user row older than it can only be the question, and a steered row with no
// assistant read after it leaves the turn incomplete anyway.
func completeHistoryTurns(rows []*types.Message, reachedStart bool) []*agentHistoryTurn {
	if len(rows) == 0 {
		return nil
	}
	partialRequest, dropPartial := "", false
	if !reachedStart {
		oldest := rows[0]
		for _, msg := range rows[1:] {
			if sortsAtOrBefore(msg, oldest) {
				oldest = msg
			}
		}
		if oldest.Role != "user" {
			partialRequest, dropPartial = oldest.RequestID, true
		}
	}

	turns := make(map[string]*agentHistoryTurn)
	for _, msg := range rows {
		if dropPartial && msg.RequestID == partialRequest {
			continue
		}
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

	complete := make([]*agentHistoryTurn, 0, len(turns))
	for _, t := range turns {
		if len(t.users) > 0 && t.assistant != nil && t.assistant.IsCompleted {
			sort.SliceStable(t.users, func(i, j int) bool {
				return t.users[i].CreatedAt.Before(t.users[j].CreatedAt)
			})
			complete = append(complete, t)
		}
	}
	sort.Slice(complete, func(i, j int) bool {
		return complete[i].createdAt.Before(complete[j].createdAt)
	})
	return complete
}

// sortsAtOrBefore reports whether msg sorts at or before boundary in the
// (created_at, id) order the backwards read pages in.
func sortsAtOrBefore(msg, boundary *types.Message) bool {
	return msg.CreatedAt.Before(boundary.CreatedAt) ||
		(msg.CreatedAt.Equal(boundary.CreatedAt) && msg.ID <= boundary.ID)
}

// historyReplay builds and prices each turn's replayed messages once, however
// many pages the read takes.
type historyReplay struct {
	estimator              *agenttoken.Estimator
	budget                 int
	retainRetrievalHistory bool
	built                  map[string][]chat.Message
	tokens                 map[string]int
	// full holds stored rows by ID until their turn is replayed.
	full map[string]*types.Message
}

func newHistoryReplay(
	estimator *agenttoken.Estimator, budget int, retainRetrievalHistory bool,
) *historyReplay {
	return &historyReplay{
		estimator:              estimator,
		budget:                 budget,
		retainRetrievalHistory: retainRetrievalHistory,
		built:                  make(map[string][]chat.Message),
		tokens:                 make(map[string]int),
		full:                   make(map[string]*types.Message),
	}
}

// track holds a stored row until its turn is replayed and returns the slim
// copy grouping works on.
func (r *historyReplay) track(msg *types.Message) *types.Message {
	r.full[msg.ID] = msg
	return &types.Message{
		ID:          msg.ID,
		RequestID:   msg.RequestID,
		Role:        msg.Role,
		CreatedAt:   msg.CreatedAt,
		IsCompleted: msg.IsCompleted,
	}
}

// messages replays one turn as the engine will send it (agent.HistoryAsSent),
// tagged with its assistant message ID, and releases the stored rows it was
// built from. Replaying what is sent, not what is stored, keeps pricing and
// loading on the same footing: a wiki page stored in full but sent as one line
// costs one line and is held in memory as one line.
func (r *historyReplay) messages(t *agentHistoryTurn) []chat.Message {
	id := t.assistant.ID
	if msgs, ok := r.built[id]; ok {
		return msgs
	}
	users := make([]*types.Message, len(t.users))
	for i, u := range t.users {
		users[i] = r.stored(u)
	}
	msgs := append([]chat.Message{buildUserHistoryMessage(users[0])},
		buildTurnBodyMessages(r.stored(t.assistant), users[1:])...)
	for i := range msgs {
		msgs[i].TurnID = id
	}
	sent := agent.HistoryAsSent(msgs, r.retainRetrievalHistory)
	tokens := 0
	for i := range sent {
		tokens += r.estimator.EstimateMessage(&sent[i])
	}
	r.built[id] = sent
	r.tokens[id] = tokens
	for _, u := range t.users {
		delete(r.full, u.ID)
	}
	delete(r.full, t.assistant.ID)
	return sent
}

// stored returns the full row behind msg, or msg itself when it is not a
// tracked slim copy (callers that group full rows directly).
func (r *historyReplay) stored(msg *types.Message) *types.Message {
	if full, ok := r.full[msg.ID]; ok {
		return full
	}
	return msg
}

// newestWithin keeps the newest turns that fit the budget and reports whether
// an older one had to be left out. It stops at the first turn that does not
// fit, so what is kept stays contiguous. The newest turn is kept regardless:
// it is what the next message most likely refers to, and compaction can split
// a turn too large to fit on its own.
func (r *historyReplay) newestWithin(turns []*agentHistoryTurn) ([]*agentHistoryTurn, bool) {
	used := 0
	for i := len(turns) - 1; i >= 0; i-- {
		r.messages(turns[i])
		cost := r.tokens[turns[i].assistant.ID]
		if used+cost > r.budget && i < len(turns)-1 {
			return turns[i+1:], true
		}
		used += cost
	}
	return turns, false
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
// turn and every turn before it. A nil checkpoint covers nothing.
//
// When the checkpoint's own turn was not read whole (the budget filled first,
// or the read ended on it), a turn is kept when its assistant row sorts after
// the checkpoint's in the (created_at, id) order the read pages in. Comparing
// assistant row with assistant row keeps a turn whose question shares the
// checkpoint's timestamp; a turn's first user time against the checkpoint's
// assistant time dropped it.
func turnsAfterCheckpoint(turns []*agentHistoryTurn, checkpoint *types.Message) []*agentHistoryTurn {
	if checkpoint == nil {
		return turns
	}
	for i, t := range turns {
		if t.assistant.ID == checkpoint.ID {
			return turns[i+1:]
		}
	}
	after := make([]*agentHistoryTurn, 0, len(turns))
	for _, t := range turns {
		if !sortsAtOrBefore(t.assistant, checkpoint) {
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
