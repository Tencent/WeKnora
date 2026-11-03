package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

// RunSnapshotVersion is the format version written into RunSnapshot.
const RunSnapshotVersion = 1

// CollaborationDelivery keeps mailbox input at its original round boundary.
// It is model context, not a user-visible execution step.
type CollaborationDelivery struct {
	BeforeStep int `json:"before_step"`
	// BeforeUsers distinguishes finishing-boundary events from the next poll,
	// which happens after that round's queued user messages have been delivered.
	BeforeUsers bool                  `json:"before_users,omitempty"`
	Events      []collaboration.Event `json:"events"`
}

// RunSnapshot is everything needed to resume a run that ended waiting on a
// user prompt. It holds durable values only: request-local model handles
// (chunk://N, doc://N, res://, msN, mtN) do not survive the run, so the user turn is kept
// as the raw query and re-rendered on resume, and tool arguments are kept in
// their decoded form.
type RunSnapshot struct {
	CollaborationDeliveries []CollaborationDelivery `json:"collaboration_deliveries,omitempty"`
	CollaborationCursor     *int64                  `json:"collaboration_cursor,omitempty"`
	MCPToolRefs             []string                `json:"mcp_tool_refs,omitempty"`
	Version                 int                     `json:"version"`
	CollaborationWait       bool                    `json:"collaboration_wait,omitempty"`
	BetweenRounds           bool                    `json:"between_rounds,omitempty"`
	// Request is the caller's types.ResumableAgentRequest.
	Request   json.RawMessage `json:"request,omitempty"`
	Query     string          `json:"query"`
	ImageURLs []string        `json:"image_urls,omitempty"`
	// UserMessages holds the raw text referenced by UserMessagesBefore.
	UserMessages map[string]string `json:"user_messages,omitempty"`

	CompletedSteps []types.AgentStep     `json:"completed_steps"`
	KnowledgeRefs  []*types.SearchResult `json:"knowledge_refs,omitempty"`
	CurrentRound   int                   `json:"current_round"`
	TurnUsage      types.TokenUsage      `json:"turn_usage"`

	PendingRound PendingRound `json:"pending_round"`
}

// PendingRound is the round interrupted by the prompt.
type PendingRound struct {
	// Step carries the round's thought and reasoning; its ToolCalls are the
	// calls that finished before the blocked one.
	Step  types.AgentStep `json:"step"`
	Calls []PendingCall   `json:"calls"`
	// BlockedIndex is the call waiting on the prompt. Calls after it have
	// not run.
	BlockedIndex int `json:"blocked_index"`
}

// PendingCall is one tool call of the interrupted round.
type PendingCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments is the decoded JSON the call executes with.
	Arguments         string                 `json:"arguments"`
	UnresolvedHandles []string               `json:"unresolved_handles,omitempty"`
	ProviderMetadata  types.ToolCallMetadata `json:"provider_metadata,omitempty"`
}

// DecodeRunSnapshot parses a saved snapshot, rejecting unknown versions.
func DecodeRunSnapshot(data []byte) (*RunSnapshot, error) {
	var snap RunSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode run snapshot: %w", err)
	}
	if snap.Version != RunSnapshotVersion {
		return nil, fmt.Errorf("unsupported run snapshot version %d", snap.Version)
	}
	return &snap, nil
}

// DisplaySteps returns the steps a suspended message shows: the completed
// rounds plus whatever the interrupted round produced so far.
func (s *RunSnapshot) DisplaySteps() []types.AgentStep {
	steps := append([]types.AgentStep(nil), s.CompletedSteps...)
	if p := s.PendingRound.Step; p.Thought != "" || len(p.ToolCalls) > 0 ||
		p.ReasoningContent != "" || len(p.UserMessagesBefore) > 0 {
		steps = append(steps, p)
	}
	return steps
}

// ClosedSteps returns the steps of a run that will never resume: the
// interrupted round's unfinished calls are recorded as failed with reason, so
// the transcript keeps one result per tool call the model issued.
func (s *RunSnapshot) ClosedSteps(reason string) []types.AgentStep {
	steps := append([]types.AgentStep(nil), s.CompletedSteps...)
	p := s.PendingRound
	step := p.Step
	step.ToolCalls = append([]types.ToolCall(nil), p.Step.ToolCalls...)
	for i := len(step.ToolCalls); i < len(p.Calls); i++ {
		c := p.Calls[i]
		var args map[string]any
		if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
			args = map[string]any{"_raw": c.Arguments}
		}
		step.ToolCalls = append(step.ToolCalls, types.ToolCall{
			ID:               c.ID,
			Name:             c.Name,
			Args:             args,
			ProviderMetadata: c.ProviderMetadata,
			Result:           &types.ToolResult{Success: false, Error: reason},
		})
	}
	return append(steps, step)
}

// turnProgress tracks the turn so it can be captured mid-round. Tool calls
// finish on errgroup goroutines, hence the lock.
type turnProgress struct {
	mu                      sync.Mutex
	query                   string
	imageURLs               []string
	state                   *types.AgentState
	userMessages            map[string]string
	collaborationDeliveries []CollaborationDelivery
	// round is the round executing tools, nil between rounds.
	round *roundProgress
}

type roundProgress struct {
	step  types.AgentStep
	calls []types.LLMToolCall
	done  map[int]types.ToolCall
}

// EnableSuspension lets prompts raised by this run suspend it instead of
// blocking. request is the caller's types.ResumableAgentRequest, saved with
// the run so another replica can rebuild it. Must be called before Execute.
func (e *AgentEngine) EnableSuspension(request json.RawMessage) {
	e.suspendRequest = request
	e.suspendable = true
}

// SetRunMode says whether the run's client can render and answer prompts
// such as ask_user, whether or not the run may also suspend.
func (e *AgentEngine) SetRunMode(mode types.RunMode) {
	e.mode = mode.Normalize()
}

func (e *AgentEngine) beginTurn(query string, imageURLs []string, state *types.AgentState) {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	e.progress.query, e.progress.imageURLs, e.progress.state = query, imageURLs, state
	e.progress.userMessages = make(map[string]string)
	e.progress.collaborationDeliveries = nil
}

func (e *AgentEngine) recordSteerMessage(id, content string) {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	if e.progress.userMessages == nil {
		e.progress.userMessages = make(map[string]string)
	}
	e.progress.userMessages[id] = content
}

func (e *AgentEngine) beginRound(step types.AgentStep, calls []types.LLMToolCall) {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	e.progress.round = &roundProgress{step: step, calls: calls, done: make(map[int]types.ToolCall)}
}

func (e *AgentEngine) endRound() {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	e.progress.round = nil
}

func (e *AgentEngine) recordCall(i int, call types.ToolCall) {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	if e.progress.round != nil {
		e.progress.round.done[i] = call
	}
}

// RestrictSuspensionToCollaboration keeps durable collaboration waits while
// prompts raised by ordinary tools block in-process, as the operator configured.
func (e *AgentEngine) RestrictSuspensionToCollaboration() {
	e.collaborationOnlySuspension = true
}

// snapshotFunc returns the ToolExecContext.Snapshot hook for call i, or nil
// when this call cannot suspend the run.
func (e *AgentEngine) snapshotFunc(i int, toolName string) func() ([]byte, int, error) {
	if !e.suspendable || (e.collaborationOnlySuspension && toolName != "wait_agent") {
		return nil
	}
	return func() ([]byte, int, error) {
		snap, err := e.snapshot(i)
		if err != nil {
			return nil, 0, err
		}
		data, err := json.Marshal(snap)
		if err != nil {
			return nil, 0, fmt.Errorf("encode run snapshot: %w", err)
		}
		return stripJSONNUL(data), RunSnapshotVersion, nil
	}
}

// stripJSONNUL drops \u0000 escapes from encoded JSON; PostgreSQL rejects
// them in jsonb, and tool output (documents, web pages) can carry them.
// Escape pairs are copied whole so an escaped backslash followed by the text
// "u0000" is left alone.
func stripJSONNUL(data []byte) []byte {
	const nul = `\u0000`
	if !bytes.Contains(data, []byte(nul)) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' || i+1 >= len(data) {
			out = append(out, data[i])
			continue
		}
		if bytes.HasPrefix(data[i:], []byte(nul)) {
			i += len(nul) - 1
			continue
		}
		out = append(out, data[i], data[i+1])
		i++
	}
	return out
}

// snapshot captures the run as it stands while call blocked waits.
func (e *AgentEngine) snapshot(blocked int) (*RunSnapshot, error) {
	e.progress.mu.Lock()
	defer e.progress.mu.Unlock()
	p := &e.progress
	if p.state == nil || p.round == nil {
		return nil, errors.New("no round in progress")
	}
	r := p.round
	if blocked < 0 || blocked >= len(r.calls) {
		return nil, fmt.Errorf("blocked call %d outside round of %d calls", blocked, len(r.calls))
	}
	step := r.step
	step.ToolCalls = nil
	calls := make([]PendingCall, len(r.calls))
	for i, tc := range r.calls {
		calls[i] = PendingCall{
			ID:                agenttools.NormalizeToolCallID(tc.ID, tc.Function.Name, i),
			Name:              tc.Function.Name,
			Arguments:         tc.Function.Arguments,
			UnresolvedHandles: tc.UnresolvedHandles,
			ProviderMetadata:  tc.ProviderMetadata,
		}
		if i < blocked {
			done, ok := r.done[i]
			if !ok {
				return nil, fmt.Errorf("call %d before the blocked call has not finished", i)
			}
			step.ToolCalls = append(step.ToolCalls, done)
		}
	}
	return &RunSnapshot{
		CollaborationDeliveries: append([]CollaborationDelivery(nil), p.collaborationDeliveries...),
		CollaborationCursor:     e.collaborationCursor(),
		Version:                 RunSnapshotVersion,
		MCPToolRefs:             e.toolRegistry.MCPExposureSnapshot(),
		Request:                 e.suspendRequest,
		Query:                   p.query,
		ImageURLs:               p.imageURLs,
		UserMessages:            maps.Clone(p.userMessages),
		CompletedSteps:          append([]types.AgentStep(nil), p.state.RoundSteps...),
		KnowledgeRefs:           append([]*types.SearchResult(nil), p.state.KnowledgeRefs...),
		CurrentRound:            p.state.CurrentRound,
		TurnUsage:               p.state.TurnUsage,
		PendingRound:            PendingRound{Step: step, Calls: calls, BlockedIndex: blocked},
	}, nil
}

// suspensionOf reports whether a finished call is in fact waiting on a prompt.
func suspensionOf(call types.ToolCall) *types.PromptSuspension {
	if call.Result == nil {
		return nil
	}
	return call.Result.Suspend
}
