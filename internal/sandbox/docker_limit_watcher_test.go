package sandbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
)

func dockerLimitTestContainer(
	id string,
	created time.Time,
	labels map[string]string,
) container.InspectResponse {
	return container.InspectResponse{
		ID:      id,
		Created: created.UTC().Format(time.RFC3339Nano),
		State:   &container.State{Status: "running", Running: true},
		Config:  &container.Config{Labels: labels},
	}
}

func newDockerLimitTestClient(
	t *testing.T,
	engine *fakeDockerEngine,
) (*DockerRemoteClient, *dockerLimitWatcherRegistry, *[]dockerLimitTermination) {
	t.Helper()
	client := newTestDockerClient(t, engine)
	client.settings.MemoryBytes = 64 * 1024 * 1024
	events := make([]dockerLimitTermination, 0, 1)
	registry := newDockerLimitWatcherRegistry(time.Hour)
	registry.onTermination = func(event dockerLimitTermination) {
		events = append(events, event)
	}
	return client, registry, &events
}

func TestDockerLimitWatcherTerminatesOOMKilledContainer(t *testing.T) {
	engine := newFakeDockerEngine()
	inspected := dockerLimitTestContainer("oom", time.Now(), nil)
	inspected.State.OOMKilled = true
	engine.inspect["oom"] = inspected
	client, registry, events := newDockerLimitTestClient(t, engine)

	stop, err := client.checkDockerLimits(context.Background(), "oom", registry)

	require.NoError(t, err)
	require.True(t, stop)
	require.Equal(t, []string{"oom"}, engine.removed)
	require.True(t, engine.removeOptions[0].Force)
	require.True(t, engine.removeOptions[0].RemoveVolumes)
	require.Len(t, *events, 1)
	require.Equal(t, dockerLimitReasonMemoryOOM, (*events)[0].Reason)
	require.Equal(t, "67108864 bytes", (*events)[0].Limit)
	require.Zero(t, engine.statsCalls, "OOM inspection must not need a stats stream")
}

func TestDockerCreateStampsHardLimitPolicyLabels(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.imagePresent["weknora/sandbox:test"] = true
	client := newTestDockerClient(t, engine)
	client.settings.CPUTimeLimit = 90 * time.Second
	client.settings.HardLifetime = time.Hour

	_, err := client.Create(context.Background(), RemoteCreateRequest{})

	require.NoError(t, err)
	require.Equal(t, "90", engine.created[0].Config.Labels[dockerCPUTimeLimitLabel])
	require.Equal(t, "3600", engine.created[0].Config.Labels[dockerHardLifetimeLabel])
}

func TestDockerLimitWatcherDoesNotClaimConcurrentDeletion(t *testing.T) {
	engine := newFakeDockerEngine()
	inspected := dockerLimitTestContainer("gone", time.Now(), nil)
	inspected.State.OOMKilled = true
	engine.inspect["gone"] = inspected
	engine.removeErr = cerrdefs.ErrNotFound.WithMessage("already removed")
	client, registry, events := newDockerLimitTestClient(t, engine)

	stop, err := client.checkDockerLimits(context.Background(), "gone", registry)

	require.NoError(t, err)
	require.True(t, stop)
	require.Empty(t, *events, "a losing delete race is not confirmed limit enforcement")
}

func TestDockerLimitWatcherNormalizesDaemonRemovalError(t *testing.T) {
	engine := newFakeDockerEngine()
	inspected := dockerLimitTestContainer("remove-error", time.Now(), nil)
	inspected.State.OOMKilled = true
	engine.inspect["remove-error"] = inspected
	engine.removeErr = errors.New("daemon unavailable")
	client, registry, events := newDockerLimitTestClient(t, engine)

	stop, err := client.checkDockerLimits(context.Background(), "remove-error", registry)

	require.False(t, stop, "the watcher must retry a failed destructive action")
	var remoteErr *RemoteError
	require.ErrorAs(t, err, &remoteErr)
	require.Equal(t, SandboxTypeDocker, remoteErr.Provider)
	require.Equal(t, "EnforceLimits", remoteErr.Op)
	require.Empty(t, *events)
}

func TestDockerLimitWatcherTerminatesAtHardLifetimeDespiteActivity(t *testing.T) {
	engine := newFakeDockerEngine()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	engine.inspect["wall"] = dockerLimitTestContainer("wall", now.Add(-31*time.Second), map[string]string{
		dockerHardLifetimeLabel: "30",
	})
	client, registry, events := newDockerLimitTestClient(t, engine)
	registry.now = func() time.Time { return now }

	stop, err := client.checkDockerLimits(context.Background(), "wall", registry)

	require.NoError(t, err)
	require.True(t, stop)
	require.Equal(t, []string{"wall"}, engine.removed)
	require.Len(t, *events, 1)
	require.Equal(t, dockerLimitReasonWallTime, (*events)[0].Reason)
	require.Equal(t, "30s", (*events)[0].Limit)
	require.Zero(t, engine.statsCalls)
}

func TestDockerLimitWatcherTerminatesAtCumulativeCPUTime(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.inspect["cpu"] = dockerLimitTestContainer("cpu", time.Now(), map[string]string{
		dockerCPUTimeLimitLabel: "2",
	})
	engine.stats = container.StatsResponse{OSType: "linux"}
	engine.stats.CPUStats.CPUUsage.TotalUsage = uint64((2 * time.Second) + time.Nanosecond)
	client, registry, events := newDockerLimitTestClient(t, engine)

	stop, err := client.checkDockerLimits(context.Background(), "cpu", registry)

	require.NoError(t, err)
	require.True(t, stop)
	require.Equal(t, []string{"cpu"}, engine.removed)
	require.Equal(t, 1, engine.statsCalls)
	require.Len(t, *events, 1)
	require.Equal(t, dockerLimitReasonCPUTime, (*events)[0].Reason)
	require.Equal(t, "2s", (*events)[0].Limit)
}

func TestDockerLimitWatcherLeavesContainerBelowLimits(t *testing.T) {
	engine := newFakeDockerEngine()
	now := time.Now().UTC()
	engine.inspect["safe"] = dockerLimitTestContainer("safe", now.Add(-10*time.Second), map[string]string{
		dockerCPUTimeLimitLabel: "10",
		dockerHardLifetimeLabel: "60",
	})
	engine.stats.CPUStats.CPUUsage.TotalUsage = uint64(time.Second)
	client, registry, events := newDockerLimitTestClient(t, engine)
	registry.now = func() time.Time { return now }

	stop, err := client.checkDockerLimits(context.Background(), "safe", registry)

	require.NoError(t, err)
	require.False(t, stop)
	require.Empty(t, engine.removed)
	require.Empty(t, *events)
	require.Equal(t, 1, engine.statsCalls)
}

func TestDockerLimitWatcherStopsForInactiveContainerWithoutWallDeadline(t *testing.T) {
	engine := newFakeDockerEngine()
	inspected := dockerLimitTestContainer("stopped", time.Now(), map[string]string{
		dockerCPUTimeLimitLabel: "10",
	})
	inspected.State.Status = "exited"
	inspected.State.Running = false
	engine.inspect["stopped"] = inspected
	client, registry, events := newDockerLimitTestClient(t, engine)

	stop, err := client.checkDockerLimits(context.Background(), "stopped", registry)

	require.NoError(t, err)
	require.True(t, stop)
	require.Zero(t, engine.statsCalls)
	require.Empty(t, engine.removed)
	require.Empty(t, *events)
}

func TestDockerLimitWatcherKeepsWallDeadlineForStoppedContainer(t *testing.T) {
	engine := newFakeDockerEngine()
	now := time.Now().UTC()
	inspected := dockerLimitTestContainer("paused", now, map[string]string{
		dockerHardLifetimeLabel: "60",
	})
	inspected.State.Status = "paused"
	inspected.State.Running = false
	engine.inspect["paused"] = inspected
	client, registry, _ := newDockerLimitTestClient(t, engine)
	registry.now = func() time.Time { return now.Add(10 * time.Second) }

	stop, err := client.checkDockerLimits(context.Background(), "paused", registry)

	require.NoError(t, err)
	require.False(t, stop, "hard lifetime remains active while the container is paused")
}

func TestDockerLimitWatcherRegistryDeduplicatesContainer(t *testing.T) {
	engine := newFakeDockerEngine()
	var inspectCalls atomic.Int32
	engine.inspectHook = func(id string) (container.InspectResponse, error) {
		inspectCalls.Add(1)
		return dockerLimitTestContainer(id, time.Now(), nil), nil
	}
	client := newTestDockerClient(t, engine)
	registry := newDockerLimitWatcherRegistry(time.Hour)
	client.limitWatchers = registry

	client.watchLimits("same")
	client.watchLimits("same")
	require.Eventually(t, func() bool { return inspectCalls.Load() == 1 }, time.Second, time.Millisecond)

	registry.mu.Lock()
	require.Len(t, registry.watches, 1)
	registry.mu.Unlock()
	registry.stop(client, "same")
	require.Eventually(t, func() bool {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		return len(registry.watches) == 0
	}, time.Second, time.Millisecond)
}

func TestDurationLabelRejectsInvalidAndOverflowingValues(t *testing.T) {
	require.Equal(t, "2", durationSecondsLabel(1500*time.Millisecond),
		"sub-second remainder must round up rather than terminate early")
	require.Zero(t, durationLabel(nil, dockerCPUTimeLimitLabel))
	require.Zero(t, durationLabel(map[string]string{dockerCPUTimeLimitLabel: "-1"}, dockerCPUTimeLimitLabel))
	require.Zero(t, durationLabel(
		map[string]string{dockerCPUTimeLimitLabel: "999999999999999999"},
		dockerCPUTimeLimitLabel,
	))
	require.Equal(t, 5*time.Second, durationLabel(
		map[string]string{dockerCPUTimeLimitLabel: "5"}, dockerCPUTimeLimitLabel,
	))
}
