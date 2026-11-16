package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type automationModels struct {
	interfaces.ModelService
	models map[string]*types.Model
}

func (m *automationModels) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	return m.models[id], nil
}

func setAutomationModels(f *automationFixture) *automationModels {
	models := &automationModels{models: map[string]*types.Model{
		"one": {
			ID: "one", TenantID: automationTenant, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
		},
		"two": {
			ID: "two", TenantID: automationTenant, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
		},
	}}
	f.svc.models = models
	return models
}

func TestAutomationModelOverridePersistsAndRuns(t *testing.T) {
	f := newAutomationFixture(t)
	setAutomationModels(f)
	a := f.create(t, types.AutomationPatch{ModelID: ptr(" one ")})
	require.Equal(t, "one", f.reload(t, a.ID).ModelID)
	for _, id := range []string{"two", ""} {
		f.now = f.now.Add(time.Minute)
		_, err := f.svc.Update(f.ctx, a.ID, &types.AutomationPatch{ModelID: &id})
		require.NoError(t, err)
		require.Equal(t, id, f.reload(t, a.ID).ModelID)
		queued, err := f.svc.RunNow(f.ctx, a.ID)
		require.NoError(t, err)
		run := f.settleRun(t, queued.ID)
		require.Equal(t, types.AutomationRunSucceeded, run.Status)
		require.Equal(t, id, f.sessions.last().req.SummaryModelID)
		require.Equal(t, id, f.sessions.last().req.CustomAgent.Config.ModelID)
		require.Empty(t, f.agents.agent.Config.ModelID, "an override must not mutate the agent")
	}
}

func TestAutomationModelOverrideRejectsUnavailableModels(t *testing.T) {
	f := newAutomationFixture(t)
	models := setAutomationModels(f)
	models.models["other"] = &types.Model{
		ID: "other", TenantID: automationTenant + 1, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
	}
	models.models["embedding"] = &types.Model{
		ID: "embedding", TenantID: automationTenant, Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive,
	}
	models.models["inactive"] = &types.Model{
		ID: "inactive", TenantID: automationTenant,
		Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusDownloading,
	}
	a := f.create(t, types.AutomationPatch{ModelID: ptr("one")})
	for _, id := range []string{"missing", "other", "embedding", "inactive"} {
		_, err := f.svc.Update(f.ctx, a.ID, &types.AutomationPatch{ModelID: &id})
		require.Error(t, err, id)
		require.Equal(t, "one", f.reload(t, a.ID).ModelID)
	}
	delete(models.models, "one")
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunFailed, run.Status)
	require.Empty(t, f.sessions.calls, "a deleted override must not silently fall back to another model")
}

func TestAutomationModelEditDoesNotChangeSuspendedRun(t *testing.T) {
	f := newAutomationFixture(t)
	setAutomationModels(f)
	a := f.create(t, types.AutomationPatch{ModelID: ptr("one")})
	f.suspendOnApproval(t, "op:knowledge.delete:kb1")
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunAwaitingApproval, run.Status)
	_, err = f.svc.Update(f.ctx, a.ID, &types.AutomationPatch{ModelID: ptr("two")})
	require.NoError(t, err)
	_, err = f.broker.Store().Transition(f.ctx, run.PendingPromptID, types.AgentPromptTransition{
		From: []string{types.AgentPromptSuspended}, To: types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true}`),
	})
	require.NoError(t, err)
	prompt, err := f.broker.Store().Get(f.ctx, run.PendingPromptID)
	require.NoError(t, err)
	require.True(t, f.svc.HandlesPrompt(f.ctx, prompt))
	run = f.settleRun(t, run.ID)
	require.Equal(t, types.AutomationRunSucceeded, run.Status)
	require.Equal(t, "one", f.sessions.last().req.SummaryModelID)
	require.Equal(t, "two", f.reload(t, a.ID).ModelID)
}
