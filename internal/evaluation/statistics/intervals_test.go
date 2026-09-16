package statistics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrapMeanIsDeterministic(t *testing.T) {
	first, err := BootstrapMean([]float64{0, 0.5, 1, 1}, 0.95, 2000, 42)
	require.NoError(t, err)
	second, err := BootstrapMean([]float64{0, 0.5, 1, 1}, 0.95, 2000, 42)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 0.625, first.Estimate)
	require.LessOrEqual(t, first.Lower, first.Estimate)
	require.GreaterOrEqual(t, first.Upper, first.Estimate)
}

func TestWilsonKnownInterval(t *testing.T) {
	interval, err := Wilson(50, 100, 0.95)
	require.NoError(t, err)
	require.InDelta(t, 0.4038, interval.Lower, 0.001)
	require.InDelta(t, 0.5962, interval.Upper, 0.001)
	require.Equal(t, "wilson_score", interval.Method)
}

func TestWilsonRejectsInsufficientSample(t *testing.T) {
	_, err := Wilson(1, 1, 0.95)
	require.Error(t, err)
}

func TestPercentilesAreDeterministic(t *testing.T) {
	p50, p95, p99, err := Percentiles([]float64{4, 1, 3, 2})
	require.NoError(t, err)
	require.InDelta(t, 2.5, p50, 1e-12)
	require.InDelta(t, 3.85, p95, 1e-12)
	require.InDelta(t, 3.97, p99, 1e-12)
}
