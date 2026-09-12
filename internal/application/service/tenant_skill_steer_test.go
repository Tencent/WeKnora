package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newGuidanceFixture(t *testing.T) (*installFixture, *installSteerSink) {
	fx := newInstallFixture(t)
	fx.svc.streams = stream.NewMemoryStreamManager()
	row, err := fx.svc.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	row.InstallSessionID, row.InstallMessageID = "sess-1", "msg-1"
	require.NoError(t, fx.skillRepo.UpdateSkill(context.Background(), row))
	tr := newInstallTranscript(context.Background(), nil, fx.svc.streams, fx.svc.messages, "sess-1", "msg-1", nil)
	sink := &installSteerSink{service: fx.svc, transcript: tr}
	require.NoError(t, fx.svc.streams.SetLiveRun(context.Background(), installSteerSession("sess-1"), "msg-1", ""))
	return fx, sink
}

func TestInstallGuidanceScopesAndDeduplicates(t *testing.T) {
	fx, sink := newGuidanceFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	send := func(tenant uint64, config, message, content string) error {
		return fx.svc.SteerInstall(ctx, tenant, config, "sk-1", message, id, content)
	}
	require.Error(t, send(8, "cfg-1", "msg-1", "install bsk"))
	require.Error(t, send(7, "other", "msg-1", "install bsk"))
	require.Error(t, send(7, "cfg-1", "old-run", "install bsk"))
	require.NoError(t, send(7, "cfg-1", "msg-1", "install bsk"))
	require.NoError(t, send(7, "cfg-1", "msg-1", "install bsk"))
	require.Error(t, send(7, "cfg-1", "msg-1", "different content"))
	state, err := fx.svc.InstallGuidance(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.True(t, state.Accepting)
	require.Len(t, state.Messages, 1)
	require.Equal(t, "pending", state.Messages[0].Status)
	closed, err := sink.closeIfDrained(ctx)
	require.NoError(t, err)
	require.False(t, closed)
	pending, _, err := sink.PollSteer(ctx, "sess-1", "msg-1", 0)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NotEmpty(t, sink.PersistSteerMessage(ctx, "sess-1", "msg-1", id, "install bsk", nil, ""))
	closed, err = sink.closeIfDrained(ctx)
	require.NoError(t, err)
	require.True(t, closed)
	require.Error(t, fx.svc.SteerInstall(ctx, 7, "cfg-1", "sk-1", "msg-1", uuid.NewString(), "late"))
	state, err = fx.svc.InstallGuidance(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, state.Accepting)
	require.Equal(t, "injected", state.Messages[0].Status)
	live, _, err := fx.svc.streams.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Empty(t, live, "ordinary chat routes must never expose the maintenance run")
}

func TestInstallGuidanceUnprocessedOnFailure(t *testing.T) {
	fx, _ := newGuidanceFixture(t)
	ctx := context.Background()
	require.NoError(t, fx.svc.SteerInstall(ctx, 7, "cfg-1", "sk-1", "msg-1", uuid.NewString(), "use the official CLI"))
	row, _ := fx.svc.GetSkill(ctx, 7, "cfg-1", "sk-1")
	row.Status = types.SkillStatusFailed
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, row))
	state, err := fx.svc.InstallGuidance(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, state.Accepting)
	require.Equal(t, "unprocessed", state.Messages[0].Status)
}

func TestInstallGuidanceArrivingAtNaturalStopContinuesBeforeSnapshot(t *testing.T) {
	fx := newInstallFixture(t)
	fx.svc.streams = stream.NewMemoryStreamManager()
	calls := 0
	fx.afterExecute = func() {
		calls++
		if calls == 1 {
			require.NoError(
				t,
				fx.svc.SteerInstall(
					context.Background(),
					7,
					"cfg-1",
					"sk-1",
					fx.currentInstallMessageID(),
					uuid.NewString(),
					"Check the external CLI too",
				),
			)
		}
	}
	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))
	require.Equal(t, 2, calls)
	require.Contains(t, fx.events, "create-snapshot")
	state, err := fx.svc.InstallGuidance(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, state.Accepting)
	require.Len(t, state.Messages, 1)
	require.Equal(t, "injected", state.Messages[0].Status)
}

func TestInstallGuidanceSurvivesRepairRound(t *testing.T) {
	fx := newInstallFixture(t)
	fx.svc.streams = stream.NewMemoryStreamManager()
	fx.loadCheckExitCodes = []int{skillVerifyRepairableExit, 0}
	first := true
	fx.beforeExecute = func() {
		if first {
			first = false
			require.NoError(
				t,
				fx.svc.SteerInstall(
					context.Background(),
					7,
					"cfg-1",
					"sk-1",
					fx.currentInstallMessageID(),
					uuid.NewString(),
					"Use our package mirror",
				),
			)
		}
	}
	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle))
	require.Len(t, fx.agentPrompts, 2)
	require.Contains(t, fx.agentPrompts[1], "Use our package mirror")
}

func TestInstallGuidanceCloseAndSendAcrossReplicas(t *testing.T) {
	fx, sink := newGuidanceFixture(t)
	mini := miniredis.RunT(t)
	mgr, err := stream.NewRedisStreamManager(mini.Addr(), "", "", 0, "steer-test", time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	fx.svc.redis, fx.svc.streams = client, mgr
	// A second service instance shares only the DB and Redis state.
	other := &TenantSkillService{skills: fx.skillRepo, streams: mgr, redis: client}
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		require.NoError(t, mgr.SetLiveRun(ctx, installSteerSession("sess-1"), "msg-1", ""))
		id := uuid.NewString()
		var sendErr, closeErr error
		var closed bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			sendErr = other.SteerInstall(ctx, 7, "cfg-1", "sk-1", "msg-1", id, "check readiness")
		}()
		go func() { defer wg.Done(); closed, closeErr = sink.closeIfDrained(ctx) }()
		wg.Wait()
		require.NoError(t, closeErr)
		if sendErr == nil {
			require.False(t, closed, "an accepted message must prevent the transition to verification")
			_, err := mgr.UpdateSteerEventData(ctx, "sess-1", "msg-1", id, map[string]interface{}{"consumed": true})
			require.NoError(t, err)
			_, err = sink.closeIfDrained(ctx)
			require.NoError(t, err)
		} else {
			require.True(t, closed)
		}
	}
}

var (
	_ types.SteerSink          = (*installSteerSink)(nil)
	_ interfaces.StreamManager = (*stream.MemoryStreamManager)(nil)
)

func TestInstallGuidanceQueueLimit(t *testing.T) {
	fx, _ := newGuidanceFixture(t)
	for i := 0; i < 10; i++ {
		require.NoError(
			t,
			fx.svc.SteerInstall(context.Background(), 7, "cfg-1", "sk-1", "msg-1", uuid.NewString(), "guidance"),
		)
	}
	require.Error(
		t,
		fx.svc.SteerInstall(context.Background(), 7, "cfg-1", "sk-1", "msg-1", uuid.NewString(), "eleventh"),
	)
}

func TestReinstallInstructionsAreInInitialAndRepairPrompts(t *testing.T) {
	fx := newInstallFixture(t)
	fx.loadCheckExitCodes = []int{skillVerifyRepairableExit, 0}
	require.NoError(t, fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle, "Use our package mirror"))
	require.Len(t, fx.agentPrompts, 2)
	for _, prompt := range fx.agentPrompts {
		require.Contains(t, prompt, "Use our package mirror")
	}
}

type failingGuidanceConsumption struct{ interfaces.StreamManager }

func (s failingGuidanceConsumption) UpdateSteerEventData(
	context.Context,
	string,
	string,
	string,
	map[string]interface{},
) (bool, error) {
	return false, context.DeadlineExceeded
}

func TestInstallGuidancePersistenceFailurePreventsSnapshot(t *testing.T) {
	fx := newInstallFixture(t)
	fx.svc.streams = failingGuidanceConsumption{stream.NewMemoryStreamManager()}
	fx.beforeExecute = func() {
		require.NoError(
			t,
			fx.svc.SteerInstall(
				context.Background(),
				7,
				"cfg-1",
				"sk-1",
				fx.currentInstallMessageID(),
				uuid.NewString(),
				"Verify bsk",
			),
		)
	}
	err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle)
	require.ErrorContains(t, err, "guidance could not be processed")
	require.NotContains(t, fx.events, "create-snapshot")
	state, err := fx.svc.InstallGuidance(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, state.Accepting)
	require.Equal(t, "unprocessed", state.Messages[0].Status)
}

func TestReinstallWithInstructionsDoesNotSkipReadyArchive(t *testing.T) {
	fx := newInstallFixture(t)
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD, "scripts/extract.py": "print('hi')\n"})
	bundle, err := ParseSkillBundle(archive)
	require.NoError(t, err)
	fx.seedReadySkillWithSHA(bundle.SHA256, "previous-snapshot")
	row, err := fx.svc.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	row.BundleRef = "file://bundle.zip"
	require.NoError(t, fx.skillRepo.UpdateSkill(context.Background(), row))
	fx.storedBundles = map[string][]byte{"file://bundle.zip": archive}
	id, err := fx.svc.ReinstallSkill(context.Background(), 7, "cfg-1", "sk-1", "Install and verify the CLI")
	require.NoError(t, err)
	require.Equal(t, "sk-1", id)
	require.Eventually(t, func() bool {
		row, err := fx.svc.GetSkill(context.Background(), 7, "cfg-1", id)
		return err == nil && row.InstallMessageID != "" && row.Status == types.SkillStatusReady
	}, 5*time.Second, 10*time.Millisecond)
	require.NotEmpty(t, fx.agentPrompts)
	require.Contains(t, fx.agentPrompts[0], "Install and verify the CLI")
}
