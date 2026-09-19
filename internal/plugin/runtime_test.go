package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerUsesCommonLifecycleForRuntime(t *testing.T) {
	var starts, stops atomic.Int32
	runtime := &BuiltinRuntime{StartFunc: func(context.Context) error { starts.Add(1); return nil }, StopFunc: func(context.Context) error { stops.Add(1); return nil }}
	manager := NewManager("1.2.0")
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, runtime))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	require.Equal(t, int32(1), starts.Load())
	health, err := manager.Health(context.Background(), manifest.ID)
	require.NoError(t, err)
	require.Equal(t, StateRunning, health.State)
	require.NoError(t, manager.Stop(context.Background(), manifest.ID))
	require.Equal(t, int32(1), stops.Load())
}

func TestExternalRuntimeConstructorsCaptureAuditByDefault(t *testing.T) {
	manifest := validManifest()
	process := NewProcessRuntime(manifest, "plugin")
	docker := NewDockerRuntime(manifest, "example/plugin:dev")
	require.NotNil(t, process.AuditSink)
	require.NotNil(t, docker.AuditSink)
}

func TestManagerAdmissionRejectsOverload(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 1, MaxQueued: 0, QueueTimeout: time.Second, DrainTimeout: time.Second})
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	lease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	defer lease.Close()
	_, err = manager.AcquireInvocation(context.Background(), manifest.ID)
	require.ErrorIs(t, err, ErrPluginOverloaded)
	status, ok := manager.Admission(manifest.ID)
	require.True(t, ok)
	require.Equal(t, uint64(1), status.Admitted)
	require.Equal(t, uint64(1), status.Rejected)
}

func TestManagerAdmissionQueueCancellationRestoresWaitingCount(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 1, MaxQueued: 1, QueueTimeout: time.Second, DrainTimeout: time.Second})
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	lease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := manager.AcquireInvocation(ctx, manifest.ID); done <- err }()
	require.Eventually(t, func() bool { status, _ := manager.Admission(manifest.ID); return status.Waiting == 1 }, time.Second, 5*time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	status, _ := manager.Admission(manifest.ID)
	require.Zero(t, status.Waiting)
	lease.Close()
}

func TestManagerAdmissionIsIsolatedPerPlugin(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 1, MaxQueued: 0, QueueTimeout: time.Second, DrainTimeout: time.Second})
	first := validManifest()
	second := validManifest()
	second.ID = "example.second"
	second.Name = "Second"
	require.NoError(t, manager.Register(first, &BuiltinRuntime{}))
	require.NoError(t, manager.Register(second, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), first.ID))
	require.NoError(t, manager.Start(context.Background(), second.ID))
	leaseFirst, err := manager.AcquireInvocation(context.Background(), first.ID)
	require.NoError(t, err)
	defer leaseFirst.Close()
	leaseSecond, err := manager.AcquireInvocation(context.Background(), second.ID)
	require.NoError(t, err)
	leaseSecond.Close()
}

func TestHealthBypassesBusinessAdmissionQuota(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 1, MaxQueued: 0, QueueTimeout: time.Second, DrainTimeout: time.Second})
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	lease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	defer lease.Close()
	health, err := manager.Health(context.Background(), manifest.ID)
	require.NoError(t, err)
	require.Equal(t, StateRunning, health.State)
}

func TestManagerDrainWaitsForLeaseAndFencesGeneration(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 2, MaxQueued: 2, QueueTimeout: time.Second, DrainTimeout: time.Second})
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	oldLease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	oldGeneration := oldLease.Generation

	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop(context.Background(), manifest.ID) }()
	require.Eventually(t, func() bool {
		status, ok := manager.Admission(manifest.ID)
		return ok && status.Draining
	}, time.Second, 5*time.Millisecond)
	_, err = manager.AcquireInvocation(context.Background(), manifest.ID)
	require.True(t, errors.Is(err, ErrPluginDraining) || err != nil)
	oldLease.Close()
	require.NoError(t, <-stopped)
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	newLease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	newGeneration := newLease.Generation
	newLease.Close()
	require.Greater(t, newGeneration, oldGeneration)
	require.False(t, manager.GenerationValid(manifest.ID, oldGeneration))
}

func TestManagerDrainTimeoutCancelsActiveInvocation(t *testing.T) {
	manager := NewManagerWithAdmission("1.2.0", AdmissionConfig{MaxConcurrent: 1, MaxQueued: 0, QueueTimeout: time.Second, DrainTimeout: 20 * time.Millisecond})
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	lease, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.NoError(t, err)
	cause := make(chan error, 1)
	go func() {
		<-lease.Context.Done()
		cause <- context.Cause(lease.Context)
		lease.Close()
	}()
	require.NoError(t, manager.Stop(context.Background(), manifest.ID))
	require.ErrorIs(t, <-cause, ErrPluginDrainTimeout)
}

func TestConcurrentStopsRunOneLifecycleOperation(t *testing.T) {
	var stops atomic.Int32
	manager := NewManager("1.2.0")
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, &BuiltinRuntime{StopFunc: func(context.Context) error { stops.Add(1); return nil }}))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	results := make(chan error, 2)
	go func() { results <- manager.Stop(context.Background(), manifest.ID) }()
	go func() { results <- manager.Stop(context.Background(), manifest.ID) }()
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, int32(1), stops.Load())
}

func TestRestartFailureKeepsPluginDraining(t *testing.T) {
	var starts atomic.Int32
	runtime := &BuiltinRuntime{StartFunc: func(context.Context) error {
		if starts.Add(1) == 1 {
			return nil
		}
		return errors.New("restart failed")
	}}
	manager := NewManager("1.2.0")
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, runtime))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	info, _ := manager.Get(manifest.ID)
	manager.restartOne(context.Background(), manifest.ID, runtime, info.State.Generation)
	status, ok := manager.Admission(manifest.ID)
	require.True(t, ok)
	require.True(t, status.Draining)
	info, _ = manager.Get(manifest.ID)
	require.Equal(t, StateFailed, info.State.State)
	_, err := manager.AcquireInvocation(context.Background(), manifest.ID)
	require.Error(t, err)
}

func TestManagerHealthSupervisorMarksRepeatedFailures(t *testing.T) {
	runtime := &BuiltinRuntime{HealthFunc: func(context.Context) HealthStatus {
		return HealthStatus{State: StateUnhealthy, Message: "probe failed"}
	}}
	manager := NewManager("1.2.0")
	manifest := validManifest()
	require.NoError(t, manager.Register(manifest, runtime))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, manager.StartHealthSupervisor(ctx, HealthSupervisorConfig{Interval: 5 * time.Millisecond, FailureThreshold: 1}))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		info, ok := manager.Get(manifest.ID)
		if ok && info.State.State == StateUnhealthy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("plugin never became unhealthy: %#v", manager.List())
}
