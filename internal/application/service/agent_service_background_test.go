package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type stubWorkHandler struct{ interfaces.WorkHandler }

func newBackgroundTestEngine(t *testing.T, registry *tools.ToolRegistry) *agent.AgentEngine {
	t.Helper()
	engine := agent.NewAgentEngine(&types.AgentConfig{MaxIterations: 1}, nil, registry, nil, nil, nil, "s", "")
	require.NotNil(t, engine)
	return engine
}

func TestBackgroundWorkIsDisabledWithoutAHandler(t *testing.T) {
	var nilWork *BackgroundWork
	require.False(t, nilWork.Enabled())
	work := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	require.False(t, work.Enabled())
	require.NotNil(t, work.Listeners)
	work.Handler = stubWorkHandler{}
	require.True(t, work.Enabled())
}

func TestAttachBackgroundWorkRegistersNoWorkToolsWithoutASubmitter(t *testing.T) {
	work := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	work.Handler = stubWorkHandler{}
	s := &agentService{background: work}
	registry := tools.NewToolRegistry()
	engine := newBackgroundTestEngine(t, registry)
	ctx := types.WithSandboxTenantID(context.Background(), 7)

	s.attachBackgroundWork(ctx, engine, registry, "s", "m")

	_, err := registry.GetTool(tools.ToolWorkWait)
	require.Error(t, err, "no submission tool is registered, so the run cannot start background work")
	_, err = registry.GetTool(tools.ToolWorkCancel)
	require.Error(t, err)
}

var (
	stubAsyncToolBound bool
	stubAsyncBoundRef  interfaces.SessionRef
	stubAsyncBoundRun  string
	stubAsyncMarks     interface{ MarkStarted(string) }
)

// stubAsyncTool wraps a real async shell tool. attachBackgroundWork binds by
// the BindBackgroundRun method, which this wrapper records.
type stubAsyncTool struct {
	*tools.ShellExecAsyncTool
}

func (s *stubAsyncTool) BindBackgroundRun(
	ref interfaces.SessionRef, runID string, marks interface{ MarkStarted(string) },
) {
	stubAsyncToolBound = true
	stubAsyncBoundRef = ref
	stubAsyncBoundRun = runID
	stubAsyncMarks = marks
	s.ShellExecAsyncTool.BindBackgroundRun(ref, runID, marks)
}

func TestAttachBackgroundWorkBindsAsyncSubmitter(t *testing.T) {
	stubAsyncToolBound = false
	stubAsyncBoundRef = interfaces.SessionRef{}
	stubAsyncBoundRun = ""
	stubAsyncMarks = nil

	work := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	work.Handler = stubWorkHandler{}
	s := &agentService{background: work}
	registry := tools.NewToolRegistry()
	engine := newBackgroundTestEngine(t, registry)
	registry.RegisterTool(&stubAsyncTool{ShellExecAsyncTool: tools.NewShellExecAsyncTool(nil, nil, nil)})
	ctx := types.WithSandboxTenantID(context.Background(), 7)

	s.attachBackgroundWork(ctx, engine, registry, "s", "m")

	_, err := registry.GetTool(tools.ToolWorkWait)
	require.NoError(t, err, "有提交工具时 work_* 必须注册（D10 静态）")
	require.True(t, stubAsyncToolBound, "BindBackgroundRun 被调用（D11）")
	require.Equal(t, interfaces.SessionRef{TenantID: 7, SessionID: "s"}, stubAsyncBoundRef)
	require.Equal(t, "m", stubAsyncBoundRun)
	notices, ok := stubAsyncMarks.(*background.RunNotices)
	require.True(t, ok, "BindBackgroundRun receives the run's RunNotices")
	require.NoError(t, work.Registry.Add(ctx, stubAsyncBoundRef, interfaces.BackgroundActivity{
		ID: "sj_probe", Kind: "shell", Title: "probe", StartedAt: time.Now(),
	}, time.Minute, 0))
	notices.MarkStarted("sj_probe")
	delivery, err := notices.BeforeFinal(ctx)
	require.NoError(t, err)
	require.Contains(t, delivery.Text, "sj_probe")
}

func TestAttachBackgroundWorkIsANoOpWhenDisabled(t *testing.T) {
	registry := tools.NewToolRegistry()
	engine := newBackgroundTestEngine(t, registry)
	for _, s := range []*agentService{{}, {background: NewBackgroundWork(nil, nil)}} {
		s.attachBackgroundWork(context.Background(), engine, registry, "s", "m")
	}
	require.Empty(t, registry.ListTools())
}
