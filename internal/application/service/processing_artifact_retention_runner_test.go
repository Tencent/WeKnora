package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type processingArtifactPurgeCountingService struct {
	interfaces.ProcessingArtifactRetentionService
	calls       atomic.Int64
	err         error
	mu          sync.Mutex
	batchSize   int
	deadlineSet bool
	deadline    time.Time
}

type processingArtifactSnapshotRegistry struct {
	snapshotCalls atomic.Int64
	counters      []types.ProcessingArtifactCounter
}

type classifiedProcessingArtifactPurgeError struct {
	kind    string
	message string
}

func (e classifiedProcessingArtifactPurgeError) Error() string {
	return e.message
}

func (e classifiedProcessingArtifactPurgeError) ProcessingArtifactFailureKind() string {
	return e.kind
}

func (r *processingArtifactSnapshotRegistry) Record(string, string) {}

func (r *processingArtifactSnapshotRegistry) Snapshot() []types.ProcessingArtifactCounter {
	r.snapshotCalls.Add(1)
	return append([]types.ProcessingArtifactCounter(nil), r.counters...)
}

func (s *processingArtifactPurgeCountingService) PurgeExpired(ctx context.Context, _ time.Time, batchSize int) (types.ProcessingArtifactPurgeResult, error) {
	s.calls.Add(1)
	deadline, deadlineSet := ctx.Deadline()
	s.mu.Lock()
	s.batchSize = batchSize
	s.deadline = deadline
	s.deadlineSet = deadlineSet
	s.mu.Unlock()
	return types.ProcessingArtifactPurgeResult{}, s.err
}

func TestProcessingArtifactRetentionRunnerDisabledAndStopIsIdempotent(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 0,
		batchSize:     1,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	runner.Start(context.Background())
	select {
	case <-runner.doneCh:
		t.Fatal("counter reporting must remain active when retention is disabled")
	default:
	}
	runner.Stop()
	runner.Stop()
	assert.Zero(t, svc.calls.Load())
}

func TestProcessingArtifactRetentionRunnerReportsSharedCountersWithRetentionEnabledAndDisabled(t *testing.T) {
	var output bytes.Buffer
	previousFormat, hadFormat := os.LookupEnv("LOG_FORMAT")
	t.Setenv("LOG_FORMAT", "%msg")
	logger.ConfigureFromEnv()
	logger.SetOutput(&output)
	logger.SetLogLevel(logger.LevelInfo)
	t.Cleanup(func() {
		if hadFormat {
			require.NoError(t, os.Setenv("LOG_FORMAT", previousFormat))
		} else {
			require.NoError(t, os.Unsetenv("LOG_FORMAT"))
		}
		logger.ConfigureFromEnv()
	})

	for _, retentionDays := range []int{30, 0} {
		t.Run(fmt.Sprintf("retention_days_%d", retentionDays), func(t *testing.T) {
			output.Reset()
			svc := &processingArtifactPurgeCountingService{}
			counters := &processingArtifactSnapshotRegistry{
				counters: []types.ProcessingArtifactCounter{
					{Stage: "chunking", Outcome: "hit", Count: 3},
				},
			}
			runner := NewProcessingArtifactRetentionRunner(
				&config.Config{ProcessingArtifact: &config.ProcessingArtifactConfig{
					RetentionDays:        retentionDays,
					CleanupIntervalHours: 24,
					CleanupBatchSize:     17,
				}},
				svc,
				counters,
			)

			runner.runOnce()

			assert.Equal(t, int64(1), counters.snapshotCalls.Load())
			if retentionDays == 0 {
				assert.Zero(t, svc.calls.Load())
			} else {
				assert.Equal(t, int64(1), svc.calls.Load())
			}
			var counterLine string
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "[processing-artifact-counter]") {
					counterLine = line
					break
				}
			}
			require.NotEmpty(t, counterLine)
			assert.Contains(t, counterLine, "stage=chunking")
			assert.Contains(t, counterLine, "outcome=hit")
			assert.Contains(t, counterLine, "count=3")
			for _, forbidden := range []string{
				"retention_days", "cleanup_batch_size", "scanned", "deleted", "failed", "deleted_bytes",
			} {
				assert.NotContains(t, counterLine, forbidden)
			}
		})
	}
}

func TestProcessingArtifactRetentionRunnerUsesConfiguredBatchAndTimeout(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     17,
		interval:      time.Hour,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	before := time.Now()
	runner.runOnce()
	assert.Equal(t, int64(1), svc.calls.Load())
	svc.mu.Lock()
	defer svc.mu.Unlock()
	assert.Equal(t, 17, svc.batchSize)
	assert.True(t, svc.deadlineSet)
	assert.WithinDuration(t, before.Add(30*time.Second), svc.deadline, time.Second)
}

func TestProcessingArtifactRetentionRunnerLogsOnlyClassifiedFailureKind(t *testing.T) {
	var output bytes.Buffer
	previousFormat, hadFormat := os.LookupEnv("LOG_FORMAT")
	t.Setenv("LOG_FORMAT", "%msg")
	logger.ConfigureFromEnv()
	logger.SetOutput(&output)
	logger.SetLogLevel(logger.LevelInfo)
	t.Cleanup(func() {
		if hadFormat {
			require.NoError(t, os.Setenv("LOG_FORMAT", previousFormat))
		} else {
			require.NoError(t, os.Unsetenv("LOG_FORMAT"))
		}
		logger.ConfigureFromEnv()
	})
	svc := &processingArtifactPurgeCountingService{
		err: classifiedProcessingArtifactPurgeError{
			kind:    "object_delete",
			message: "secret object path",
		},
	}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     1,
		interval:      time.Hour,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	runner.runOnce()

	assert.Contains(t, output.String(), "failure_kind=object_delete")
	assert.NotContains(t, output.String(), "secret object path")
}

func TestProcessingArtifactRetentionRunnerStartAndStopAreIdempotent(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     1,
		interval:      time.Hour,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	runner.Start(context.Background())
	runner.Start(context.Background())
	runner.Stop()
	runner.Stop()
	assert.Zero(t, svc.calls.Load())
}

func TestProcessingArtifactRetentionRunnerStopBeforeStartIsTerminal(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     1,
		interval:      time.Hour,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	runner.Stop()
	runner.Start(context.Background())
	select {
	case <-runner.doneCh:
	default:
		t.Fatal("Stop before Start must keep the runner terminal")
	}
	assert.Zero(t, svc.calls.Load())
}

func TestProcessingArtifactRetentionRunnerConcurrentStartStopIsTerminal(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     1,
		interval:      time.Hour,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			runner.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			<-start
			runner.Stop()
		}()
	}
	close(start)
	wg.Wait()
	select {
	case <-runner.doneCh:
	default:
		runner.Stop()
		t.Fatal("concurrent Stop must make the runner terminal")
	}
	runner.Start(context.Background())
	assert.Zero(t, svc.calls.Load())
}

func TestProcessingArtifactRetentionRunnerDisablesUnrepresentableDurations(t *testing.T) {
	for _, artifactConfig := range []*config.ProcessingArtifactConfig{
		{
			RetentionDays:        config.MaxProcessingArtifactRetentionDays + 1,
			CleanupIntervalHours: 24,
			CleanupBatchSize:     100,
		},
		{
			RetentionDays:        30,
			CleanupIntervalHours: config.MaxProcessingArtifactCleanupIntervalHours + 1,
			CleanupBatchSize:     100,
		},
	} {
		svc := &processingArtifactPurgeCountingService{}
		runner := NewProcessingArtifactRetentionRunner(
			&config.Config{ProcessingArtifact: artifactConfig},
			svc,
			&processingArtifactSnapshotRegistry{},
		)
		runner.runOnce()
		assert.Zero(t, svc.calls.Load())
		assert.Equal(t, 24*time.Hour, runner.interval)
	}
}

func TestProcessingArtifactRetentionRunnerContinuesOnCadenceAfterError(t *testing.T) {
	svc := &processingArtifactPurgeCountingService{err: errors.New("purge failed")}
	runner := &ProcessingArtifactRetentionRunner{
		service:       svc,
		retentionDays: 30,
		batchSize:     1,
		interval:      20 * time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	runner.mu.Lock()
	runner.started = true
	runner.mu.Unlock()
	go runner.runLoopWithoutStartupDelay()
	time.Sleep(70 * time.Millisecond)
	runner.Stop()
	assert.GreaterOrEqual(t, svc.calls.Load(), int64(2))
}

func (r *ProcessingArtifactRetentionRunner) runLoopWithoutStartupDelay() {
	defer close(r.doneCh)
	r.runOnce()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runOnce()
		case <-r.stopCh:
			return
		}
	}
}

func TestProcessingArtifactRetentionRunnerProductionStartupDelay(t *testing.T) {
	assert.Equal(t, 10*time.Minute, processingArtifactRetentionStartupDelay)
}
