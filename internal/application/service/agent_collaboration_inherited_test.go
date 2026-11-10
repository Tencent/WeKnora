package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestInheritedChildSnapshotKeepsEffectiveScopeWithoutSecrets(t *testing.T) {
	_, agents, _, ctx := collaborationService(t)
	agents.agent.Config.ModelID = "template-model"
	ctx = context.WithValue(ctx, childSourceKey{}, childSource{Agent: agents.agent, ModelID: "input-model"})
	cfg := &types.AgentConfig{
		SystemPrompt: "effective instructions", ReasoningEffort: "high", MaxCompletionTokens: 24576,
		MaxIterations: -1, AllowedTools: []string{tools.ToolWikiReadPage},
		MCPSelectionMode: "selected", MCPServices: []string{"selected-mcp"},
		SkillsEnabled: true, AllowedSkills: []string{"selected-skill"},
		SandboxConfigID: "sandbox", LocalBrowserEnabled: true,
		AssistantAccess: types.AssistantAccessAutoWrite,
		KnowledgeBases:  []string{"kb"}, KnowledgeIDs: []string{"doc"},
		SearchTargets: types.SearchTargets{
			{
				Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", TenantID: 7,
				KnowledgeIDs: []string{"doc"}, TagIDs: []string{"tag"},
			},
		}, WritableKBIDs: []string{"kb"},
		TenantSkills:            []*types.TenantSkillEntity{{Name: "selected-skill", Instructions: "private-body"}},
		AttachmentSandboxCopies: map[string]string{"private-path": "attachment://secret"},
	}
	raw, err := inheritedChildSnapshot(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotContains(t, raw, "private-body")
	require.NotContains(t, raw, "attachment://secret")
	cfg.MCPServices[0] = "later-service"
	agents.agent.Config.ModelID = "later-model"
	var saved delegatedConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &saved))
	require.Equal(t, "input-model", saved.modelID())
	require.Equal(t, "template-model", saved.Agent.Config.ModelID)
	require.Equal(t, []string{"selected-mcp"}, saved.Inherited.Config.MCPServices)
	require.Equal(t, []string{"selected-skill"}, saved.Inherited.SkillNames)
	svc := &sessionService{knowledgeBaseService: &tagTargetKnowledgeBaseService{
		kbs: map[string]*types.KnowledgeBase{"kb": {ID: "kb", TenantID: 7}},
	}}
	tenant := &types.Tenant{
		ID:              7,
		AssistantConfig: &types.AssistantConfig{MaxAccessLevel: types.AssistantAccessConfirm},
	}
	restored, err := svc.restoreInheritedConfig(ctx, &saved, tenant)
	require.NoError(t, err)
	require.Equal(t, types.AssistantAccessAutoWrite, restored.AssistantAccess,
		"an ordinary agent's child is not held to the assistant ceiling")
	assistantChild := saved
	assistantChild.AssistantParent = true
	capped, err := svc.restoreInheritedConfig(ctx, &assistantChild, tenant)
	require.NoError(t, err)
	require.Equal(t, types.AssistantAccessConfirm, capped.AssistantAccess,
		"the assistant's child stays under the workspace's current ceiling")
	require.Equal(t, []string{"doc"}, restored.SearchTargets[0].KnowledgeIDs)
	require.Equal(t, []string{"tag"}, restored.SearchTargets[0].TagIDs)
	require.Equal(t, []string{"kb"}, restored.WritableKBIDs)
	require.Equal(t, "effective instructions", restored.SystemPrompt)
	require.Equal(t, "high", restored.ReasoningEffort)
	require.Equal(t, 24576, restored.MaxCompletionTokens)
	require.Equal(t, -1, restored.MaxIterations)
	require.Equal(t, "sandbox", restored.SandboxConfigID)
	require.True(t, restored.LocalBrowserEnabled)
	require.True(t, restored.MultiTurnEnabled)
	require.Empty(t, restored.TenantSkills, "unavailable installed skills must not be restored from stale bodies")
	tenant.AssistantConfig.MaxAccessLevel = types.AssistantAccessReadOnly
	restored, err = svc.restoreInheritedConfig(ctx, &assistantChild, tenant)
	require.NoError(t, err)
	require.Empty(t, restored.WritableKBIDs)
	svc.knowledgeBaseService = &tagTargetKnowledgeBaseService{kbs: map[string]*types.KnowledgeBase{}}
	_, err = svc.restoreInheritedConfig(ctx, &saved, tenant)
	require.ErrorContains(t, err, "no longer available")
}

func TestInheritedSpawnWorksWithoutTemplateIDAndSurvivesRestartFollowup(t *testing.T) {
	s, agents, _, ctx := collaborationService(t)
	ctx = context.WithValue(ctx, childSourceKey{}, childSource{Agent: agents.agent, ModelID: "chosen-model"})
	registry := tools.NewToolRegistry()
	require.NoError(t, s.attach(ctx, &agent.AgentEngine{}, registry, &types.AgentConfig{
		SystemPrompt: "pinned prompt", AssistantAccess: types.AssistantAccessConfirm,
	}, "parent", "parent-answer"))
	tool, err := registry.GetTool("spawn_agent")
	require.NoError(t, err)
	var schema struct {
		Required []string `json:"required"`
	}
	require.NoError(t, json.Unmarshal(tool.Parameters(), &schema))
	require.ElementsMatch(t, []string{"task_name", "message"}, schema.Required)
	for i, args := range []string{
		`{"task_name":"check","message":"check independently"}`,
		`{"task_name":"research","message":"research independently"}`,
		`{"agent_id":"a","task_name":"delegate","message":"use template"}`,
	} {
		callCtx := tools.WithToolExecContext(ctx, &tools.ToolExecContext{
			ToolCallID: fmt.Sprint(i), AssistantMessageID: "parent-answer",
		})
		result, err := registry.ExecuteTool(callCtx, "spawn_agent", json.RawMessage(args))
		require.NoError(t, err)
		require.True(t, result.Success, "%+v", result)
	}
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-answer"}
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Len(t, g.Members, 3)
	require.Empty(t, g.Members[0].AgentID)
	require.Equal(t, "a", g.Members[2].AgentID)
	require.Equal(t, "check", collaborationStatusView(g).Tasks[0].DisplayName)
	// A new scheduler has only persisted state, no original parent request/config.
	s.Runtime = collaboration.New(s.Runtime.Store, s.run)
	started := make(chan string, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]string{}
	s.Bind(&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		b := collaboration.FromContext(ctx)
		if b.Child.Member.AgentID == "" {
			if req.SummaryModelID != "chosen-model" {
				return errors.New("input model lost")
			}
			saved, err := inheritedConfigFromBinding(ctx)
			if err != nil {
				return err
			}
			if saved == nil || saved.Inherited.Config.SystemPrompt != "pinned prompt" {
				return errors.New("effective config lost")
			}
		} else if req.SummaryModelID != "" {
			return errors.New("template model overwritten")
		}
		mu.Lock()
		old := seen[b.Child.Member.Name]
		seen[b.Child.Member.Name] = req.Session.ID
		mu.Unlock()
		if old != "" && old != req.Session.ID {
			return errors.New("followup changed session")
		}
		started <- b.Child.Member.Name
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return bus.Emit(ctx, event.Event{
			Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: req.Query},
		})
	}})
	s.Runtime.Poll = 5 * time.Millisecond
	s.Runtime.Start(ctx)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, s.Runtime.Stop(stop))
	})
	for range 3 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("three workers did not execute concurrently")
		}
	}
	close(release)
	require.Eventually(t, func() bool {
		g, err := s.Runtime.Store.Read(ctx, scope)
		if err != nil {
			return false
		}
		for _, m := range g.Members {
			if m.Runs[0].State != "succeeded" {
				return false
			}
		}
		return true
	}, 3*time.Second, 5*time.Millisecond)
	_, err = s.Runtime.Send(ctx, scope, "followup", "check", "continue existing work", "", true)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		g, err := s.Runtime.Store.Read(ctx, scope)
		return err == nil && len(g.Members[0].Runs) == 2 && g.Members[0].Runs[1].State == "succeeded"
	}, 3*time.Second, 5*time.Millisecond)
}

func TestInheritedSnapshotDoesNotAcceptUntrustedOrPrivilegedSource(t *testing.T) {
	_, agents, _, ctx := collaborationService(t)
	_, err := inheritedChildSnapshot(ctx, &types.AgentConfig{}, nil)
	require.Error(t, err)
	ctx = context.WithValue(ctx, childSourceKey{}, childSource{Agent: agents.agent, ModelID: "model"})
	_, err = inheritedChildSnapshot(ctx, &types.AgentConfig{SharedAgentReadOnly: true}, nil)
	require.Error(t, err)
	cfg := &types.AgentConfig{}
	cfg.EnableSkillInstallMode(types.BuiltinSkillInstallerID, "/install")
	_, err = inheritedChildSnapshot(ctx, cfg, nil)
	require.Error(t, err)
	agents.agent.TenantID = 8
	_, err = inheritedChildSnapshot(ctx, &types.AgentConfig{}, nil)
	require.Error(t, err)
}

func TestInheritedWorkersRequireApprovalForWritesAndShell(t *testing.T) {
	for _, name := range []string{
		tools.ToolWikiWritePage, tools.ToolWikiDeletePage, tools.ToolShellExec,
		tools.ToolWriteSandboxFile, tools.ToolEditSandboxFile,
	} {
		t.Run(name, func(t *testing.T) {
			testChildWikiConfirmation(t, name, types.AssistantAccessConfirm, true)
		})
	}
}
