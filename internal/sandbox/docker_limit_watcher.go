package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Docker can enforce a memory ceiling and a CPU rate in HostConfig, but it
// has no cumulative CPU-time or hard-lifetime policy. The watcher closes that
// gap and upgrades an exec-level OOM into termination of the persistent
// session container.
const (
	dockerCPUTimeLimitLabel = "com.weknora.sandbox.cpu-time-limit-seconds"
	dockerHardLifetimeLabel = "com.weknora.sandbox.hard-lifetime-seconds"

	dockerLimitReasonMemoryOOM = "memory_oom"
	dockerLimitReasonCPUTime   = "cpu_time"
	dockerLimitReasonWallTime  = "wall_time"

	dockerLimitWatchInterval = 5 * time.Second
)

func durationSecondsLabel(value time.Duration) string {
	if value <= 0 {
		return "0"
	}
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	return strconv.FormatInt(seconds, 10)
}

func durationLabel(labels map[string]string, key string) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(labels[key]), 10, 64)
	if err != nil || seconds <= 0 || seconds > int64(math.MaxInt64/time.Second) {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

type dockerLimitTermination struct {
	ContainerID string
	Reason      string
	Observed    string
	Limit       string
}

type dockerLimitWatch struct {
	cancel context.CancelFunc
}

// dockerLimitWatcherRegistry deduplicates watchers across the short-lived
// managers built for each request. The daemon endpoint is part of the key, so
// identical container IDs from different daemons cannot collide.
type dockerLimitWatcherRegistry struct {
	mu       sync.Mutex
	watches  map[string]*dockerLimitWatch
	interval time.Duration
	now      func() time.Time

	// onTermination is an observation seam for tests. Production evidence is
	// the structured log emitted after the Engine confirms removal.
	onTermination func(dockerLimitTermination)
}

var sharedDockerLimitWatchers = newDockerLimitWatcherRegistry(dockerLimitWatchInterval)

func newDockerLimitWatcherRegistry(interval time.Duration) *dockerLimitWatcherRegistry {
	if interval <= 0 {
		interval = dockerLimitWatchInterval
	}
	return &dockerLimitWatcherRegistry{
		watches:  make(map[string]*dockerLimitWatch),
		interval: interval,
		now:      time.Now,
	}
}

func (c *DockerRemoteClient) watchLimits(containerID string) {
	if c == nil || c.limitWatchers == nil || strings.TrimSpace(containerID) == "" {
		return
	}
	c.limitWatchers.start(c, containerID)
}

func (r *dockerLimitWatcherRegistry) key(c *DockerRemoteClient, containerID string) string {
	return c.settings.Endpoint.key() + "|" + containerID
}

func (r *dockerLimitWatcherRegistry) start(c *DockerRemoteClient, containerID string) {
	key := r.key(c, containerID)
	r.mu.Lock()
	if _, exists := r.watches[key]; exists {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	watch := &dockerLimitWatch{cancel: cancel}
	r.watches[key] = watch
	r.mu.Unlock()

	go func() {
		defer r.release(key, watch)
		c.runLimitWatcher(ctx, containerID, r)
	}()
}

func (r *dockerLimitWatcherRegistry) stop(c *DockerRemoteClient, containerID string) {
	key := r.key(c, containerID)
	r.mu.Lock()
	watch := r.watches[key]
	if watch != nil {
		delete(r.watches, key)
	}
	r.mu.Unlock()
	if watch != nil {
		watch.cancel()
	}
}

func (r *dockerLimitWatcherRegistry) release(key string, watch *dockerLimitWatch) {
	r.mu.Lock()
	if r.watches[key] == watch {
		delete(r.watches, key)
	}
	r.mu.Unlock()
}

func (c *DockerRemoteClient) runLimitWatcher(
	ctx context.Context,
	containerID string,
	registry *dockerLimitWatcherRegistry,
) {
	ticker := time.NewTicker(registry.interval)
	defer ticker.Stop()
	for {
		stop, err := c.checkDockerLimits(ctx, containerID, registry)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[sandbox] docker limit watcher: container %s: %v", containerID, err)
		}
		if stop || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkDockerLimits performs one bounded sample. Its bool result means the
// watcher is finished because the container disappeared, became terminal, or
// was removed for a limit breach.
func (c *DockerRemoteClient) checkDockerLimits(
	ctx context.Context,
	containerID string,
	registry *dockerLimitWatcherRegistry,
) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, c.limitCheckTimeout())
	defer cancel()

	inspected, err := c.api.ContainerInspect(
		checkCtx, containerID, client.ContainerInspectOptions{},
	)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return true, nil
		}
		return false, dockerError("EnforceLimits", err)
	}
	state := inspected.Container.State
	if state == nil {
		return false, errors.New("docker limit watcher: daemon returned no container state")
	}
	labels := map[string]string(nil)
	if inspected.Container.Config != nil {
		labels = inspected.Container.Config.Labels
	}

	if state.OOMKilled {
		memoryLimit := c.settings.MemoryBytes
		if inspected.Container.HostConfig != nil && inspected.Container.HostConfig.Memory > 0 {
			memoryLimit = inspected.Container.HostConfig.Memory
		}
		return c.terminateForLimit(checkCtx, registry, dockerLimitTermination{
			ContainerID: containerID,
			Reason:      dockerLimitReasonMemoryOOM,
			Observed:    "oom_killed=true",
			Limit:       strconv.FormatInt(memoryLimit, 10) + " bytes",
		})
	}

	hardLifetime := durationLabel(labels, dockerHardLifetimeLabel)
	if hardLifetime > 0 {
		created, parseErr := time.Parse(time.RFC3339Nano, inspected.Container.Created)
		if parseErr != nil {
			return false, fmt.Errorf("parse container creation time: %w", parseErr)
		}
		age := registry.now().UTC().Sub(created.UTC())
		if age >= hardLifetime {
			return c.terminateForLimit(checkCtx, registry, dockerLimitTermination{
				ContainerID: containerID,
				Reason:      dockerLimitReasonWallTime,
				Observed:    age.String(),
				Limit:       hardLifetime.String(),
			})
		}
	}

	if dockerStateOf(state.Status) == RemoteStateTerminal {
		return true, nil
	}
	if !state.Running {
		// CPU and OOM counters cannot change while stopped. A later Connect
		// installs a fresh watcher after resuming the same container. Keep this
		// one only when an activity-independent wall deadline still has work to
		// do; otherwise a manually stopped sandbox would retain a goroutine
		// forever.
		return hardLifetime <= 0, nil
	}

	cpuLimit := durationLabel(labels, dockerCPUTimeLimitLabel)
	if cpuLimit <= 0 {
		return false, nil
	}
	cpuUsed, err := c.readContainerCPUTime(checkCtx, containerID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	if cpuUsed >= cpuLimit {
		return c.terminateForLimit(checkCtx, registry, dockerLimitTermination{
			ContainerID: containerID,
			Reason:      dockerLimitReasonCPUTime,
			Observed:    cpuUsed.String(),
			Limit:       cpuLimit.String(),
		})
	}
	return false, nil
}

func (c *DockerRemoteClient) limitCheckTimeout() time.Duration {
	if c.settings.HTTPTimeout > 0 && c.settings.HTTPTimeout < DefaultDockerHTTPTimeout {
		return c.settings.HTTPTimeout
	}
	return DefaultDockerHTTPTimeout
}

func (c *DockerRemoteClient) readContainerCPUTime(
	ctx context.Context,
	containerID string,
) (time.Duration, error) {
	result, err := c.api.ContainerStats(ctx, containerID, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: false,
	})
	if err != nil {
		return 0, dockerError("EnforceLimits", err)
	}
	if result.Body == nil {
		return 0, dockerError("EnforceLimits", errors.New("daemon returned no stats body"))
	}
	defer func() {
		if closeErr := result.Body.Close(); closeErr != nil {
			log.Printf(
				"[sandbox] docker limit watcher: close stats body for container %s: %v",
				containerID, closeErr,
			)
		}
	}()

	var stats container.StatsResponse
	if err := json.NewDecoder(result.Body).Decode(&stats); err != nil {
		return 0, dockerError("EnforceLimits", fmt.Errorf("decode container stats: %w", err))
	}
	units := stats.CPUStats.CPUUsage.TotalUsage
	if strings.EqualFold(stats.OSType, "windows") {
		if units > math.MaxUint64/100 {
			return time.Duration(math.MaxInt64), nil
		}
		units *= 100
	}
	if units > math.MaxInt64 {
		return time.Duration(math.MaxInt64), nil
	}
	return time.Duration(units), nil
}

func (c *DockerRemoteClient) terminateForLimit(
	ctx context.Context,
	registry *dockerLimitWatcherRegistry,
	event dockerLimitTermination,
) (bool, error) {
	removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.limitCheckTimeout())
	defer cancel()
	_, err := c.api.ContainerRemove(removeCtx, event.ContainerID, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	if cerrdefs.IsNotFound(err) {
		// Another lifecycle path won the deletion race. The limit was breached,
		// but this watcher did not perform or confirm the termination, so it
		// must not emit a misleading enforcement event.
		return true, nil
	}
	if err != nil {
		return false, dockerError("EnforceLimits", err)
	}
	log.Printf(
		"[sandbox] docker hard limit terminated container=%s reason=%s observed=%s limit=%s",
		event.ContainerID, event.Reason, event.Observed, event.Limit,
	)
	if registry.onTermination != nil {
		registry.onTermination(event)
	}
	return true, nil
}
