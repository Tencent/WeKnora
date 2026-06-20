package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeProcessLeaseLiteSerializesByKnowledge(t *testing.T) {
	ctx := context.Background()
	svc := &knowledgeService{}

	leaseCtx, release, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.NoError(t, err)
	defer release()

	_, releaseBusy, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKnowledgeProcessLeaseBusy))
	releaseBusy()

	reentrantCtx, releaseReentrant, err := svc.acquireKnowledgeProcessLease(leaseCtx, 1, "kid-lease")
	require.NoError(t, err)
	releaseReentrant()
	assert.Equal(t, leaseCtx, reentrantCtx)

	release()
	_, releaseAgain, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.NoError(t, err)
	releaseAgain()
}

func TestKnowledgeProcessLeaseLiteScopesByKnowledge(t *testing.T) {
	ctx := context.Background()
	svc := &knowledgeService{}

	_, releaseA, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-a")
	require.NoError(t, err)
	defer releaseA()

	_, releaseB, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-b")
	require.NoError(t, err)
	releaseB()
}
