package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func collaborationTools(t *testing.T) (*ToolRegistry, *collaboration.Binding, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tools.db")), &gorm.Config{})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, db.AutoMigrate(&collaboration.Record{}))
	scope := collaboration.Scope{TenantID: 1, UserID: "u", SessionID: "s", ID: "g"}
	store := collaboration.NewStore(db)
	require.NoError(t, store.Ensure(context.Background(), scope))
	b := &collaboration.Binding{Runtime: collaboration.New(store, nil), Scope: scope}
	registry := NewToolRegistry()
	for _, tool := range NewCollaborationTools(b,
		func(context.Context,
			string) (string,
			error,
		) {
			return `{"secret_config":"must not leak"}`,
				nil
		}) {
		registry.RegisterTool(tool)
	}
	ctx := WithToolExecContext(
		context.Background(),
		&ToolExecContext{ToolCallID: "call1", AssistantMessageID: "message1"},
	)
	return registry, b, ctx
}

func TestCollaborationExactlySixToolsAndNoAuthorityArguments(t *testing.T) {
	r, _, ctx := collaborationTools(t)
	require.ElementsMatch(t, CollaborationToolNames, r.ListTools())
	result, err := r.ExecuteTool(
		ctx,
		"spawn_agent",
		json.RawMessage(`{"agent_id":"a","task_name":"one","message":"q","tenant_id":2}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	result, err = r.ExecuteTool(ctx, "spawn_agent", json.RawMessage(`{"agent_id":"a","task_name":"one","message":"q"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	result, err = r.ExecuteTool(ctx, "list_agents", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.NotContains(t, result.Output, "secret_config")
	require.NotContains(t, result.Output, "owner")
	require.NotContains(t, result.Output, "query")
	var list []collaboration.MemberView
	require.NoError(t, json.Unmarshal([]byte(result.Output), &list))
	require.Len(t, list, 1)
	require.Equal(t, "queued", list[0].Status)
}

func TestCollaborationRequiresIdempotencyMetadata(t *testing.T) {
	r, b, _ := collaborationTools(t)
	result, err := r.ExecuteTool(
		context.Background(),
		"spawn_agent",
		json.RawMessage(`{"agent_id":"a","task_name":"one","message":"q"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	g, err := b.Runtime.Store.Read(context.Background(), b.Scope)
	require.NoError(t, err)
	require.Empty(t, g.Members)
}

func TestChildCannotControlOthersOrStartRoot(t *testing.T) {
	_, b, ctx := collaborationTools(t)
	b.Child = &collaboration.Work{}
	tools := NewCollaborationTools(b, nil)
	require.Len(t, tools, 1)
	require.Equal(t, "send_message", tools[0].Name())
	result, err := tools[0].Execute(ctx, json.RawMessage(`{"target":"sibling","message":"q"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
}

func TestChildSendMessageReachesParentBeforeCompletion(t *testing.T) {
	_, parent, ctx := collaborationTools(t)
	_, err := parent.Runtime.Spawn(ctx, parent.Scope, "seed", "research", "a", "{}", "work")
	require.NoError(t, err)
	var work collaboration.Work
	require.NoError(t, parent.Runtime.Store.Change(ctx, parent.Scope, func(g *collaboration.Group) error {
		m := g.Members[0]
		r := m.Runs[0]
		r.State, r.Owner, r.Lease = "running", "worker", time.Now().Add(time.Minute)
		work = collaboration.Work{Scope: parent.Scope, Member: *m, Run: *r}
		return nil
	}))
	child := &collaboration.Binding{Runtime: parent.Runtime, Scope: parent.Scope, Child: &work}
	childTools := NewCollaborationTools(child, nil)
	require.Len(t, childTools, 1)
	tool := childTools[0]
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(tool.Parameters(), &schema))
	require.Len(t, schema.Properties, 2)
	require.Equal(t, []string{"/root"}, schema.Properties["target"].Enum)
	require.Contains(t, tool.Description(), "parent assistant")
	require.NotContains(t, tool.Description(), "Use followup_task")
	args := json.RawMessage(`{"target":"/root","message":"Sources checked; one article is unavailable."}`)
	for range 2 { // Replaying the same tool call must not duplicate the mailbox event.
		result, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		require.True(t, result.Success)
	}
	updated, err := parent.Wait(ctx, 0, time.Second)
	require.NoError(t, err)
	require.True(t, updated)
	// A fresh binding after restart observes the durable message too.
	recovered := &collaboration.Binding{Runtime: parent.Runtime, Scope: parent.Scope}
	events, err := recovered.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "message", events[0].Kind)
	require.Equal(t, "research", events[0].From)
	require.Equal(t, "Sources checked; one article is unavailable.", events[0].Content)
	views, err := parent.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "running", views[0].Status)
	require.NoError(t, parent.Runtime.Cancel(ctx, parent.Scope))
	lateCtx := WithToolExecContext(ctx, &ToolExecContext{ToolCallID: "late", AssistantMessageID: "message1"})
	result, err := tool.Execute(lateCtx, args)
	require.NoError(t, err)
	require.False(t, result.Success)
	g, err := parent.Runtime.Store.Read(ctx, parent.Scope)
	require.NoError(t, err)
	for _, event := range g.Events {
		if event.Kind == "message" {
			require.Equal(t, events[0].Sequence, event.Sequence)
		}
	}
}

func TestRuntimeGuardCoversRegistryExecution(t *testing.T) {
	r, _, ctx := collaborationTools(t)
	r.SetExecutionGuard(func(context.Context, types.Tool) error { return collaboration.ErrConflict })
	result, err := r.ExecuteTool(
		ctx,
		"spawn_agent",
		json.RawMessage(`{"agent_id":"a","task_name":"one","message":"q"}`),
	)
	require.ErrorIs(t, err, collaboration.ErrConflict)
	require.False(t, result.Success)
}

func TestWaitOnlyReturnsNotificationAndRuntimeStillDeliversBody(t *testing.T) {
	r, b, ctx := collaborationTools(t)
	_, err := b.Runtime.Spawn(ctx, b.Scope, "seed", "one", "a", "{}", "q")
	require.NoError(t, err)
	require.NoError(t, b.Runtime.Interrupt(ctx, b.Scope, "one", ""))
	result, err := r.ExecuteTool(ctx, "wait_agent", json.RawMessage(`{"timeout_ms":1000}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"updated":true`)
	require.Contains(t, result.Output, `"interrupted":false`)
	require.Zero(t, b.Seen)
	events, err := b.Poll(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "interrupted", events[0].Kind)
}

func TestWaitReportsSteerAsInterruptionNotTimeout(t *testing.T) {
	registry, b, ctx := collaborationTools(t)
	b.HasInput = func(context.Context) (bool, error) { return true, nil }
	result, err := registry.ExecuteTool(ctx, "wait_agent", json.RawMessage(`{"timeout_ms":60000}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"interrupted":true`)
	require.Contains(t, result.Output, `"timed_out":false`)
}
