package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestVerifyAcceptsUntamperedReportAndRejectsMutation(t *testing.T) {
	report := types.EvaluationEvidenceReport{
		SchemaVersion: 1,
		Task:          &types.EvaluationTask{ID: "task-1", DatasetID: "fixed"},
	}
	unsigned, err := json.Marshal(&report)
	require.NoError(t, err)
	report.ReportSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(unsigned))
	encoded, err := json.MarshalIndent(&report, "", "  ")
	require.NoError(t, err)
	require.NoError(t, verify(bytes.NewReader(encoded)))

	report.Task.DatasetID = "tampered"
	tampered, err := json.Marshal(&report)
	require.NoError(t, err)
	require.ErrorContains(t, verify(bytes.NewReader(tampered)), "checksum mismatch")
}
