package router

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/stretchr/testify/require"
)

// TestAsynqRetryDelayFuncVectorStoreNotReady locks the patient backoff applied to
// a document task that stopped because its vector store was not reachable yet.
// The default exponential schedule (≈10s/40s/90s) can exhaust the retry budget
// before a slow store is up, so the delay must stay fixed and long.
func TestAsynqRetryDelayFuncVectorStoreNotReady(t *testing.T) {
	require.Equal(t, 60*time.Second, vectorStoreNotReadyRetryDelay)

	err := fmt.Errorf("vector store not ready: dial tcp: connection refused: %w",
		service.ErrVectorStoreNotReady)

	require.Equal(t, vectorStoreNotReadyRetryDelay, asynqRetryDelayFunc(0, err, nil))
}

// TestAsynqRetryDelayFuncWikiLockUnchanged guards against the shared policy
// function accidentally losing the pre-existing wiki lock branch.
func TestAsynqRetryDelayFuncWikiLockUnchanged(t *testing.T) {
	err := fmt.Errorf("concurrent wiki task active: %w", service.ErrWikiIngestConcurrent)

	require.Equal(t, wikiIngestRetryDelay, asynqRetryDelayFunc(0, err, nil))
}
