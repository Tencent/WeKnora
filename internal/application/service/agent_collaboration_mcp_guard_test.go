package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestCollaborationMCPGuardOnlyMatchesOwnedActiveCall(t *testing.T) {
	type fixture struct {
		prompt  *types.AgentPrompt
		run     *collaboration.Run
		request types.ToolApprovalPromptRequest
		meta    *tools.ToolExecContext
		caller  types.Caller
		child   bool
		newRun  bool
		args    string
	}
	cases := []struct {
		name    string
		blocked bool
		change  func(*fixture)
	}{
		{"pending", true, func(f *fixture) { f.prompt.Status = types.AgentPromptPending }},
		{"suspended", true, func(f *fixture) { f.prompt.Status = types.AgentPromptSuspended }},
		{"answered", true, func(f *fixture) { f.prompt.Status = types.AgentPromptAnswered }},
		{"executing", true, func(f *fixture) { f.prompt.Status = types.AgentPromptExecuting }},
		{"done-approved-while-summarizing", true, func(f *fixture) {
			f.prompt.Status = types.AgentPromptDone
			f.prompt.Response = types.JSON(`{"approved":true}`)
		}},
		{"queued-resume", true, func(f *fixture) { f.run.State = "queued" }},
		{"waiting", true, func(f *fixture) { f.run.State = "waiting" }},
		{"different-service", false, func(f *fixture) { f.request.ServiceID = "other" }},
		{"different-native-tool", false, func(f *fixture) { f.request.MCPToolName = "other" }},
		{"different-args", false, func(f *fixture) { f.args = `{"id":2,"tags":["a","b"]}` }},
		{"reordered-array", false, func(f *fixture) { f.args = `{"id":1,"tags":["b","a"]}` }},
		{"modified-args", true, func(f *fixture) {
			f.prompt.Status = types.AgentPromptExecuting
			f.prompt.Response = types.JSON(`{"approved":true,"modified_args":{"id":2}}`)
			f.args = `{"id":2.0}`
		}},
		{"modified-original-no-longer-matches", false, func(f *fixture) {
			f.prompt.Status = types.AgentPromptExecuting
			f.prompt.Response = types.JSON(`{"approved":true,"modified_args":{"id":2}}`)
		}},
		{"done-rejected", false, func(f *fixture) {
			f.prompt.Status = types.AgentPromptDone
			f.prompt.Response = types.JSON(`{"approved":false}`)
		}},
		{"canceled-prompt", false, func(f *fixture) { f.prompt.Status = types.AgentPromptCanceled }},
		{"finished-run", false, func(f *fixture) { f.run.State = "succeeded" }},
		{"failed-run", false, func(f *fixture) { f.run.State = "failed" }},
		{"canceled-run", false, func(f *fixture) { f.run.State = "cancelled" }},
		{"cancel-requested", false, func(f *fixture) { f.run.CancelRequested = true }},
		{"replaced-run", false, func(f *fixture) { f.newRun = true }},
		{"child-caller", false, func(f *fixture) { f.child = true }},
		{"other-caller-tenant", false, func(f *fixture) { f.caller.TenantID = 8 }},
		{"other-caller-user", false, func(f *fixture) { f.caller.UserID = "other" }},
		{"other-parent-session", false, func(f *fixture) { f.meta.SessionID = "other" }},
		{"other-parent-message", false, func(f *fixture) { f.meta.AssistantMessageID = "other" }},
		{"other-prompt-tenant", false, func(f *fixture) { f.prompt.TenantID = 8 }},
		{"other-prompt-user", false, func(f *fixture) { f.prompt.UserID = "web_user:other" }},
		{"other-child-session", false, func(f *fixture) { f.prompt.SessionID = "other" }},
		{"other-child-message", false, func(f *fixture) { f.prompt.AssistantMessageID = "other" }},
		{"other-prompt-kind", false, func(f *fixture) { f.prompt.Kind = types.AgentPromptKindAskUser }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, ctx := collaborationService(t)
			s.BindRecovery(hitl.NewBroker(nil, nil), nil, nil)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
			b, err := s.Runtime.AcquireParent(ctx, scope)
			require.NoError(t, err)
			_, err = s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", "{}", "task")
			require.NoError(t, err)
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			f := fixture{
				prompt: &types.AgentPrompt{
					ID:                 "prompt",
					TenantID:           7,
					UserID:             "web_user:u",
					SessionID:          g.Members[0].SessionID,
					AssistantMessageID: "child-message",
					Kind:               types.AgentPromptKindToolApproval,
					Status:             types.AgentPromptSuspended,
				},
				run: g.Members[0].Runs[0],
				request: types.ToolApprovalPromptRequest{
					ServiceID:   "service",
					MCPToolName: "native_tool",
					Args:        json.RawMessage(`{"id":1,"tags":["a","b"]}`),
				},
				meta:   &tools.ToolExecContext{SessionID: scope.SessionID, AssistantMessageID: scope.ID},
				caller: types.CallerFromContext(ctx), args: `{"tags":["a","b"],"id":1.0}`,
			}
			f.run.State, f.run.MessageID, f.run.PromptID = "running", "child-message", "prompt"
			tc.change(&f)
			raw, err := json.Marshal(f.request)
			require.NoError(t, err)
			f.prompt.Request = raw
			require.NoError(t, s.broker.Store().Create(ctx, f.prompt))
			require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(group *collaboration.Group) error {
				group.Members[0].Runs[0] = f.run
				if f.newRun {
					group.Members[0].Runs = append(
						group.Members[0].Runs,
						&collaboration.Run{ID: "new", State: "running"},
					)
				}
				return nil
			}))
			if f.child {
				b.Child = &collaboration.Work{}
			}
			ctx = tools.WithToolExecContext(types.WithCaller(ctx, f.caller), f.meta)
			result, err := s.guardDelegatedMCPCall(ctx, b, "service", "native_tool", json.RawMessage(f.args))
			require.NoError(t, err)
			if !tc.blocked {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.False(t, result.Success)
			require.Nil(t, result.Suspend)
			require.Equal(t, "delegated_call_in_progress", result.Data["code"])
			require.Equal(t, delegatedCallInProgressError, result.Error)
			rows, err := s.broker.Store().
				ListBySession(ctx, scope.SessionID, []string{types.AgentPromptPending, types.AgentPromptSuspended})
			require.NoError(t, err)
			require.Empty(t, rows, "coordination must never create or reuse a parent approval")
		})
	}
}

func TestCollaborationMCPArgumentEquality(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		equal bool
	}{
		{`{"a":1,"b":{"c":2}}`, `{"b":{"c":2.0},"a":1e0}`, true},
		{`{"id":9007199254740992}`, `{"id":9007199254740993}`, false},
		{`{"a":null}`, `{}`, false},
		{`{"a":"1"}`, `{"a":1}`, false},
		{`{"a":-0.0}`, `{"a":0}`, true},
		{`{"a":1e999999999999999999}`, `{"a":10e999999999999999998}`, true},
		{`{"a":1} trailing`, `{"a":1}`, false},
	} {
		require.Equal(t, tc.equal, sameMCPArguments([]byte(tc.a), []byte(tc.b)), "%s / %s", tc.a, tc.b)
	}
}

type pausedMCPApprovalRead struct {
	interfaces.AgentPromptRepository
	once     sync.Once
	captured chan struct{}
	release  chan struct{}
}

func (s *pausedMCPApprovalRead) Get(ctx context.Context, id string) (*types.AgentPrompt, error) {
	p, err := s.AgentPromptRepository.Get(ctx, id)
	s.once.Do(func() {
		close(s.captured)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return p, err
}

func TestCollaborationMCPGuardRechecksConcurrentTransitions(t *testing.T) {
	for _, action := range []string{
		"cancel-child",
		"finish-child",
		"cancel-prompt",
		"resume-prompt",
		"edit-approved-args",
		"cancel-parent",
		"transfer-parent",
	} {
		t.Run(action, func(t *testing.T) {
			s, _, _, ctx := collaborationService(t)
			broker := hitl.NewBroker(nil, nil)
			s.BindRecovery(broker, nil, nil)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
			b, err := s.Runtime.AcquireParent(ctx, scope)
			require.NoError(t, err)
			_, err = s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", "{}", "task")
			require.NoError(t, err)
			require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
				r := g.Members[0].Runs[0]
				r.State, r.PromptID, r.MessageID = "running", "prompt", "child-message"
				return nil
			}))
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			require.NoError(t, broker.Store().Create(ctx, &types.AgentPrompt{
				ID: "prompt", TenantID: 7, UserID: "web_user:u", SessionID: g.Members[0].SessionID,
				AssistantMessageID: "child-message",
				Kind:               types.AgentPromptKindToolApproval,
				Status:             types.AgentPromptSuspended,

				Request: types.JSON(`{"service_id":"service","mcp_tool_name":"native_tool","args":{"id":1}}`),
			}))
			paused := &pausedMCPApprovalRead{
				AgentPromptRepository: broker.Store(),
				captured:              make(chan struct{}),
				release:               make(chan struct{}),
			}
			s.broker = hitl.NewBroker(paused, nil)
			ctx = tools.WithToolExecContext(
				ctx,
				&tools.ToolExecContext{SessionID: "parent", AssistantMessageID: "message"},
			)
			type outcome struct {
				result *types.ToolResult
				err    error
			}
			completed := make(chan outcome, 1)
			go func() {
				result, err := s.guardDelegatedMCPCall(ctx, b, "service", "native_tool", json.RawMessage(`{"id":1}`))
				completed <- outcome{result, err}
			}()
			select {
			case <-paused.captured:
			case <-time.After(time.Second):
				t.Fatal("guard did not read approval")
			}
			switch action {
			case "cancel-child":
				require.NoError(t, s.Runtime.Interrupt(ctx, scope, "worker", ""))
			case "finish-child":
				require.NoError(
					t,
					s.Runtime.Store.Change(
						ctx,
						scope,
						func(g *collaboration.Group) error { g.Members[0].Runs[0].State = "succeeded"; return nil },
					),
				)
			case "cancel-prompt", "resume-prompt", "edit-approved-args":
				status, response := types.AgentPromptCanceled, types.JSON(nil)
				if action == "resume-prompt" {
					status, response = types.AgentPromptExecuting, types.JSON(`{"approved":true}`)
				}
				if action == "edit-approved-args" {
					status, response = types.AgentPromptAnswered, types.JSON(
						`{"approved":true,"modified_args":{"id":2}}`,
					)
				}
				changed, err := broker.Store().
					Transition(ctx,
						"prompt",
						types.AgentPromptTransition{
							From: []string{
								types.AgentPromptSuspended,
							},
							To:       status,
							Response: response,
						})
				require.NoError(t, err)
				require.True(t, changed)
			case "cancel-parent":
				require.NoError(t, s.Runtime.Cancel(ctx, scope))
			case "transfer-parent":
				require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
					g.ParentOwner = "replacement"
					g.ParentMessageID = "replacement-message"
					return nil
				}))
			}
			close(paused.release)
			select {
			case got := <-completed:
				switch action {
				case "cancel-parent":
					require.ErrorIs(t, got.err, collaboration.ErrClosed)
				case "transfer-parent":
					require.ErrorIs(t, got.err, collaboration.ErrConflict)
				case "resume-prompt":
					require.NoError(t, got.err)
					require.NotNil(t, got.result)
					require.Equal(t, "delegated_call_in_progress", got.result.Data["code"])
				default:
					require.NoError(t, got.err)
					require.Nil(t, got.result, "ended/canceled/changed child call cannot fence the parent")
				}
			case <-time.After(time.Second):
				t.Fatal("guard did not finish")
			}
		})
	}
}
