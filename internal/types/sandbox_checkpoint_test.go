package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxCheckpointBackgroundActiveJSON(t *testing.T) {
	raw, err := json.Marshal(SandboxCheckpoint{SandboxID: "sbx", CommitSHA: "sha"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "background_active", "unflagged checkpoints keep their old shape")

	var flagged SandboxCheckpoint
	require.NoError(t, flagged.Scan([]byte(`{"sandbox_id":"sbx","commit_sha":"sha","background_active":true}`)))
	require.True(t, flagged.BackgroundActive)

	var old SandboxCheckpoint
	require.NoError(t, old.Scan([]byte(`{"sandbox_id":"sbx","commit_sha":"sha"}`)))
	require.False(t, old.BackgroundActive, "rows written before the flag existed stay usable")
}
