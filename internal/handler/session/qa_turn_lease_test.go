package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func setupLeaseTestStream(ctx context.Context, t *testing.T, mode qaMode) *sseStreamContext {
	t.Helper()
	h := &Handler{streamManager: stream.NewMemoryStreamManager()}
	req := &qaRequestContext{
		ctx:              ctx,
		skipSSE:          true,
		sessionID:        "session",
		session:          &types.Session{ID: "session", TenantID: 42},
		assistantMessage: &types.Message{ID: "assistant", SessionID: "session"},
	}
	if mode == qaModeAgent {
		req.customAgent = &types.CustomAgent{ID: "agent"}
	}
	got := h.setupSSEStream(req, false, mode)
	t.Cleanup(got.cancel)
	return got
}

func TestSetupSSEStreamCancelsOnTurnLeaseLoss(t *testing.T) {
	for name, mode := range map[string]qaMode{"normal": qaModeNormal, "agent": qaModeAgent} {
		for _, lostBeforeSetup := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/during-run", true: "/before-setup"}[lostBeforeSetup],
				func(t *testing.T) {
					requestCtx, disconnect := context.WithCancel(t.Context())
					defer disconnect()
					// The handler detaches from HTTP before acquiring the turn lease.
					baseCtx := logger.CloneContext(requestCtx)
					reg := admission.NewMemory()
					scope := admission.MachineScope("machine")
					member := admission.SessionMember("session", "turn", "token")
					lease, err := reg.Hold(baseCtx, scope, member, admission.DefaultHoldTTL)
					require.NoError(t, err)
					defer lease.Release()
					turnCtx, endTurn := admission.Bind(baseCtx, lease)
					defer endTurn()
					turnCtx = context.WithValue(turnCtx, types.SandboxAdmissionExemptContextKey, []string{member})
					if lostBeforeSetup {
						reg.ExpireMember(scope, member)
						require.Eventually(t, func() bool { return turnCtx.Err() != nil },
							time.Second, time.Millisecond)
					}

					got := setupLeaseTestStream(turnCtx, t, mode)
					require.Equal(t, []string{member}, got.asyncCtx.Value(types.SandboxAdmissionExemptContextKey))
					if lostBeforeSetup {
						require.ErrorIs(t, context.Cause(got.asyncCtx), admission.ErrLeaseLost)
					} else {
						disconnect()
						require.NoError(t, got.asyncCtx.Err(), "HTTP disconnect must not stop the detached turn")
						reg.ExpireMember(scope, member)
					}
					require.Eventually(t, func() bool { return got.asyncCtx.Err() != nil },
						time.Second, time.Millisecond,
						"the actual QA context must stop when the turn loses its admission")
					require.ErrorIs(t, context.Cause(got.asyncCtx), admission.ErrLeaseLost)
				})
		}
	}
}

func TestSetupSSEStreamKeepsOrdinaryCancellationDetached(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	got := setupLeaseTestStream(parent, t, qaModeNormal)
	require.Never(t, func() bool { return got.asyncCtx.Err() != nil }, 30*time.Millisecond, time.Millisecond,
		"ordinary request cancellation is not a lost sandbox lease")
	got.cancel()
	require.ErrorIs(t, got.asyncCtx.Err(), context.Canceled, "explicit stop must still cancel QA")
}
