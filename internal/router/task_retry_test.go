package router

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
)

func TestAsynqRetryDelayUsesFixedDelayForPendingDataSourceIngest(t *testing.T) {
	err := fmt.Errorf("retry reconciliation: %w", service.ErrDataSourceIngestPending)

	delay := asynqRetryDelayFunc(8, err, asynq.NewTask("datasource:sync", nil))

	assert.Equal(t, dataSourceIngestPendingRetryDelay, delay)
}
