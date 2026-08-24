//go:build docker_integration

// Conformance test for the Docker backend against a real daemon.
//
// It drives the same public surface as the E2B-protocol conformance suite
// (session-scoped script execution, shell commands, attachment staging,
// artifact listing, timeouts, teardown), because the point of the docker
// backend is to be indistinguishable from a remote one at that level. Anything
// asserted here that the E2B suite also asserts is deliberate duplication: a
// backend that passes one and fails the other is not interchangeable.
//
// Run with a reachable daemon and the standard sandbox image:
//
//	docker build -f docker/Dockerfile.sandbox -t wechatopenai/weknora-sandbox:dev .
//	DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev \
//	go test -tags=docker_integration ./internal/sandbox \
//	  -run '^TestDocker.*Integration' -count=1 -v -timeout=15m
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Tencent/WeKnora/internal/types"
)

const dockerIntegrationTenantID = 1

func dockerIntegrationConfig(t *testing.T) *Config {
	t.Helper()
	image := strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_IMAGE"))
	if image == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE is required for the docker conformance suite")
	}
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeDocker
	cfg.FallbackEnabled = false
	cfg.DockerImage = image
	cfg.DockerHost = strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_HOST"))
	cfg.DefaultTimeout = 2 * time.Minute
	applyDockerRuntimeDefaults(cfg)
	return cfg
}

func newDockerIntegrationManager(t *testing.T, cfg *Config) *SessionBoundManager {
	t.Helper()
	client, err := NewDockerRemoteClient(cfg)
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Health(probeCtx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	manager, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         PermissiveSessionExistenceChecker{},
		ConfigID:        "docker-conformance",
		SkipHealthProbe: true,
	})
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	return manager
}

func TestDockerBackendConformanceIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		12*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-conformance-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			2*time.Minute,
		)
		defer cleanupCancel()
		if err := manager.DestroySession(cleanupCtx, sessionID); err != nil {
			t.Errorf("DestroySession: %v", err)
		}
	})

	counterPath := path.Join(SessionOutputRoot, "counter.txt")
	t.Run("SessionScopedStatePersistsAcrossExecutions", func(t *testing.T) {
		first := runDockerScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q, 'w') as handle:
    handle.write('1')
print('wrote counter')
`, counterPath))
		if !first.IsSuccess() {
			t.Fatalf("first execution failed: %#v", first)
		}
		second := runDockerScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q) as handle:
    print('counter=' + handle.read())
`, counterPath))
		if !second.IsSuccess() || !strings.Contains(second.Stdout, "counter=1") {
			t.Fatalf("session state did not persist: %#v", second)
		}
	})

	t.Run("InstalledPackagesSurviveBetweenExecutions", func(t *testing.T) {
		install := runDockerScript(t, ctx, manager, sessionID, `
import subprocess
print(subprocess.run(
    ['pip', 'install', '--quiet', '--no-cache-dir', '--user', 'cowsay==6.1'],
    capture_output=True, text=True).returncode)
`)
		if !install.IsSuccess() {
			t.Fatalf("package install failed: %#v", install)
		}
		use := runDockerScript(t, ctx, manager, sessionID, `
import cowsay
print('cowsay-ok')
`)
		if !use.IsSuccess() || !strings.Contains(use.Stdout, "cowsay-ok") {
			t.Fatalf("installed package did not survive: %#v", use)
		}
	})

	t.Run("ShellExecSharesTheSessionSandbox", func(t *testing.T) {
		executor := manager.SessionShellExecutor()
		if executor == nil {
			t.Fatal("session shell executor is unavailable on a healthy docker backend")
		}
		result, err := executor.ExecShellCommand(
			ctx, sessionID, "cat "+counterPath, SessionWorkspaceRoot, time.Minute, nil,
		)
		if err != nil {
			t.Fatalf("ExecShellCommand: %v", err)
		}
		if !result.IsSuccess() || !strings.Contains(result.Stdout, "1") {
			t.Fatalf("shell command did not observe the session sandbox: %#v", result)
		}
	})

	t.Run("AttachmentStagingAndArtifactCollection", func(t *testing.T) {
		files := manager.SessionFileStore()
		if files == nil {
			t.Fatal("session file store is unavailable on a healthy docker backend")
		}
		inputPath := path.Join(SessionInputRoot, "attachment.txt")
		payload := []byte("attachment payload\n")
		if err := files.WriteSessionInputFile(ctx, sessionID, inputPath, payload); err != nil {
			t.Fatalf("WriteSessionInputFile: %v", err)
		}
		content, err := files.ReadSessionFile(ctx, sessionID, inputPath)
		if err != nil {
			t.Fatalf("ReadSessionFile: %v", err)
		}
		if !bytes.Equal(content, payload) {
			t.Fatalf("staged attachment mismatch: got=%q want=%q", content, payload)
		}

		result := runDockerScript(t, ctx, manager, sessionID, fmt.Sprintf(`
import os
target = os.path.join(os.environ['%s'], 'report.txt')
with open(target, 'w') as handle:
    handle.write('artifact body')
print('artifact written')
`, skillOutputEnvVar))
		if !result.IsSuccess() {
			t.Fatalf("artifact-producing execution failed: %#v", result)
		}

		entries, err := files.ListSessionFiles(ctx, sessionID, SessionOutputRoot)
		if err != nil {
			t.Fatalf("ListSessionFiles: %v", err)
		}
		found := false
		for _, entry := range entries {
			if entry.Name == "report.txt" {
				found = true
			}
		}
		if !found {
			t.Fatalf("artifact directory did not contain report.txt: %#v", entries)
		}
		if err := files.RemoveSessionInputPath(ctx, sessionID, inputPath); err != nil {
			t.Fatalf("RemoveSessionInputPath: %v", err)
		}
	})

	t.Run("TimeoutIsReportedAsKilled", func(t *testing.T) {
		result := runDockerScriptWithTimeout(t, ctx, manager, sessionID, `
import time
time.sleep(60)
`, 5*time.Second)
		if !result.Killed {
			t.Fatalf("expected a killed result for an over-running script: %#v", result)
		}
	})

	// The daemon does not stop a process when the client goes away, so the
	// only proof that a timeout means anything is that nothing is left running.
	t.Run("TimeoutActuallyStopsTheProcess", func(t *testing.T) {
		executor := manager.SessionShellExecutor()
		_, err := executor.ExecShellCommand(
			ctx, sessionID, "sleep 120", SessionWorkspaceRoot, 3*time.Second, nil,
		)
		if err != nil {
			t.Fatalf("ExecShellCommand: %v", err)
		}
		time.Sleep(2 * time.Second)
		// Count processes whose command line is exactly "sleep 120", which the
		// probe's own shell cannot match.
		const probe = `n=0; for p in /proc/[0-9]*; do ` +
			`cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); ` +
			`case "$cmd" in "sleep 120 ") n=$((n+1));; esac; done; echo "$n"`
		survivors, err := executor.ExecShellCommand(
			ctx, sessionID, probe, SessionWorkspaceRoot, 30*time.Second, nil,
		)
		if err != nil {
			t.Fatalf("survivor probe: %v", err)
		}
		if got := strings.TrimSpace(survivors.Stdout); got != "0" {
			t.Fatalf("timeout left %s 'sleep 120' process(es) running: %#v", got, survivors)
		}
	})

	// The entrypoint is a plain `sleep`, which never calls wait(). A background
	// process that outlives the exec which started it is reparented to PID 1,
	// so without tini in front of it every such process becomes a permanent Z
	// entry and a long session eventually exhausts PidsLimit. Measured on a
	// real daemon: three orphans leave three zombies without HostConfig.Init
	// and none with it.
	t.Run("OrphanedProcessesAreReaped", func(t *testing.T) {
		executor := manager.SessionShellExecutor()
		for i := 0; i < 3; i++ {
			if _, err := executor.ExecShellCommand(
				ctx, sessionID, "(sleep 0.2 &) ; exit 0",
				SessionWorkspaceRoot, 30*time.Second, nil,
			); err != nil {
				t.Fatalf("spawn orphan %d: %v", i, err)
			}
		}
		time.Sleep(2 * time.Second)

		const probe = `n=0; for p in /proc/[0-9]*; do ` +
			`s=$(awk '{print $3}' "$p/stat" 2>/dev/null); ` +
			`[ "$s" = "Z" ] && n=$((n+1)); done; echo "$n"`
		zombies, err := executor.ExecShellCommand(
			ctx, sessionID, probe, SessionWorkspaceRoot, 30*time.Second, nil,
		)
		if err != nil {
			t.Fatalf("zombie probe: %v", err)
		}
		if got := strings.TrimSpace(zombies.Stdout); got != "0" {
			t.Fatalf("PID 1 left %s zombie(s) unreaped: %#v", got, zombies)
		}
	})
}

// The idle sweeper reclaims a container by the mtime of its activity marker,
// and skill scripts run as the unprivileged sandbox user. A session that only
// ever runs scripts must still count as active, or it gets deleted underneath
// the user who is actively using it.
func TestDockerBackendScriptExecutionRefreshesActivityMarkerIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		5*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-activity-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			time.Minute,
		)
		defer cleanupCancel()
		_ = manager.DestroySession(cleanupCtx, sessionID)
	})

	first := runDockerScript(t, ctx, manager, sessionID, `print('one')`)
	if !first.IsSuccess() {
		t.Fatalf("first execution failed: %#v", first)
	}
	client, err := NewDockerRemoteClient(cfg)
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	summaries, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{remoteMetadataSessionID: sessionID},
	})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("expected exactly one container: %v %#v", err, summaries)
	}
	handle, err := client.Connect(ctx, summaries[0].ID)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// The container entrypoint makes the marker world-writable on purpose.
	// Leaving it to whatever umask the daemon happens to run with would work
	// on a host with umask 000 and silently stop tracking script executions on
	// a host with umask 022.
	if mode := dockerActivityMarkerStat(t, ctx, summaries[0].ID).Mode.Perm(); mode != 0o666 {
		t.Fatalf("activity marker is %v, want 0666 so both root and %s can refresh it",
			mode, DefaultSandboxExecUser)
	}
	before := dockerActivityMarkerMTime(t, ctx, summaries[0].ID)

	// Exec directly as the unprivileged account, with no root-run step in
	// between: WeKnora happens to prepare the artifact directory as root
	// before each script today, and leaning on that would let the marker
	// silently stop tracking the account that actually runs user code.
	// The marker has one-second resolution on most filesystems.
	time.Sleep(2 * time.Second)
	result, err := client.Exec(ctx, handle, RemoteExecRequest{
		Command: "echo",
		Args:    []string{"as-sandbox-user"},
		User:    DefaultSandboxExecUser,
		Timeout: 30 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("exec as %s failed: %v %#v", DefaultSandboxExecUser, err, result)
	}
	after := dockerActivityMarkerMTime(t, ctx, summaries[0].ID)

	if !after.After(before) {
		t.Fatalf("an exec as %s did not refresh the activity marker: before=%s after=%s",
			DefaultSandboxExecUser, before, after)
	}
}

func dockerActivityMarkerMTime(t *testing.T, ctx context.Context, containerID string) time.Time {
	t.Helper()
	return dockerActivityMarkerStat(t, ctx, containerID).Mtime
}

func dockerActivityMarkerStat(
	t *testing.T, ctx context.Context, containerID string,
) container.PathStat {
	t.Helper()
	api, err := sharedDockerEngineClients.get(dockerEndpoint{
		Host:    strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_HOST")),
		Timeout: DefaultDockerHTTPTimeout,
	})
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	stat, err := api.ContainerStatPath(ctx, containerID,
		client.ContainerStatPathOptions{Path: dockerActivityMarker})
	if err != nil {
		t.Fatalf("stat activity marker: %v", err)
	}
	return stat.Stat
}

// A session must survive the container being stopped underneath it: the
// filesystem is intact, so Connect restarts it rather than losing the state.
func TestDockerBackendResumesStoppedContainerIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		5*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-resume-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			time.Minute,
		)
		defer cleanupCancel()
		_ = manager.DestroySession(cleanupCtx, sessionID)
	})

	marker := path.Join(SessionOutputRoot, "resume.txt")
	first := runDockerScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q, 'w') as handle:
    handle.write('before-stop')
print('ok')
`, marker))
	if !first.IsSuccess() {
		t.Fatalf("seed execution failed: %#v", first)
	}

	client, err := NewDockerRemoteClient(cfg)
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	summaries, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{remoteMetadataSessionID: sessionID},
	})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("expected exactly one container for the session: %v %#v", err, summaries)
	}

	// Stop it the way a host reboot or an operator would.
	stopCtx, stopCancel := context.WithTimeout(ctx, time.Minute)
	defer stopCancel()
	if err := stopDockerContainerForTest(stopCtx, summaries[0].ID); err != nil {
		t.Fatalf("stop container: %v", err)
	}

	second := runDockerScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q) as handle:
    print('restored=' + handle.read())
`, marker))
	if !second.IsSuccess() || !strings.Contains(second.Stdout, "restored=before-stop") {
		t.Fatalf("session did not resume with its filesystem: %#v", second)
	}
}

// stopDockerContainerForTest stops a container behind the adapter's back, the
// way a host reboot or an operator with a shell would.
func stopDockerContainerForTest(ctx context.Context, id string) error {
	api, err := sharedDockerEngineClients.get(dockerEndpoint{
		Host:    strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_HOST")),
		Timeout: DefaultDockerHTTPTimeout,
	})
	if err != nil {
		return err
	}
	grace := 5
	_, err = api.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &grace})
	return err
}

func runDockerScript(
	t *testing.T,
	ctx context.Context,
	manager *SessionBoundManager,
	sessionID, source string,
) *ExecuteResult {
	t.Helper()
	return runDockerScriptWithTimeout(t, ctx, manager, sessionID, source, 3*time.Minute)
}

func runDockerScriptWithTimeout(
	t *testing.T,
	ctx context.Context,
	manager *SessionBoundManager,
	sessionID, source string,
	timeout time.Duration,
) *ExecuteResult {
	t.Helper()
	result, err := manager.Execute(ctx, &ExecuteConfig{
		Script:         "conformance.py",
		ScriptContent:  source,
		SessionID:      sessionID,
		Timeout:        timeout,
		SkipValidation: true,
		Env:            map[string]string{skillOutputEnvVar: SessionOutputRoot},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned no result")
	}
	t.Logf("execute exit=%d killed=%v stdout=%q stderr=%q err=%q",
		result.ExitCode, result.Killed, result.Stdout, result.Stderr, result.Error)
	return result
}
