package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCompletedMessageRestoresCollaborationStatus(t *testing.T) {
	view := json.RawMessage(`{"group_id":"group","revision":12,"tasks":[{"id":"worker","status":"succeeded"}]}`)
	h := &MessageHandler{
		CollaborationStatuses: func(_ context.Context,
			session string,
			ids []string) (map[string]json.RawMessage,
			error,
		) {
			require.Equal(t, "session", session)
			require.Equal(t, []string{"answer", "other"}, ids)
			return map[string]json.RawMessage{"answer": view}, nil
		},
	}
	completed := &types.Message{ID: "answer", IsCompleted: true}
	other := &types.Message{ID: "other"}
	h.attachPendingPrompts(context.Background(), "session", []*types.Message{completed, other})
	require.JSONEq(t, string(view), string(completed.CollaborationStatus))
	require.Empty(t, other.CollaborationStatus)
	encoded, err := json.Marshal(completed)
	require.NoError(t, err)
	var response map[string]any
	require.NoError(t, json.Unmarshal(encoded, &response))
	require.IsType(
		t,
		map[string]any{},
		response["collaboration_status"],
		"history returns an object, not encoded JSON text",
	)
}
