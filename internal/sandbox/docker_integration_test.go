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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
)

const dockerIntegrationTenantID = 1

func dockerIntegrationConfig(t *testing.T) *Config {
	t.Helper()
	image := integrationEnv(t, "DOCKER_INTEGRATION_IMAGE")
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeDocker
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
	// Integration tests clean up their own sessions. Do not reclaim unrelated
	// idle containers when using a shared developer daemon.
	client.sweeper = nil
	probeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Health(probeCtx); err != nil {
		if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("docker daemon unreachable: %v", err)
		}
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
	handle, err := client.Connect(ctx, RemoteConnectRequest{SandboxID: summaries[0].ID})
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

	// Exec directly as the unprivileged account: this asserts that the account
	// running user code refreshes the marker itself, rather than relying on
	// some other step happening to touch it first. The marker has one-second
	// resolution on most filesystems.
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

// A session owns /workspace, so it can replace its own artifact directory with
// a symlink pointing anywhere in the container. chown and chmod follow
// symlinks, so if the pre-execution bootstrap ran as root it would hand the
// session ownership of the link's target — /etc here, which is enough to
// rewrite passwd and give the sandbox account uid 0 on the next exec.
//
// Session-scoped containers are what make this reachable: the link planted by
// one execution is still there when the next one runs the bootstrap. Under the
// old one-shot `docker run --rm` the two never shared a filesystem.
func TestDockerBackendArtifactBootstrapDoesNotFollowSymlinkIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		5*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-chown-escape-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			time.Minute,
		)
		defer cleanupCancel()
		_ = manager.DestroySession(cleanupCtx, sessionID)
	})

	if first := runDockerScript(t, ctx, manager, sessionID, `print('seed')`); !first.IsSuccess() {
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
	handle, err := client.Connect(ctx, RemoteConnectRequest{SandboxID: summaries[0].ID})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ownerOfEtc := func() string {
		t.Helper()
		result, err := client.Exec(ctx, handle, RemoteExecRequest{
			Command: "stat",
			Args:    []string{"-c", "%U:%G", "/etc"},
			User:    DefaultSandboxExecUser,
			Timeout: 30 * time.Second,
		})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("stat /etc failed: %v %#v", err, result)
		}
		return strings.TrimSpace(result.Stdout)
	}

	before := ownerOfEtc()
	if before != "root:root" {
		t.Fatalf("/etc should start out root-owned, got %q", before)
	}

	// Exactly what a model with shell_exec can do: it owns /workspace.
	plant, err := client.Exec(ctx, handle, RemoteExecRequest{
		Shell: true,
		Command: fmt.Sprintf(
			"rm -rf %s && ln -s /etc %s", SessionOutputRoot, SessionOutputRoot,
		),
		User:    DefaultSandboxExecUser,
		Timeout: 30 * time.Second,
	})
	if err != nil || plant.ExitCode != 0 {
		t.Fatalf("planting the symlink failed: %v %#v", err, plant)
	}

	// Any further execution runs the bootstrap against the planted path.
	_, err = manager.Execute(ctx, &ExecuteConfig{
		Script: "conformance.py", ScriptContent: `print('after')`,
		SessionID: sessionID, Timeout: time.Minute, SkipValidation: true,
		Env: map[string]string{skillOutputEnvVar: SessionOutputRoot},
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("bootstrap must preserve and report the symlink before execution: %v", err)
	}

	if after := ownerOfEtc(); after != "root:root" {
		t.Fatalf("the artifact bootstrap chowned through the symlink: /etc is now %q, want root:root",
			after)
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

func TestDockerSkillSnapshotIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	client, err := NewDockerRemoteClient(cfg)
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProbe()
	if err := client.Health(probeCtx); err != nil {
		if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("docker daemon unreachable: %v", err)
		}
		t.Skipf("docker daemon unreachable: %v", err)
	}
	if !client.Capabilities().SupportsSnapshots {
		t.Fatal("docker backend must advertise snapshot support")
	}

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		5*time.Minute,
	)
	defer cancel()

	builder, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: cfg.DockerImage,
		Metadata:   map[string]string{"weknora.test": "skill-snapshot"},
	})
	if err != nil {
		t.Fatalf("Create builder: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = client.Delete(cleanupCtx, builder.ID())
	})

	skillDir := SkillsImageRoot + "/itest"
	skillFile := skillDir + "/SKILL.md"
	seed, err := client.Exec(ctx, builder, RemoteExecRequest{
		Command: fmt.Sprintf(
			"mkdir -p %s && echo skill-ok > %s && chown %s:%s %s %s",
			skillDir, skillFile, DefaultSandboxExecUser, DefaultSandboxExecUser,
			SkillsImageRoot, skillDir,
		),
		Shell:   true,
		User:    "root",
		Timeout: time.Minute,
	})
	if err != nil || seed == nil || seed.ExitCode != 0 {
		t.Fatalf("seed skill tree: err=%v result=%#v", err, seed)
	}

	ref, err := client.CreateSnapshot(ctx, builder.ID(), "weknora-sk-itest-g1")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if ref.ID == "" {
		t.Fatal("CreateSnapshot returned an empty id")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = client.DeleteSnapshot(cleanupCtx, ref.ID)
	})

	if err := client.Delete(ctx, builder.ID()); err != nil {
		t.Fatalf("Delete builder: %v", err)
	}

	booted, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: ref.ID,
		Metadata:   map[string]string{"weknora.test": "skill-snapshot-boot"},
	})
	if err != nil {
		t.Fatalf("Create from snapshot: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = client.Delete(cleanupCtx, booted.ID())
	})

	check, err := client.Exec(ctx, booted, RemoteExecRequest{
		Command: "cat " + skillFile,
		Shell:   true,
		User:    DefaultSandboxExecUser,
		Timeout: 30 * time.Second,
	})
	if err != nil || check == nil || check.ExitCode != 0 || !strings.Contains(check.Stdout, "skill-ok") {
		t.Fatalf("snapshot did not carry the skill: err=%v result=%#v", err, check)
	}

	listed, err := client.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	found := false
	for _, item := range listed {
		if item.ID == ref.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListSnapshots missing %s: %#v", ref.ID, listed)
	}

	templates, err := client.ListTemplates(ctx)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	for _, item := range templates {
		if dockerCanonicalSnapshotID(item.ID) == ref.ID {
			t.Fatalf("skill snapshot %s leaked into the template catalog", ref.ID)
		}
	}
}

// terminalCapture drains a PTY session in the background so assertions can wait
// for output that arrived before the check, which a direct receive would have
// consumed.
type terminalCapture struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func captureTerminal(session RemoteTerminalSession) *terminalCapture {
	capture := &terminalCapture{done: make(chan struct{})}
	go func() {
		defer close(capture.done)
		for event := range session.Output() {
			if event.Err != nil {
				return
			}
			capture.mu.Lock()
			capture.buf.Write(event.Data)
			capture.mu.Unlock()
		}
	}()
	return capture
}

func (c *terminalCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *terminalCapture) waitFor(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(c.String(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("terminal output never contained %q; got: %q", want, c.String())
}

func (c *terminalCapture) assertNever(t *testing.T, unwanted string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if strings.Contains(c.String(), unwanted) {
			t.Fatalf("terminal output unexpectedly contained %q: %q", unwanted, c.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDockerBackendTerminalIntegration exercises the interactive PTY against a
// real daemon and the standard image: live output, window resize, and Ctrl-C.
func TestDockerBackendTerminalIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		6*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-terminal-%d", time.Now().UnixNano())
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

	// Provision the session sandbox the way a chat turn would, then open the
	// PTY on it.
	executor := manager.SessionShellExecutor()
	if executor == nil {
		t.Fatal("session shell executor is unavailable on a healthy docker backend")
	}
	if _, err := executor.ExecShellCommand(
		ctx, sessionID, "true", SessionWorkspaceRoot, time.Minute, nil,
	); err != nil {
		t.Fatalf("provision session sandbox: %v", err)
	}

	terminal := manager.SessionTerminalManager()
	if terminal == nil {
		t.Fatal("docker backend must advertise the terminal capability")
	}
	session, err := manager.OpenSessionTerminal(ctx, sessionID,
		RemoteTerminalOptions{Cols: 100, Rows: 40})
	if err != nil {
		t.Fatalf("OpenSessionTerminal: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	capture := captureTerminal(session)

	// Live output: the computed value 42 does not appear in the typed command's
	// own echo, so matching it proves the shell actually ran the line.
	if err := session.Write(ctx, []byte("echo LIVE=$((6*7))\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	capture.waitFor(t, "LIVE=42", 30*time.Second)

	// Resize: stty reports the geometry the PTY was resized to. The target
	// differs from the 100x40 the PTY opened at, so a match proves ExecResize
	// took effect rather than echoing the create-time ConsoleSize.
	if err := session.Resize(ctx, 120, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := session.Write(ctx, []byte("stty size\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	capture.waitFor(t, "30 120", 30*time.Second)

	// The adapter's interactive defaults reach the shell (the daemon alone
	// would leave TERM=xterm).
	if err := session.Write(ctx, []byte("echo ENV=$TERM/$LC_ALL\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	capture.waitFor(t, "ENV=xterm-256color/C.UTF-8", 30*time.Second)

	// Interrupt: Ctrl-C cancels the sleeping command, and its sentinel never
	// runs. The prompt returning is proven by the follow-up echo completing.
	if err := session.Write(ctx, []byte("sleep 60; echo SLOW=$((2+2))\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := session.Write(ctx, []byte{0x03}); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if err := session.Write(ctx, []byte("echo AFTER=$((5+5))\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	capture.waitFor(t, "AFTER=10", 20*time.Second)
	capture.assertNever(t, "SLOW=4", 3*time.Second)
}

// Closing a live Docker terminal must terminate the shell. The daemon keeps an
// exec (and its foreground jobs) alive when the hijacked connection goes away,
// so a bare disconnect would strand the process until the container is swept.
func TestDockerBackendTerminalCloseTerminatesShellIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		6*time.Minute,
	)
	defer cancel()

	sessionID := fmt.Sprintf("docker-terminal-close-%d", time.Now().UnixNano())
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

	executor := manager.SessionShellExecutor()
	if executor == nil {
		t.Fatal("session shell executor is unavailable on a healthy docker backend")
	}
	if _, err := executor.ExecShellCommand(
		ctx, sessionID, "true", SessionWorkspaceRoot, time.Minute, nil,
	); err != nil {
		t.Fatalf("provision session sandbox: %v", err)
	}

	session, err := manager.OpenSessionTerminal(ctx, sessionID, RemoteTerminalOptions{})
	if err != nil {
		t.Fatalf("OpenSessionTerminal: %v", err)
	}
	go func() {
		for range session.Output() {
		}
	}()

	// Record the shell PID and start a long foreground job with a unique
	// marker so the probe can find the job without matching itself.
	if err := session.Write(ctx, []byte("echo $$ > /tmp/weknora-close.pid\n")); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	// A job the user detached on purpose must outlive the hangup, as it would
	// when any real terminal closes.
	if err := session.Write(ctx, []byte("nohup sleep 3619 >/dev/null 2>&1 &\n")); err != nil {
		t.Fatalf("write nohup job: %v", err)
	}
	if err := session.Write(ctx, []byte("sleep 3611\n")); err != nil {
		t.Fatalf("write job: %v", err)
	}

	// Positive control: the shell and its job must both be visible before the
	// close, or a broken probe would read as a pass.
	var shellPID string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		shellPID = strings.TrimSpace(terminalProbeOutput(
			t, ctx, executor, sessionID, "cat /tmp/weknora-close.pid 2>/dev/null || true"))
		if shellPID != "" && terminalProbeJobAlive(t, ctx, executor, sessionID, "sleep 3611") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if shellPID == "" {
		t.Fatal("terminal shell never recorded its PID")
	}
	if !terminalProbeJobAlive(t, ctx, executor, sessionID, "sleep 3611") {
		t.Fatal("positive control failed: foreground job not visible before close")
	}
	if !terminalProbeJobAlive(t, ctx, executor, sessionID, "sleep 3619") {
		t.Fatal("positive control failed: nohup job not visible before close")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The hangup runs as a detached exec, so it may land just after Close
	// returns; poll until both the shell and its job are gone.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		alive := strings.Contains(terminalProbeOutput(t, ctx, executor, sessionID,
			"if [ -d /proc/"+shellPID+" ]; then echo ALIVE; else echo DEAD; fi"), "ALIVE")
		if !alive && !terminalProbeJobAlive(t, ctx, executor, sessionID, "sleep 3611") {
			// Past the script's SIGKILL pass too, so a survivor is not just
			// a job the second pass has yet to reach.
			time.Sleep(3 * time.Second)
			if !terminalProbeJobAlive(t, ctx, executor, sessionID, "sleep 3619") {
				t.Fatal("Close killed a nohup'd job; a terminal hangup must spare it")
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("Close left the shell (pid %s) and/or its foreground job running", shellPID)
}

// Cancelling the caller's context must terminate the shell just like Close. The
// adapter's stream never observes the context on its own — the pump reads the
// hijacked connection and the moby hijack stops watching the context once
// upgraded — so without the lifetime watcher a cancelled context would leak the
// shell and (via the detached TTL refresh) pin the container forever.
func TestDockerBackendTerminalContextCancelTerminatesShellIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	manager := newDockerIntegrationManager(t, cfg)

	probeCtx, cancelProbe := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		6*time.Minute,
	)
	defer cancelProbe()

	sessionID := fmt.Sprintf("docker-terminal-cancel-%d", time.Now().UnixNano())
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

	executor := manager.SessionShellExecutor()
	if executor == nil {
		t.Fatal("session shell executor is unavailable on a healthy docker backend")
	}
	if _, err := executor.ExecShellCommand(
		probeCtx, sessionID, "true", SessionWorkspaceRoot, time.Minute, nil,
	); err != nil {
		t.Fatalf("provision session sandbox: %v", err)
	}

	// The session ctx is a child of probeCtx so the probes below keep a live
	// context after we cancel only the session's.
	sessionCtx, cancelSession := context.WithCancel(probeCtx)
	defer cancelSession()
	session, err := manager.OpenSessionTerminal(sessionCtx, sessionID, RemoteTerminalOptions{})
	if err != nil {
		t.Fatalf("OpenSessionTerminal: %v", err)
	}
	go func() {
		for range session.Output() {
		}
	}()

	if err := session.Write(sessionCtx, []byte("echo $$ > /tmp/weknora-cancel.pid\n")); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if err := session.Write(sessionCtx, []byte("sleep 3613\n")); err != nil {
		t.Fatalf("write job: %v", err)
	}

	var shellPID string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		shellPID = strings.TrimSpace(terminalProbeOutput(
			t, probeCtx, executor, sessionID, "cat /tmp/weknora-cancel.pid 2>/dev/null || true"))
		if shellPID != "" && terminalProbeJobAlive(t, probeCtx, executor, sessionID, "sleep 3613") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if shellPID == "" {
		t.Fatal("terminal shell never recorded its PID")
	}
	if !terminalProbeJobAlive(t, probeCtx, executor, sessionID, "sleep 3613") {
		t.Fatal("positive control failed: foreground job not visible before cancel")
	}

	// Cancel only. No Close().
	cancelSession()

	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		alive := strings.Contains(terminalProbeOutput(t, probeCtx, executor, sessionID,
			"if [ -d /proc/"+shellPID+" ]; then echo ALIVE; else echo DEAD; fi"), "ALIVE")
		if !alive && !terminalProbeJobAlive(t, probeCtx, executor, sessionID, "sleep 3613") {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("cancelling the context left the shell (pid %s) and/or its job running", shellPID)
}

func terminalProbeOutput(
	t *testing.T, ctx context.Context, executor SessionShellExecutor, sessionID, script string,
) string {
	t.Helper()
	result, err := executor.ExecShellCommand(
		ctx, sessionID, script, SessionWorkspaceRoot, 30*time.Second, nil)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	return result.Stdout
}

// terminalProbeJobAlive reports whether the foreground job (jobCmd, e.g.
// "sleep 3611") is still running. The pattern is anchored to the start of the
// command line so the probe's own shell (whose argv contains the pattern text)
// never matches itself.
func terminalProbeJobAlive(
	t *testing.T, ctx context.Context, executor SessionShellExecutor, sessionID, jobCmd string,
) bool {
	t.Helper()
	out := terminalProbeOutput(t, ctx, executor, sessionID,
		`n=0; for p in /proc/[0-9]*; do cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); case "$cmd" in "`+jobCmd+` "*) n=$((n+1));; esac; done; echo $n`)
	return strings.TrimSpace(out) != "0"
}

func dockerUserAssignment(sessionID string) allocation.Assignment {
	return allocation.Assignment{
		SessionID: sessionID, ConfigTenantID: dockerIntegrationTenantID, ConfigID: "docker-conformance",
		Machine: allocation.UserMachine{
			OwnerTenantID: dockerIntegrationTenantID, OwnerID: "docker-user",
			ConfigTenantID: dockerIntegrationTenantID, ConfigID: "docker-conformance",
		},
	}
}

func dockerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// dockerGitWorkspaceVars matches gitWorkspaceVars for a shared-machine layout.
// checkpointScriptFor and workspaceResetScriptFor live in application/service,
// which this package cannot import.
func dockerGitWorkspaceVars(root, gitDir string) string {
	return fmt.Sprintf(`WORK_TREE=%s
GIT_DIR=%s
git_ws() {
  git -c safe.directory='*' --git-dir="$GIT_DIR" --work-tree="$WORK_TREE" "$@"
}
mkdir -p "$WORK_TREE"
`, dockerShellQuote(root), dockerShellQuote(gitDir))
}

func dockerCheckpointScript(l WorkspaceLayout, messageID string) string {
	return fmt.Sprintf(`set -e
%s
git_ws rev-parse --git-dir >/dev/null 2>&1 || {
  mkdir -p "$(dirname "$GIT_DIR")"
  git_ws init -q
  git_ws config user.email agent@weknora.local
  git_ws config user.name 'WeKnora Agent'
}
git_ws add -A
git_ws commit -q --allow-empty -m 'turn:%s'
git_ws rev-parse HEAD`, dockerGitWorkspaceVars(l.Root, l.GitDir), messageID)
}

func dockerResetScript(l WorkspaceLayout, sha string) string {
	return "set -e\n" + dockerGitWorkspaceVars(l.Root, l.GitDir) +
		fmt.Sprintf("git_ws cat-file -e \"%s^{commit}\" 2>/dev/null || exit 3\n", sha) +
		"git_ws reset --hard " + sha + "\n" +
		`current=$(git_ws symbolic-ref -q HEAD || true)
for ref in $(git_ws for-each-ref --format='%(refname)'); do
  [ -z "$ref" ] && continue
  [ "$ref" = "$current" ] && continue
  git_ws update-ref -d "$ref"
done
rm -f "$GIT_DIR"/ORIG_HEAD "$GIT_DIR"/FETCH_HEAD
git_ws reflog expire --expire=now --all
git_ws gc --prune=now
git_ws clean -fdx
`
}

func dockerUserShell(t *testing.T, ctx context.Context, mgr *SessionBoundManager, sessionID, command string) *ExecuteResult {
	t.Helper()
	result, err := mgr.ExecShellCommand(ctx, sessionID, command, "", time.Minute, nil)
	if err != nil {
		t.Fatalf("ExecShellCommand %s: %v", sessionID, err)
	}
	if result == nil {
		t.Fatalf("ExecShellCommand %s returned no result", sessionID)
	}
	return result
}

func dockerLastLine(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func requireDockerContainerGone(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	api, err := sharedDockerEngineClients.get(dockerEndpoint{
		Host:    strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_HOST")),
		Timeout: DefaultDockerHTTPTimeout,
	})
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	_, err = api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err == nil {
		t.Fatalf("container %s still exists after reclaim", id)
	}
	if !cerrdefs.IsNotFound(err) {
		t.Fatalf("docker inspect %s: %v", id, err)
	}
}

func TestDockerUserMachineSharesContainerAndIsolatesDirectories(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	redisAddr := integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR")
	remote, err := NewDockerRemoteClient(cfg)
	if err != nil {
		t.Fatalf("build docker client: %v", err)
	}
	remote.sweeper = nil
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProbe()
	if err := remote.Health(probeCtx); err != nil {
		if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("docker daemon unreachable: %v", err)
		}
		t.Skipf("docker daemon unreachable: %v", err)
	}

	sessionA, sessionB, sessionC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ns := "weknora-it-" + uuid.NewString()[:8]
	rc := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rc.Close() })
	store, err := NewRedisSessionSandboxBindingStore(rc, ns)
	if err != nil {
		t.Fatalf("binding store: %v", err)
	}
	allocs := newFakeAllocations(
		dockerUserAssignment(sessionA), dockerUserAssignment(sessionB), dockerUserAssignment(sessionC),
	)
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config: cfg, Client: remote, Store: store,
		Checker: PermissiveSessionExistenceChecker{}, SkipHealthProbe: true,
		ConfigID: "docker-conformance", ConfigTenantID: dockerIntegrationTenantID,
		Allocations: allocs, Admission: admission.NewRedis(rc, "weknora:sandbox:user:"),
		Namespace: ns,
	})
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		8*time.Minute,
	)
	defer cancel()
	machine, ok := dockerUserAssignment(sessionA).UserMachine()
	if !ok {
		t.Fatal("user assignment has no machine")
	}
	var releaseHold func()
	t.Cleanup(func() {
		if releaseHold != nil {
			releaseHold()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			time.Minute,
		)
		defer cleanupCancel()
		_, _ = mgr.ReclaimUserMachine(cleanupCtx, machine, func(context.Context) (bool, error) { return true, nil })
	})

	layoutA, err := UserWorkspaceLayout(sessionA)
	if err != nil {
		t.Fatal(err)
	}
	layoutB, err := UserWorkspaceLayout(sessionB)
	if err != nil {
		t.Fatal(err)
	}
	layoutC, err := UserWorkspaceLayout(sessionC)
	if err != nil {
		t.Fatal(err)
	}

	wrote := dockerUserShell(t, ctx, mgr, sessionA, "echo a > note.txt && cat note.txt")
	if wrote.ExitCode != 0 || strings.TrimSpace(wrote.Stdout) != "a" {
		t.Fatalf("seed A: %#v", wrote)
	}
	missing := dockerUserShell(t, ctx, mgr, sessionB, "cat note.txt || echo missing")
	if missing.ExitCode != 0 || !strings.Contains(missing.Stdout, "missing") {
		t.Fatalf("B should not see A's note: %#v", missing)
	}
	idA, okA := mgr.BoundSandboxID(ctx, sessionA)
	idB, okB := mgr.BoundSandboxID(ctx, sessionB)
	if !okA || !okB || idA == "" || idA != idB {
		t.Fatalf("sessions do not share one sandbox: A=%q (%v) B=%q (%v)", idA, okA, idB, okB)
	}
	side := dockerUserShell(t, ctx, mgr, sessionB, "echo b-side > "+dockerShellQuote(layoutB.Root+"/keep.txt")+" && cat keep.txt")
	if side.ExitCode != 0 || strings.TrimSpace(side.Stdout) != "b-side" {
		t.Fatalf("seed B: %#v", side)
	}

	checkpoint := dockerUserShell(t, ctx, mgr, sessionA, dockerCheckpointScript(layoutA, "m1"))
	sha := dockerLastLine(checkpoint.Stdout)
	if checkpoint.ExitCode != 0 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		t.Fatalf("checkpoint: sha=%q %#v", sha, checkpoint)
	}
	more := dockerUserShell(t, ctx, mgr, sessionA, "echo more >> note.txt && cat note.txt")
	if more.ExitCode != 0 || !strings.Contains(more.Stdout, "more") {
		t.Fatalf("write after checkpoint: %#v", more)
	}
	reset := dockerUserShell(t, ctx, mgr, sessionA, dockerResetScript(layoutA, sha))
	if reset.ExitCode != 0 {
		t.Fatalf("reset to checkpoint: %#v", reset)
	}
	restored := dockerUserShell(t, ctx, mgr, sessionA, "cat note.txt")
	if strings.TrimSpace(restored.Stdout) != "a" {
		t.Fatalf("A was not restored: %#v", restored)
	}
	kept := dockerUserShell(t, ctx, mgr, sessionB, "cat "+dockerShellQuote(layoutB.Root+"/keep.txt"))
	if strings.TrimSpace(kept.Stdout) != "b-side" {
		t.Fatalf("B's files changed: %#v", kept)
	}
	missingSHA := strings.Repeat("f", 40)
	if missingSHA == sha {
		missingSHA = strings.Repeat("e", 40)
	}
	absent := dockerUserShell(t, ctx, mgr, sessionA, dockerResetScript(layoutA, missingSHA))
	if absent.ExitCode != 3 {
		t.Fatalf("missing commit exit=%d, want 3: %#v", absent.ExitCode, absent)
	}
	untouched := dockerUserShell(t, ctx, mgr, sessionA, "cat note.txt")
	if strings.TrimSpace(untouched.Stdout) != "a" {
		t.Fatalf("a missing commit changed A: %#v", untouched)
	}

	fork, err := mgr.BeginWorkspaceFork(ctx, sessionA)
	if err != nil {
		t.Fatalf("BeginWorkspaceFork: %v", err)
	}
	reachable, err := fork.Copy(sessionC, sha, []string{sha})
	if err != nil {
		fork.Release()
		t.Fatalf("Copy: %v", err)
	}
	if !reachable[sha] {
		fork.Release()
		t.Fatalf("SHA not reachable: %#v", reachable)
	}
	fork.Release()
	copied := dockerUserShell(t, ctx, mgr, sessionC, "cat note.txt")
	if strings.TrimSpace(copied.Stdout) != "a" {
		t.Fatalf("C did not receive note.txt: %#v", copied)
	}
	catFile := dockerUserShell(t, ctx, mgr, sessionC, fmt.Sprintf(
		"git --git-dir=%s cat-file -e %s", dockerShellQuote(layoutC.GitDir), dockerShellQuote(sha+"^{commit}"),
	))
	if catFile.ExitCode != 0 {
		t.Fatalf("C.git does not contain the commit: %#v", catFile)
	}

	deleted, err := mgr.DeleteSessionWorkspaces(ctx, machine, []string{sessionA})
	if err != nil {
		t.Fatalf("DeleteSessionWorkspaces: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != sessionA {
		t.Fatalf("deleted %v, want [%s]", deleted, sessionA)
	}
	gone := dockerUserShell(t, ctx, mgr, sessionB, fmt.Sprintf(
		"if [ -e %s ] || [ -e %s ]; then echo a-remains; else echo a-gone; fi; cat keep.txt",
		dockerShellQuote(layoutA.Root), dockerShellQuote(layoutA.GitDir),
	))
	if gone.ExitCode != 0 || !strings.Contains(gone.Stdout, "a-gone") || !strings.Contains(gone.Stdout, "b-side") {
		t.Fatalf("A should be gone and B intact: %#v", gone)
	}

	_, releaseHold, err = mgr.HoldSessionOperation(ctx, sessionB, "pty")
	if err != nil {
		t.Fatalf("HoldSessionOperation: %v", err)
	}
	reclaimed, err := mgr.ReclaimUserMachine(ctx, machine, func(context.Context) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("reclaim while held: %v", err)
	}
	if reclaimed {
		t.Fatal("an open terminal must block reclaim")
	}
	releaseHold()
	releaseHold = nil
	reclaimed, err = mgr.ReclaimUserMachine(ctx, machine, func(context.Context) (bool, error) { return true, nil })
	if err != nil || !reclaimed {
		t.Fatalf("reclaim after release: ok=%v err=%v", reclaimed, err)
	}
	requireDockerContainerGone(t, ctx, idA)
}
