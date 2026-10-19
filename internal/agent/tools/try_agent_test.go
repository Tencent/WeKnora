package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTrialRunner struct {
	trial     *AgentTrial
	err       error
	questions []string
}

func (r *fakeTrialRunner) AgentName(_ context.Context, agentID string) (string, error) {
	if agentID == "missing" {
		return "", errors.New("not found")
	}
	return "Support bot", nil
}

func (r *fakeTrialRunner) TryAgent(_ context.Context, _ string, question string) (*AgentTrial, error) {
	r.questions = append(r.questions, question)
	if r.err != nil {
		return nil, r.err
	}
	return r.trial, nil
}

func TestTryAgentReportsTheTrialWithinABudget(t *testing.T) {
	runner := &fakeTrialRunner{trial: &AgentTrial{
		AgentName: "Support bot",
		Answer:    strings.Repeat("a", maxTrialAnswerRunes+10),
		ToolCalls: []AgentTrialToolCall{{Name: "knowledge_search", Error: strings.Repeat("e", 1000)}},
	}}
	tool := NewTryAgentTool(runner)
	args := json.RawMessage(`{"agent_id":"a1","question":"  How do I reset a password?  "}`)

	res, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	assert.Equal(t, []string{"How do I reset a password?"}, runner.questions)
	var got AgentTrial
	require.NoError(t, json.Unmarshal([]byte(res.Output), &got))
	assert.True(t, got.AnswerTruncated)
	assert.LessOrEqual(t, utf8.RuneCountInString(got.Answer), maxTrialAnswerRunes+3)
	assert.Less(t, utf8.RuneCountInString(got.ToolCalls[0].Error), 1000)
	assert.Equal(t, 1000, utf8.RuneCountInString(runner.trial.ToolCalls[0].Error),
		"trimming leaves the runner's result alone")

	for range maxTrialsPerRun - 1 {
		res, err = tool.Execute(context.Background(), args)
		require.NoError(t, err)
		require.True(t, res.Success)
	}
	res, err = tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "already used")
	assert.Len(t, runner.questions, maxTrialsPerRun)
}

func TestTryAgentRejectsBadInput(t *testing.T) {
	runner := &fakeTrialRunner{err: errors.New("this agent manages WeKnora and cannot be tried")}
	tool := NewTryAgentTool(runner)
	for _, args := range []string{
		`{"agent_id":"","question":"q"}`,
		`{"agent_id":"a1","question":"   "}`,
		`{"agent_id":"a1","question":"` + strings.Repeat("q", maxTrialQuestionRunes+1) + `"}`,
		`{"agent_id":1}`,
	} {
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		require.NoError(t, err)
		assert.False(t, res.Success, args)
	}
	assert.Empty(t, runner.questions, "bad input never reaches the runner")

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"agent_id":"a1","question":"q"}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "cannot be tried")
}

func TestTryAgentConfirmationNamesAgentAndQuestion(t *testing.T) {
	tool := NewTryAgentTool(&fakeTrialRunner{trial: &AgentTrial{}})
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	confirmed := NewConfirmedTool(tool, operations.RiskWrite,
		operations.Access{Level: types.AssistantAccessConfirm}, gate)

	en := context.WithValue(interactiveCtx(), types.LanguageContextKey, "en-US")
	res, err := confirmed.Execute(en, json.RawMessage(`{"agent_id":"a1","question":"Where is the VPN guide?"}`))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Equal(t, `Try agent "Support bot": Where is the VPN guide?`, gate.requests[0].Description)

	zh := context.WithValue(context.Background(), types.LanguageContextKey, "zh-CN")
	assert.Equal(t, "试跑智能体「missing」：q", tool.ApprovalDescription(zh,
		json.RawMessage(`{"agent_id":"missing","question":"q"}`)), "an unresolved agent is named by its ID")
}
