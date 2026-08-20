package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTenantSkillServiceWithConfigLockLocalRespectsCanceledContext(t *testing.T) {
	svc := NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- svc.withConfigLock(context.Background(), "config-1", func(context.Context) error {
			close(entered)
			<-releaseHolder
			return nil
		})
	}()

	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- svc.withConfigLock(ctx, "config-1", func(context.Context) error {
			return errors.New("canceled waiter entered lock")
		})
	}()

	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		close(releaseHolder)
		require.Fail(t, "canceled lock waiter did not return while another holder still held the local lock")
	}

	close(releaseHolder)
	require.NoError(t, <-holderDone)
}
