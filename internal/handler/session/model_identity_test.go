package session

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSetupSSEStreamPropagatesBusinessSession(t *testing.T) {
	for _, mode := range []qaMode{qaModeNormal, qaModeAgent} {
		h := &Handler{streamManager: stream.NewMemoryStreamManager()}
		reqCtx := &qaRequestContext{
			c:                newSteerWiringGinContext(),
			ctx:              types.WithSessionID(t.Context(), "parent-session"),
			sessionID:        "business-session",
			session:          &types.Session{ID: "business-session"},
			assistantMessage: &types.Message{ID: "assistant", SessionID: "business-session"},
		}
		streamCtx := h.setupSSEStream(reqCtx, false, mode)
		require.NotNil(t, streamCtx)
		id, ok := types.SessionIDFromContext(streamCtx.asyncCtx)
		require.True(t, ok)
		require.Equal(t, reqCtx.session.ID, id)
		streamCtx.cancel()
	}
}
