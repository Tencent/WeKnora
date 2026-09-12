package container

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationTaskRetentionWiringRepository struct {
	interfaces.EvaluationTaskRepository
}

func TestEvaluationTaskRetentionWiringValidatesConfiguration(t *testing.T) {
	cleaner := &evaluationTaskRecoveryWiringCleaner{}

	negative := -1
	err := startEvaluationTaskRetention(
		&config.Config{Evaluation: &config.EvaluationConfig{RetentionDays: &negative}},
		&evaluationTaskRetentionWiringRepository{},
		cleaner,
	)
	require.Error(t, err, "negative retention_days must fail startup")

	zero := 0
	err = startEvaluationTaskRetention(
		&config.Config{Evaluation: &config.EvaluationConfig{RetentionDays: &zero}},
		&evaluationTaskRetentionWiringRepository{},
		cleaner,
	)
	require.NoError(t, err)
	assert.Empty(t, cleaner.names, "zero retention_days disables the runner")
}

func TestEvaluationTaskRetentionWiringStartsAndRegistersShutdown(t *testing.T) {
	cleaner := &evaluationTaskRecoveryWiringCleaner{}
	days := 1
	err := startEvaluationTaskRetention(
		&config.Config{Evaluation: &config.EvaluationConfig{RetentionDays: &days}},
		&evaluationTaskRetentionWiringRepository{},
		cleaner,
	)
	require.NoError(t, err)

	cleaner.mu.Lock()
	names := append([]string(nil), cleaner.names...)
	cleaner.mu.Unlock()
	assert.Equal(t, []string{"EvaluationTaskRetentionRunner"}, names)
	require.Empty(t, cleaner.Cleanup(context.Background()))
}
