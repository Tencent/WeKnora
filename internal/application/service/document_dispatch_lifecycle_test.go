package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type documentShutdownTenant struct {
	interfaces.TenantRepository
	entered chan struct{}
	release chan struct{}
}

func (r *documentShutdownTenant) GetTenantByID(ctx context.Context, _ uint64) (*types.Tenant, error) {
	close(r.entered)
	if r.release != nil {
		<-r.release
	} else {
		<-ctx.Done()
	}
	return nil, ctx.Err()
}

func TestDocumentDispatchShutdownCancelsAndWaits(t *testing.T) {
	s, r, q, _ := documentTestService()
	tenant := &documentShutdownTenant{entered: make(chan struct{})}
	s.tenantRepo = tenant
	done := make(chan error, 1)
	go func() { done <- s.ProcessDocumentDispatch(context.Background(), documentTestToken()) }()
	<-tenant.entered
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, s.StopDocumentDispatches(stopCtx))
	require.NoError(t, <-done)
	require.Equal(t, []string{"retry"}, r.outcomes)
	require.Empty(t, q.tasks, "shutdown must not create a replacement wakeup")
	require.NoError(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
	require.Equal(t, []string{"retry"}, r.outcomes, "stopped service must not claim again")
	require.NoError(t, s.StopDocumentDispatches(stopCtx))
}

func TestDocumentDispatchShutdownRespectsDeadline(t *testing.T) {
	s, _, _, _ := documentTestService()
	tenant := &documentShutdownTenant{entered: make(chan struct{}), release: make(chan struct{})}
	s.tenantRepo = tenant
	done := make(chan error, 1)
	go func() { done <- s.ProcessDocumentDispatch(context.Background(), documentTestToken()) }()
	<-tenant.entered
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := s.StopDocumentDispatches(stopCtx)
	close(tenant.release)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, <-done)
	require.NoError(t, s.StopDocumentDispatches(context.Background()))
}
