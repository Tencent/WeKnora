package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSandboxCheckpointRoundTrips(t *testing.T) {
	at := time.Date(2026, 9, 10, 9, 59, 0, 0, time.UTC)
	original := SandboxCheckpoint{SandboxID: "sbx-1", CommitSHA: "abc123", CommittedAt: at}

	raw, err := original.Value()
	require.NoError(t, err)

	var decoded SandboxCheckpoint
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, original.SandboxID, decoded.SandboxID)
	require.Equal(t, original.CommitSHA, decoded.CommitSHA)
	require.True(t, original.CommittedAt.Equal(decoded.CommittedAt))
}

func TestSandboxCheckpointScanNilYieldsZeroValue(t *testing.T) {
	var decoded SandboxCheckpoint
	require.NoError(t, decoded.Scan(nil))
	require.Equal(t, SandboxCheckpoint{}, decoded)
}

func TestSandboxCheckpointScanRejectsGarbage(t *testing.T) {
	var decoded SandboxCheckpoint
	require.Error(t, decoded.Scan([]byte("not json")))
}

func TestForkBootstrapConsumedReportsState(t *testing.T) {
	pending := ForkBootstrap{SnapshotID: "snap-1", CommitSHA: "abc123"}
	require.False(t, pending.Consumed())

	at := time.Now().UTC()
	done := ForkBootstrap{SnapshotID: "snap-1", CommitSHA: "abc123", ConsumedAt: &at}
	require.True(t, done.Consumed())
}

func TestForkBootstrapRoundTripsConsumedAt(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	original := ForkBootstrap{
		SnapshotID:      "snap-1",
		CommitSHA:       "abc123",
		SourceSandboxID: "sbx-1",
		CreatedAt:       at,
		ConsumedAt:      &at,
	}

	raw, err := original.Value()
	require.NoError(t, err)

	var decoded ForkBootstrap
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, original.SnapshotID, decoded.SnapshotID)
	require.NotNil(t, decoded.ConsumedAt)
	require.True(t, at.Equal(*decoded.ConsumedAt))
}
