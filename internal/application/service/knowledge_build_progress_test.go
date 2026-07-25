package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildKnowledgeProgress(t *testing.T) {
	got := buildKnowledgeProgress(map[string]int64{
		types.ParseStatusPending:         2,
		types.ParseStatusProcessing:      1,
		types.ParseStatusFinalizing:      1,
		types.ParseStatusCompleted:       4,
		types.ParseStatusFailed:          1,
		types.ParseStatusCancelled:       1,
		types.ManualKnowledgeStatusDraft: 1,
		"custom":                         1,
	})

	assert.Equal(t, &types.KnowledgeBuildProgress{
		Total:      12,
		Settled:    8,
		InFlight:   4,
		Pending:    2,
		Processing: 1,
		Finalizing: 1,
		Completed:  4,
		Failed:     1,
		Cancelled:  1,
		Draft:      1,
		Other:      1,
		Percentage: 66,
	}, got)
}

func TestBuildKnowledgeProgressEmpty(t *testing.T) {
	assert.Equal(t, &types.KnowledgeBuildProgress{}, buildKnowledgeProgress(nil))
}
