package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/policy"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memGrants is an in-memory GrantStore keyed like the agent_grants table.
type memGrants struct {
	mu     sync.Mutex
	grants map[string]*types.AgentGrant
}

func newMemGrants() *memGrants { return &memGrants{grants: map[string]*types.AgentGrant{}} }

func memGrantKey(tenantID uint64, userID string, scope types.GrantScope, key string) string {
	return fmt.Sprint(tenantID, "|", userID, "|", scope.Scope, "|", scope.ScopeID, "|", key)
}

func (m *memGrants) FindGrant(
	_ context.Context, tenantID uint64, userID string, scope types.GrantScope, key string,
) (*types.AgentGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.grants[memGrantKey(tenantID, userID, scope, key)]; ok {
		copied := *g
		return &copied, nil
	}
	return nil, nil
}

func (m *memGrants) SaveGrant(_ context.Context, g *types.AgentGrant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := *g
	copied.ID = memGrantKey(g.TenantID, g.UserID, types.GrantScope{Scope: g.Scope, ScopeID: g.ScopeID}, g.GrantKey)
	copied.CreatedAt = time.Now()
	m.grants[copied.ID] = &copied
	return nil
}

func (m *memGrants) TouchGrant(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.grants[id]; ok {
		g.LastUsedAt = &at
	}
	return nil
}

func (m *memGrants) revokeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, g := range m.grants {
		g.RevokedAt = &now
	}
}

// grantGate answers every approval it raises with answer and counts them.
func grantGate(t *testing.T, answer Decision) (*Gate, *event.EventBus, *[]event.ToolApprovalRequiredData) {
	t.Helper()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil, nil).
		WithGrants(newMemGrants())
	bus := event.NewEventBus()
	var raised []event.ToolApprovalRequiredData
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.ToolApprovalRequiredData)
		raised = append(raised, data)
		go func() {
			_ = g.Resolve(context.Background(), ResolveRequest{
				TenantID: data.TenantID, UserID: "u1", PendingID: data.PendingID, Decision: answer,
			})
		}()
		return nil
	})
	return g, bus, &raised
}

func grantRequest(bus *event.EventBus, session, grant string) PendingRequest {
	return PendingRequest{
		TenantID: 7, UserID: "u1", SessionID: session, EventBus: bus,
		ServiceName: "WeKnora", MCPToolName: "knowledge.update", RegisteredToolName: "run_operation",
		Args: json.RawMessage(`{}`), Grant: grant, GrantScope: "Update a document",
	}
}

func approveAndRun(t *testing.T, g *Gate, req PendingRequest) Decision {
	t.Helper()
	d, err := g.RequestBuiltinApproval(attended(), req)
	require.NoError(t, err)
	g.MarkExecuted(context.Background(), d.PendingID)
	return d
}

func TestSessionGrantSkipsLaterApprovalsOfTheSameKind(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	grant := "knowledge.update knowledge_base:kb1"

	first := approveAndRun(t, g, grantRequest(bus, "s1", grant))
	require.True(t, first.Approved)
	assert.False(t, first.Granted)
	assert.True(t, first.AllowInSession)
	require.Len(t, *raised, 1)
	assert.Equal(t, "Update a document", (*raised)[0].GrantScope, "the card offers the grant")

	second := approveAndRun(t, g, grantRequest(bus, "s1", grant))
	assert.True(t, second.Approved)
	assert.True(t, second.Granted)
	assert.Empty(t, second.PendingID)
	assert.Len(t, *raised, 1, "an allowed kind of call does not ask again")

	approveAndRun(t, g, grantRequest(bus, "s1", "knowledge.update knowledge_base:kb2"))
	approveAndRun(t, g, grantRequest(bus, "s2", grant))
	other := grantRequest(bus, "s1", grant)
	other.UserID = "u2"
	ctx, cancel := context.WithTimeout(attended(), 100*time.Millisecond)
	defer cancel()
	d, _ := g.RequestBuiltinApproval(ctx, other)
	assert.False(t, d.Approved, "u2's prompt is answered by nobody here")
	assert.Len(t, *raised, 4, "another knowledge base, session or user asks again")
}

func TestApprovalWithoutSessionGrantAsksAgain(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true})
	grant := "knowledge.update knowledge_base:kb1"
	approveAndRun(t, g, grantRequest(bus, "s1", grant))
	d := approveAndRun(t, g, grantRequest(bus, "s1", grant))
	assert.False(t, d.Granted)
	assert.Len(t, *raised, 2)
}

func TestRequestWithoutGrantCannotBeAllowedForTheSession(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	ctx, cancel := context.WithTimeout(attended(), 300*time.Millisecond)
	defer cancel()
	d, err := g.RequestBuiltinApproval(ctx, grantRequest(bus, "s1", ""))
	require.NoError(t, err)
	assert.False(t, d.Approved, "allowing what the card did not offer is refused, so the prompt stays open")
	require.Len(t, *raised, 1)
	assert.Empty(t, (*raised)[0].GrantScope, "no grant is offered")
}

func TestAllowInSessionNeedsApprovalAndAnOfferedGrant(t *testing.T) {
	offered, _ := json.Marshal(types.ToolApprovalPromptRequest{Grant: "g", GrantScope: "scope"})
	notOffered, _ := json.Marshal(types.ToolApprovalPromptRequest{})
	for _, tc := range []struct {
		name     string
		request  []byte
		response string
		ok       bool
	}{
		{"approve and allow", offered, `{"approved":true,"allow_in_session":true}`, true},
		{"plain approve", notOffered, `{"approved":true}`, true},
		{"reject and allow", offered, `{"approved":false,"allow_in_session":true}`, false},
		{"allow what was not offered", notOffered, `{"approved":true,"allow_in_session":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolApprovalResponse(tc.request, json.RawMessage(tc.response))
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestRejectingDropsAllowInSession(t *testing.T) {
	g := NewGate(nil, nil, hitl.NewBroker(nil, nil))
	bus := event.NewEventBus()
	var pending string
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		pending = evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			_ = g.Resolve(context.Background(), ResolveRequest{
				TenantID: 7, UserID: "u1", PendingID: pending,
				Decision: Decision{Approved: false, AllowInSession: true},
			})
		}()
		return nil
	})
	d, err := g.RequestBuiltinApproval(attended(), grantRequest(bus, "s1", "g"))
	require.NoError(t, err)
	assert.False(t, d.Approved)
	assert.False(t, d.AllowInSession)
}

func TestGrantsBelongToTheRunScope(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	grant := "mcp:svc/write#abc"
	root := types.GrantScope{Scope: types.AgentGrantScopeSession, ScopeID: "root"}
	child := func() context.Context { return types.WithGrantScope(attended(), root) }

	d, err := g.RequestBuiltinApproval(child(), grantRequest(bus, "child-1", grant))
	require.NoError(t, err)
	g.MarkExecuted(context.Background(), d.PendingID)
	require.Len(t, *raised, 1)

	d, err = g.RequestBuiltinApproval(child(), grantRequest(bus, "child-2", grant))
	require.NoError(t, err)
	assert.True(t, d.Granted, "a sibling child shares the root conversation's grant")
	d = approveAndRun(t, g, grantRequest(bus, "root", grant))
	assert.True(t, d.Granted, "so does the root conversation")
	assert.Len(t, *raised, 1)
}

func TestRevokedOrRebindedGrantAsksAgain(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	store := g.grants.(*memGrants)
	grant := "mcp:svc/write#abc"
	bound := func(binding string) context.Context {
		return types.WithGrantScope(attended(), types.GrantScope{
			Scope: types.AgentGrantScopeAutomation, ScopeID: "auto-1", BindingHash: binding,
		})
	}
	run := func(ctx context.Context) Decision {
		d, err := g.RequestBuiltinApproval(ctx, grantRequest(bus, "s", grant))
		require.NoError(t, err)
		g.MarkExecuted(context.Background(), d.PendingID)
		return d
	}
	run(bound("v1"))
	assert.True(t, run(bound("v1")).Granted)
	assert.False(t, run(bound("v2")).Granted, "a changed automation asks again")
	require.Len(t, *raised, 2)
	assert.Equal(t, types.AgentGrantLapseChanged, (*raised)[1].GrantLapsed, "the card says why it asks again")

	store.revokeAll()
	assert.False(t, run(bound("v2")).Granted, "a revoked grant asks again")
	require.Len(t, *raised, 3)
	assert.Empty(t, (*raised)[2].GrantLapsed, "the user withdrew it, so nothing to explain")
}

func TestStandingGrantsCoverOnlyWhatTheyWereApprovedFor(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	store := g.grants.(*memGrants)
	standing := types.GrantScope{Scope: types.AgentGrantScopeAutomation, ScopeID: "auto-1", BindingHash: "v1"}
	runCtx := func() context.Context {
		ctx := types.WithGrantScope(attended(), types.GrantScope{Scope: types.AgentGrantScopeSession, ScopeID: "run-1"})
		return types.WithStandingGrantScope(ctx, standing)
	}
	require.NoError(t, store.SaveGrant(context.Background(), &types.AgentGrant{
		TenantID: 7, UserID: "u1", Scope: standing.Scope, ScopeID: standing.ScopeID, BindingHash: "v1",
		GrantKey: "knowledge.delete knowledge_base:kb1", Risk: string(policy.RiskDestructive),
	}))
	require.NoError(t, store.SaveGrant(context.Background(), &types.AgentGrant{
		TenantID: 7, UserID: "u1", Scope: standing.Scope, ScopeID: standing.ScopeID, BindingHash: "v1",
		GrantKey: "mcp:svc/purge#abc", Risk: string(policy.RiskWrite),
	}))
	ask := func(grant string, risk policy.Risk) Decision {
		req := grantRequest(bus, "run-1", grant)
		req.GrantRisk = risk
		d, err := g.RequestBuiltinApproval(runCtx(), req)
		require.NoError(t, err)
		g.MarkExecuted(context.Background(), d.PendingID)
		return d
	}

	assert.True(t, ask("knowledge.delete knowledge_base:kb1", policy.RiskDestructive).Granted,
		"a destructive call pre-approved for this knowledge base runs")
	assert.Empty(t, *raised)

	assert.False(t, ask("knowledge.delete knowledge_base:kb2", policy.RiskDestructive).Granted,
		"another knowledge base asks")
	require.Len(t, *raised, 1)
	assert.Empty(t, (*raised)[0].GrantScope, "a destructive call is never offered for the rest of the run")

	assert.False(t, ask("mcp:svc/purge#abc", policy.RiskDestructive).Granted,
		"a grant approved for a write does not cover the tool once it is destructive")
	assert.Len(t, *raised, 2)
}

func TestApprovalsDuringAnAutomationRunStayInTheRun(t *testing.T) {
	g, bus, raised := grantGate(t, Decision{Approved: true, AllowInSession: true})
	standing := types.GrantScope{Scope: types.AgentGrantScopeAutomation, ScopeID: "auto-1"}
	run := func(session string) Decision {
		ctx := types.WithGrantScope(attended(), types.GrantScope{Scope: types.AgentGrantScopeSession, ScopeID: session})
		ctx = types.WithStandingGrantScope(ctx, standing)
		d, err := g.RequestBuiltinApproval(ctx, grantRequest(bus, session, "mcp:svc/write#abc"))
		require.NoError(t, err)
		g.MarkExecuted(context.Background(), d.PendingID)
		return d
	}
	run("run-1")
	assert.True(t, run("run-1").Granted, "the run remembers its own approval")
	assert.False(t, run("run-2").Granted, "the next run does not inherit it")
	assert.Len(t, *raised, 2)
	found, err := g.grants.FindGrant(context.Background(), 7, "u1", standing, "mcp:svc/write#abc")
	require.NoError(t, err)
	assert.Nil(t, found, "nothing approved during a run becomes a standing grant")
}
