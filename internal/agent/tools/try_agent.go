package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// ToolTryAgent asks an agent one question the way a user would, so the
// assistant can check the setup it just made.
const ToolTryAgent = "try_agent"

const (
	maxTrialsPerRun        = 3
	maxTrialQuestionRunes  = 2000
	maxTrialAnswerRunes    = 4000
	maxTrialToolCalls      = 30
	maxTrialToolErrorRunes = 300
	maxTrialReferences     = 10
)

// AgentTrial is what one trial run produced.
type AgentTrial struct {
	AgentName       string                `json:"agent_name"`
	AgentMode       string                `json:"agent_mode"`
	Answer          string                `json:"answer"`
	AnswerTruncated bool                  `json:"answer_truncated,omitempty"`
	ToolCalls       []AgentTrialToolCall  `json:"tool_calls,omitempty"`
	References      []AgentTrialReference `json:"references,omitempty"`
	Error           string                `json:"error,omitempty"`
	DurationMs      int64                 `json:"duration_ms"`
}

// AgentTrialToolCall is one tool the tried agent called.
type AgentTrialToolCall struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// AgentTrialReference is one document the tried agent's answer drew on.
type AgentTrialReference struct {
	Title      string  `json:"title"`
	ChunkIndex int     `json:"chunk_index"`
	Score      float64 `json:"score"`
}

// AgentTrialRunner runs trials as the signed-in user, in a session that no
// conversation list shows and that is removed afterwards.
type AgentTrialRunner interface {
	// AgentName resolves the agent to try, refusing agents that cannot be.
	AgentName(ctx context.Context, agentID string) (string, error)
	TryAgent(ctx context.Context, agentID, question string) (*AgentTrial, error)
}

// TryAgentTool runs one question against an agent. A tool is built per run,
// so its trial count is per conversation turn.
type TryAgentTool struct {
	BaseTool
	runner AgentTrialRunner
	trials atomic.Int32
}

// NewTryAgentTool creates try_agent over runner.
func NewTryAgentTool(runner AgentTrialRunner) *TryAgentTool {
	return &TryAgentTool{
		BaseTool: NewBaseTool(ToolTryAgent, tryAgentDescription, json.RawMessage(tryAgentSchema)),
		runner:   runner,
	}
}

const tryAgentDescription = "" +
	"Ask an agent one question the way a user would, and see its answer, the tools it called " +
	"and the documents it cited. Use it after creating or changing an agent to check that it " +
	"finds what it should, before telling the user it is ready. " +
	"The agent runs with its saved configuration, and its own tools run for real: an agent that " +
	"can write to a Wiki may write during the trial. Tools that would ask the user for " +
	"confirmation are refused instead. The trial runs in a separate conversation that the user " +
	"never sees, removed afterwards, and it costs model calls like any chat. " +
	"The assistant itself and agents that manage WeKnora cannot be tried. " +
	"At most 3 trials per turn: pick questions whose answers you can judge from the knowledge you know is there."

const tryAgentSchema = `{
  "type": "object",
  "properties": {
    "agent_id": {
      "type": "string",
      "description": "ID of the agent to try, as returned by the agent operations."
    },
    "question": {
      "type": "string",
      "description": "The question to ask, as a user would phrase it."
    }
  },
  "required": ["agent_id", "question"],
  "additionalProperties": false
}`

type tryAgentInput struct {
	AgentID  string `json:"agent_id"`
	Question string `json:"question"`
}

func parseTryAgentInput(args json.RawMessage) (tryAgentInput, error) {
	var input tryAgentInput
	if err := json.Unmarshal(args, &input); err != nil {
		return input, fmt.Errorf("arguments must be {\"agent_id\": string, \"question\": string}: %v", err)
	}
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.Question = strings.TrimSpace(input.Question)
	if input.AgentID == "" {
		return input, errors.New("agent_id is required")
	}
	if input.Question == "" {
		return input, errors.New("question is required")
	}
	if utf8.RuneCountInString(input.Question) > maxTrialQuestionRunes {
		return input, fmt.Errorf("question must be at most %d characters", maxTrialQuestionRunes)
	}
	return input, nil
}

// Execute runs the trial and reports what the agent did.
func (t *TryAgentTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	input, err := parseTryAgentInput(args)
	if err != nil {
		return operationFailure(err.Error()), nil
	}
	if t.trials.Add(1) > maxTrialsPerRun {
		return operationFailure(fmt.Sprintf("the agent was not tried: this conversation turn already used its "+
			"%d trials. Report what the earlier trials showed instead.", maxTrialsPerRun)), nil
	}
	trial, err := t.runner.TryAgent(ctx, input.AgentID, input.Question)
	if err != nil {
		return operationFailure(fmt.Sprintf("the agent could not be tried: %v", err)), nil
	}
	out, err := json.Marshal(trimAgentTrial(trial))
	if err != nil {
		return operationFailure(err.Error()), nil
	}
	return &types.ToolResult{Success: true, Output: string(out)}, nil
}

// ApprovalDescription names the agent and the question on the confirmation
// card.
func (t *TryAgentTool) ApprovalDescription(ctx context.Context, args json.RawMessage) string {
	input, err := parseTryAgentInput(args)
	if err != nil {
		return ""
	}
	name := input.AgentID
	if resolved, err := t.runner.AgentName(ctx, input.AgentID); err == nil && resolved != "" {
		name = resolved
	}
	question := truncateRunes(input.Question, 200)
	if strings.HasPrefix(types.LanguageFromContextOrDefault(ctx), "zh") {
		return fmt.Sprintf("试跑智能体「%s」：%s", name, question)
	}
	return fmt.Sprintf("Try agent %q: %s", name, question)
}

// ApprovalGrant lets the user allow further trials of the same agent for the
// rest of the session; the per-turn trial limit still applies.
func (t *TryAgentTool) ApprovalGrant(ctx context.Context, args json.RawMessage) (string, string) {
	input, err := parseTryAgentInput(args)
	if err != nil {
		return "", ""
	}
	name, err := t.runner.AgentName(ctx, input.AgentID)
	if err != nil || name == "" {
		return "", ""
	}
	if strings.HasPrefix(types.LanguageFromContextOrDefault(ctx), "zh") {
		return ToolTryAgent + " agent:" + input.AgentID, fmt.Sprintf("试跑智能体「%s」", name)
	}
	return ToolTryAgent + " agent:" + input.AgentID, fmt.Sprintf("Try agent %q", name)
}

func trimAgentTrial(trial *AgentTrial) *AgentTrial {
	out := *trial
	if utf8.RuneCountInString(out.Answer) > maxTrialAnswerRunes {
		out.Answer = truncateRunes(out.Answer, maxTrialAnswerRunes)
		out.AnswerTruncated = true
	}
	if len(out.ToolCalls) > maxTrialToolCalls {
		out.ToolCalls = out.ToolCalls[:maxTrialToolCalls]
	}
	calls := make([]AgentTrialToolCall, len(out.ToolCalls))
	for i, call := range out.ToolCalls {
		call.Error = truncateRunes(call.Error, maxTrialToolErrorRunes)
		calls[i] = call
	}
	out.ToolCalls = calls
	if len(out.References) > maxTrialReferences {
		out.References = out.References[:maxTrialReferences]
	}
	return &out
}
