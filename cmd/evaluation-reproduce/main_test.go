package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPassesAndWritesBothArtifacts(t *testing.T) {
	t.Setenv("GITHUB_SHA", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	outputDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--dataset", "../../dataset/golden/v1/dataset.json",
		"--thresholds", "../../evaluation/regression/thresholds.json",
		"--output-dir", outputDir,
	}, &stdout, &stderr)
	require.Zero(t, code, stderr.String())
	require.FileExists(t, filepath.Join(outputDir, "evaluation-regression.json"))
	require.FileExists(t, filepath.Join(outputDir, "evaluation-regression.md"))
	require.Contains(t, stdout.String(), "gate: PASS")
}

func TestRunDegradationFixtureReturnsNonZeroWithDiagnostic(t *testing.T) {
	t.Setenv("GITHUB_SHA", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--dataset", "../../dataset/golden/v1/dataset.json",
		"--thresholds", "../../evaluation/regression/thresholds.json",
		"--metric-overrides", "../../evaluation/regression/testdata/degraded.json",
		"--output-dir", t.TempDir(),
	}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "metric=retrieval.recall")
	require.Contains(t, stderr.String(), "baseline=0.75")
	require.Contains(t, stderr.String(), "current=0.5")
	require.Contains(t, stderr.String(), "absolute_delta=0.25")
	require.Contains(t, stderr.String(), "threshold=0")
}
