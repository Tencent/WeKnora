package approval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantGate answers every approval it raises with answer and counts them.
func grantGate(t *testing.T, answer Decision) (*Gate, *event.EventBus, *[]event.ToolApprovalRequiredData) {
	t.Helper()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil, nil)
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
	d, err := g.RequestBuiltinApproval(context.Background(), req)
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
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
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
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
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
	d, err := g.RequestBuiltinApproval(context.Background(), grantRequest(bus, "s1", "g"))
	require.NoError(t, err)
	assert.False(t, d.Approved)
	assert.False(t, d.AllowInSession)
}
