//go:build docker_integration

// Shell-job protocol against a real Docker daemon.
//
// Covers the remote rows of the shell spec's test matrix: a short command
// finishes inside one exec, a long command returns a running handle, rotation
// keeps an 8 KiB .head, TERM cancels the command process group while the
// wrapper still writes the terminal record, and a submit-exec timeout does
// not kill the setsid wrapper.
//
// The suite uses the same daemon gate as docker_integration_test.go
// (DOCKER_INTEGRATION_IMAGE, DOCKER_INTEGRATION_HOST). Without the image it
// skips; it does not fail.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestShellJobShortCommandOneRoundTrip: the command finishes inside the yield
// window, so the same exec that submitted it returns the terminal record.
func TestShellJobShortCommandOneRoundTrip(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	job := ShellJobMeta{ID: "sj_0123", Command: "echo hi", WorkDir: "/workspace"}
	script, _ := ShellJobSubmitScript(job, job.Command, 5000)
	res := dockerIntegrationExec(t, client, handle, script, 16*time.Second)
	require.Equal(t, 0, res.ExitCode, "stderr=%s", res.Stderr)
	require.False(t, res.Killed)
	require.Contains(t, res.Stdout, "hi", "stdout=%s", clipForLog(res.Stdout))
	require.Contains(t, res.Stdout, `"state":"succeeded"`, "窗口内结束直接带回终态")
}

// TestShellJobLongCommandReturnsRunningThenTerminal: the yield window expires
// while the command is still running, so the submit exec returns a handle.
// The wrapper keeps the command and later writes the terminal record itself.
func TestShellJobLongCommandReturnsRunningThenTerminal(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	const id = "sj_ab01"
	job := ShellJobMeta{ID: id, Command: "sleep 3; echo done", WorkDir: "/workspace"}
	script, marker := ShellJobSubmitScript(job, job.Command, 1000)
	res := dockerIntegrationExec(t, client, handle, script, 12*time.Second)
	require.Equal(t, 0, res.ExitCode, "stderr=%s stdout=%s", res.Stderr, clipForLog(res.Stdout))
	require.Contains(t, res.Stdout, marker)
	require.Contains(t, res.Stdout, `"state":"running"`, "窗口到期仍是 running")
	require.NotContains(t, res.Stdout, "done", "yield 返回时命令尚未结束")

	var snap ShellJobSnapshot
	require.Eventually(t, func() bool {
		loaded, err := readRemoteJob(t, client, handle, id)
		if err != nil || !WorkStateSucceededLike(loaded.Status.State) {
			return false
		}
		snap = loaded
		return true
	}, 15*time.Second, 200*time.Millisecond)
	require.True(t, WorkStateSucceededLike(snap.Status.State))
	require.Equal(t, "done\n", snap.Stdout.Text)
}

// TestShellJobCancelKillsGroupWritesCancelled sends TERM to the watcher.
// The trap kills the command process group and the wrapper still writes
// cancelled (exit 143) afterwards.
func TestShellJobCancelKillsGroupWritesCancelled(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	const id = "sj_fedc"
	job := ShellJobMeta{ID: id, Command: "sleep 60", WorkDir: "/workspace"}
	script, _ := ShellJobSubmitScript(job, job.Command, 1000)
	res := dockerIntegrationExec(t, client, handle, script, 12*time.Second)
	require.Equal(t, 0, res.ExitCode, "stderr=%s", res.Stderr)
	snap, err := readRemoteJob(t, client, handle, id)
	require.NoError(t, err)
	require.Equal(t, "running", snap.Status.State)
	require.NotZero(t, snap.Status.WatcherPID)
	alive, ok := remoteCommandCount(t, client, handle, "sleep 60")
	require.True(t, ok, "process probe failed before TERM")
	require.Greater(t, alive, 0, "sleep 60 must be visible before TERM")

	killed := dockerIntegrationExec(t, client, handle,
		"kill -TERM "+strconv.Itoa(snap.Status.WatcherPID)+"; sleep 1",
		10*time.Second)
	require.Equal(t, 0, killed.ExitCode, "stderr=%s", killed.Stderr)

	require.Eventually(t, func() bool {
		loaded, loadErr := readRemoteJob(t, client, handle, id)
		if loadErr != nil || loaded.Status.State != "cancelled" {
			return false
		}
		snap = loaded
		n, countOK := remoteCommandCount(t, client, handle, "sleep 60")
		return countOK && n == 0
	}, 10*time.Second, 200*time.Millisecond)
	require.Equal(t, "cancelled", snap.Status.State)
	require.NotNil(t, snap.Status.ExitCode)
	require.Equal(t, 143, *snap.Status.ExitCode, "wrapper 应活到写完 cancelled")
	gone, ok := remoteCommandCount(t, client, handle, "sleep 60")
	require.True(t, ok)
	require.Zero(t, gone, "TERM 应杀掉命令进程组")
}

// TestShellJobRotationKeepsHeadAndCommandSurvives writes 20 MiB plus a tail
// marker. The watcher rotates at 16 MiB, keeps the first 8 KiB in .head, and
// the command still reaches the marker. Totals are .rotated plus the live
// segment, not "one segment plus a 16-byte fudge".
func TestShellJobRotationKeepsHeadAndCommandSurvives(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	const id = "sj_aa01"
	// 20 MiB of x, then a marker that can only appear if the command keeps
	// running across rotation. tr '\0' is the command the wrapper receives.
	const command = "head -c 20971520 /dev/zero | tr '\\0' 'x'; echo TAILMARK"
	job := ShellJobMeta{ID: id, Command: command, WorkDir: "/workspace"}
	script, _ := ShellJobSubmitScript(job, job.Command, 1000)
	// The submit loop forwards at most 1 MiB. 60s lets that copy finish; a
	// kill here must not be treated as a rotation failure because the wrapper
	// is already detached.
	res, err := execInSandbox(client, handle, script, 60*time.Second)
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Eventually(t, func() bool {
		state, ok := remoteJobState(client, handle, id)
		return ok && state == "succeeded"
	}, 90*time.Second, time.Second)

	snap, root, err := loadRemoteJob(t, client, handle, id)
	require.NoError(t, err)
	require.Equal(t, "succeeded", snap.Status.State)
	dir := filepath.Join(root, id)
	head, err := os.ReadFile(filepath.Join(dir, "stdout.head"))
	require.NoError(t, err)
	require.Equal(t, shellJobHeadBytes, len(head), "rotation writes an 8 KiB .head")
	require.Equal(t, bytes.Repeat([]byte{'x'}, shellJobHeadBytes), head, "开头保留")
	info, err := os.Stat(filepath.Join(dir, "stdout.1"))
	require.NoError(t, err)
	require.Positive(t, info.Size())

	rotatedRaw, err := os.ReadFile(filepath.Join(dir, "stdout.rotated"))
	require.NoError(t, err)
	rotated, err := strconv.ParseInt(strings.TrimSpace(string(rotatedRaw)), 10, 64)
	require.NoError(t, err)
	live, err := os.ReadFile(filepath.Join(dir, "stdout"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, rotated, int64(shellJobSegmentBytes), ".rotated 至少跨过一个 16 MiB 段")
	require.Equal(t, rotated+int64(len(live)), snap.Stdout.TotalBytes, "总字节 = .rotated + 当前段")
	const written = int64(20971520 + len("TAILMARK\n"))
	require.LessOrEqual(t, snap.Stdout.TotalBytes, written, "计数不超过命令写过的字节")
	require.True(t, strings.HasPrefix(snap.Stdout.Text, "xxxx"), "开头保留")
	require.Contains(t, streamTail(t, dir, "stdout"), "TAILMARK", "轮转后命令仍写完")
	require.Contains(t, snap.Stdout.Text, "TAILMARK")
}

// TestShellJobSubmitExecTimeoutLeavesWrapperRunning kills the submit exec
// with the in-container timeout. The wrapper was started under setsid, so
// the command process group is still running afterwards.
func TestShellJobSubmitExecTimeoutLeavesWrapperRunning(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	const id = "sj_cc03"
	job := ShellJobMeta{ID: id, Command: "sleep 90", WorkDir: "/workspace"}
	script, _ := ShellJobSubmitScript(job, job.Command, 30000)
	res := dockerIntegrationExec(t, client, handle, script, 3*time.Second)
	require.True(t, res.Killed, "submit exec must hit its own timeout, exit=%d stdout=%s stderr=%s",
		res.ExitCode, clipForLog(res.Stdout), res.Stderr)

	var snap ShellJobSnapshot
	require.Eventually(t, func() bool {
		loaded, loadErr := readRemoteJob(t, client, handle, id)
		if loadErr != nil || loaded.Status.State != "running" || loaded.Status.WatcherPID == 0 {
			return false
		}
		if !remoteWatcherAlive(t, client, handle, loaded.Status.WatcherPID, id) {
			return false
		}
		n, ok := remoteCommandCount(t, client, handle, "sleep 90")
		if !ok || n == 0 {
			return false
		}
		snap = loaded
		return true
	}, 15*time.Second, 300*time.Millisecond)
	require.Equal(t, "running", snap.Status.State)
	require.NotZero(t, snap.Status.WatcherPID)
	require.True(t, remoteWatcherAlive(t, client, handle, snap.Status.WatcherPID, id),
		"timeout 不应杀掉 setsid wrapper")
	n, ok := remoteCommandCount(t, client, handle, "sleep 90")
	require.True(t, ok)
	require.Greater(t, n, 0, "timeout 不应杀掉命令进程组")
}

// WorkStateSucceededLike reports the terminal state of a command that exited 0.
func WorkStateSucceededLike(state string) bool {
	return state == "succeeded"
}

func dockerIntegrationClient(t *testing.T) RemoteSandboxClient {
	t.Helper()
	cfg := dockerIntegrationConfig(t)
	client, err := NewDockerRemoteClient(cfg)
	require.NoError(t, err)
	// This process must not sweep unrelated containers on a shared daemon.
	client.sweeper = nil
	probeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Health(probeCtx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	return client
}

func dockerIntegrationHandle(t *testing.T, client RemoteSandboxClient) RemoteSandboxHandle {
	t.Helper()
	if os.Getenv("DOCKER_INTEGRATION_IMAGE") == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE not set")
	}
	return dockerIntegrationHandleFrom(t, client)
}

func dockerIntegrationHandleFrom(t *testing.T, client RemoteSandboxClient) RemoteSandboxHandle {
	t.Helper()
	image := strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_IMAGE"))
	if image == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: image,
		Metadata:   map[string]string{"weknora.test": "shell-job"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := client.Delete(cleanupCtx, handle.ID()); err != nil {
			t.Errorf("delete shell-job sandbox: %v", err)
		}
	})
	return handle
}

func dockerIntegrationExec(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	script string,
	timeout time.Duration,
) *RemoteExecResult {
	t.Helper()
	res, err := execInSandbox(client, handle, script, timeout)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

func execInSandbox(
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	script string,
	timeout time.Duration,
) (*RemoteExecResult, error) {
	return client.Exec(context.Background(), handle, RemoteExecRequest{
		Command: script,
		Shell:   true,
		Timeout: timeout,
	})
}

// readRemoteJob copies one job directory out of the sandbox and reads it with
// ReadShellJob. The wrapper's echo writes ".rotated" with a trailing newline;
// ReadShellJob rejects that newline, so the local copy is trimmed to the same
// integer the in-sandbox shell reader already accepts.
func readRemoteJob(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	id string,
) (ShellJobSnapshot, error) {
	t.Helper()
	snap, _, err := loadRemoteJob(t, client, handle, id)
	return snap, err
}

func loadRemoteJob(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	id string,
) (ShellJobSnapshot, string, error) {
	t.Helper()
	root, err := fetchRemoteJobRoot(t, client, handle, id)
	if err != nil {
		return ShellJobSnapshot{}, "", err
	}
	if err := trimRotatedCounters(filepath.Join(root, id)); err != nil {
		return ShellJobSnapshot{}, "", err
	}
	snap, err := ReadShellJob(root, ShellJobRead{ID: id})
	if err != nil {
		return ShellJobSnapshot{}, "", err
	}
	return snap, root, nil
}

func fetchRemoteJobRoot(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	id string,
) (string, error) {
	t.Helper()
	if !ValidShellJobID(id) {
		return "", fmt.Errorf("sandbox: invalid shell job id %q", id)
	}
	res, err := execInSandbox(client, handle,
		"tar -C "+shQuote(ShellJobsRoot)+" -cf - "+shQuote(id),
		time.Minute)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("tar job %s: no result", id)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("tar job %s: exit %d stderr=%s", id, res.ExitCode, clipForLog(res.Stderr))
	}
	root := t.TempDir()
	cmd := exec.Command("tar", "-C", root, "-xf", "-")
	cmd.Stdin = strings.NewReader(res.Stdout)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("untar job %s: %w %s", id, err, clipForLog(string(out)))
	}
	return root, nil
}

// trimRotatedCounters strips the trailing newline echo writes into
// stdout.rotated and stderr.rotated. readRotated treats any non-digit as a
// corrupt counter and would otherwise report 0 after a real rotation.
func trimRotatedCounters(dir string) error {
	for _, name := range []string{"stdout.rotated", "stderr.rotated"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == string(raw) {
			continue
		}
		if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func remoteJobState(client RemoteSandboxClient, handle RemoteSandboxHandle, id string) (string, bool) {
	if !ValidShellJobID(id) {
		return "", false
	}
	res, err := execInSandbox(client, handle,
		"cat "+shQuote(path.Join(ShellJobsRoot, id, "status.json")),
		15*time.Second)
	if err != nil || res == nil || res.ExitCode != 0 {
		return "", false
	}
	var status ShellJobStatus
	if err := json.Unmarshal([]byte(res.Stdout), &status); err != nil || status.State == "" {
		return "", false
	}
	return status.State, true
}

// remoteCommandCount counts processes whose cmdline is exactly argv or starts
// with "argv ". The probe's own shell contains the text but does not start
// with it, so it cannot match itself.
func remoteCommandCount(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	argv string,
) (int, bool) {
	t.Helper()
	script := fmt.Sprintf(`n=0
for p in /proc/[0-9]*; do
  cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null || true)
  case "$cmd" in
    %s|%s*) n=$((n+1));;
  esac
done
echo "$n"
`, shQuote(argv), shQuote(argv+" "))
	res, err := execInSandbox(client, handle, script, 15*time.Second)
	if err != nil || res == nil || res.ExitCode != 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		return 0, false
	}
	return n, true
}

func remoteWatcherAlive(
	t *testing.T,
	client RemoteSandboxClient,
	handle RemoteSandboxHandle,
	pid int,
	jobID string,
) bool {
	t.Helper()
	if pid <= 0 || !ValidShellJobID(jobID) {
		return false
	}
	script := fmt.Sprintf(
		`if [ -r /proc/%d/cmdline ]; then tr '\0' ' ' < /proc/%d/cmdline; fi`,
		pid, pid)
	res, err := execInSandbox(client, handle, script, 15*time.Second)
	if err != nil || res == nil || res.ExitCode != 0 {
		return false
	}
	return strings.Contains(res.Stdout, jobID)
}

// streamTail is the retained end of a stream: the live segment when it holds
// the marker, otherwise the rotated-away .1 copy.
func streamTail(t *testing.T, dir, name string) string {
	t.Helper()
	live, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	if strings.Contains(string(live), "TAILMARK") {
		return string(live)
	}
	prev, err := os.ReadFile(filepath.Join(dir, name+".1"))
	require.NoError(t, err)
	return string(prev)
}

func clipForLog(s string) string {
	const limit = 400
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

func TestShellJobSharedMachineCancelChecksOwnerAndStopsStubbornProcess(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	jobs := []ShellJobMeta{
		{
			ID: "sj_a111", TenantID: 7, SessionID: "session-a", SandboxID: handle.ID(),
			Command: `trap '' TERM; while :; do sleep 1; done`,
		},
		{ID: "sj_b111", TenantID: 7, SessionID: "session-b", SandboxID: handle.ID(), Command: "sleep 60"},
	}
	for _, job := range jobs {
		script, _ := ShellJobSubmitScript(job, job.Command, 250)
		res := dockerIntegrationExec(t, client, handle, script, 10*time.Second)
		require.Equal(t, 0, res.ExitCode, res.Stderr)
	}
	// Even if ownership changed after the host-side read, the signal exec must
	// verify persisted tenant, session and sandbox identity again.
	for _, wrong := range []ShellJobMeta{
		{ID: jobs[1].ID, TenantID: 7, SessionID: "session-a", SandboxID: handle.ID()},
		{ID: jobs[1].ID, TenantID: 70, SessionID: "session-b", SandboxID: handle.ID()},
		{ID: jobs[1].ID, TenantID: 7, SessionID: "session-b", SandboxID: "replaced"},
	} {
		res := dockerIntegrationExec(t, client, handle, ShellJobCancelScript(wrong, true), 10*time.Second)
		require.Equal(t, 3, res.ExitCode)
	}
	res := dockerIntegrationExec(t, client, handle, ShellJobCancelScript(jobs[0], true), 10*time.Second)
	require.Equal(t, 0, res.ExitCode, res.Stderr)
	require.Contains(t, res.Stdout, `"state":"cancelled"`)
	snapshots := ParseShellJobBatch(dockerIntegrationExec(t, client, handle,
		ShellJobBatchReadScript(handle.ID(), []string{jobs[0].ID, jobs[1].ID}), 10*time.Second).Stdout)
	require.Len(t, snapshots, 2)
	require.Equal(t, "cancelled", snapshots[0].Status.State)
	require.Equal(t, "running", snapshots[1].Status.State)
	require.True(t, snapshots[1].WatcherAlive, "another chat on the same machine keeps running")
}

func TestShellJobBudgetedIncrementalReadAfterRotation(t *testing.T) {
	client := dockerIntegrationClient(t)
	handle := dockerIntegrationHandle(t, client)
	id := "sj_c111"
	script := `D=/var/lib/weknora/jobs/sj_c111; mkdir -p "$D"
printf '%s' '{"work_id":"sj_c111"}' > "$D/meta.json"
printf '%s' '{"state":"succeeded"}' > "$D/status.json"
printf 65536 > "$D/stdout.rotated"
head -c 20000 /dev/zero | tr '\0' x > "$D/stdout.1"
printf FINAL-DIAGNOSTIC > "$D/stdout"
printf STDERR > "$D/stderr"
`
	require.Equal(t, 0, dockerIntegrationExec(t, client, handle, script, 10*time.Second).ExitCode)
	offsets := ShellJobOffsets{Stdout: 1}
	var output strings.Builder
	for i := 0; i < 5; i++ {
		readScript := ShellJobBatchReadScriptWithBudget(
			handle.ID(), []string{id}, map[string]ShellJobOffsets{id: offsets}, 12000,
		)
		res := dockerIntegrationExec(t, client, handle, readScript, 10*time.Second)
		require.Equal(t, 0, res.ExitCode, res.Stderr)
		snapshots := ParseShellJobBatch(res.Stdout)
		require.Len(t, snapshots, 1)
		snap := snapshots[0]
		require.LessOrEqual(t, len(snap.Stdout.Text)+len(snap.Stderr.Text), 12000)
		if i == 0 {
			require.Positive(t, snap.Stdout.OmittedBytes)
		}
		output.WriteString(snap.Stdout.Text)
		require.NotNil(t, snap.Stdout.NextBytes)
		offsets = ShellJobOffsets{Stdout: *snap.Stdout.NextBytes, Stderr: *snap.Stderr.NextBytes}
		if offsets.Stdout == snap.Stdout.TotalBytes {
			break
		}
	}
	require.Equal(t, strings.Repeat("x", 20000)+"FINAL-DIAGNOSTIC", output.String())
}
