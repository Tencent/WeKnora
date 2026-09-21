package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSkillProgressKeyIncludesTheTenant(t *testing.T) {
	require.Equal(t, "weknora-skill-install:7:cfg-1:sk-1", skillProgressKey(7, "cfg-1", "sk-1"))
	require.NotEqual(t, skillProgressKey(7, "cfg-1", "sk-1"), skillProgressKey(8, "cfg-1", "sk-1"),
		"two workspaces must not share a progress slot because they happened to reuse IDs")
}

func TestSkillImageLockKeyIncludesTheTenant(t *testing.T) {
	require.Equal(t, "weknora-skill-image-lock:7:cfg-1", skillImageLockKey(7, "cfg-1"))
	require.NotEqual(t, skillImageLockKey(7, "cfg-1"), skillImageLockKey(8, "cfg-1"),
		"two workspaces must not share an image lock because they happened to reuse config IDs")
}

func TestTenantSkillServiceWithConfigLockLocalRespectsCanceledContext(t *testing.T) {
	svc := NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- svc.withConfigLock(context.Background(), 7, "config-1", func(context.Context) error {
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
		waiterDone <- svc.withConfigLock(ctx, 7, "config-1", func(context.Context) error {
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
