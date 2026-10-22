package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireShellSyntax(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "sh -n: %s", out)
}

func TestSubmitScriptEmbedsJobAndYield(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", Command: "TOKEN=1 make build", WorkDir: "/workspace"}
	script, marker := ShellJobSubmitScript(job, "TOKEN=1 make build", 10000)
	require.True(t, strings.HasPrefix(marker, shellJobMarkerPrefix))
	require.Contains(t, script, "/var/lib/weknora/jobs/sj_0123")
	require.Contains(t, script, marker)   // 结尾行标记
	require.Contains(t, script, "10000")  // yield 轮询上限
	require.Contains(t, script, "setsid") // wrapper 脱管启动
	require.Contains(t, script, `find "$D/status.json" -mmin +1440`)
	require.Contains(t, script, "exit 17")
	require.Contains(t, script, `mkdir "$D" || {`)
	require.NotContains(t, script, `D="$R/$J"; mkdir "$D" || exit 17;`)
	require.NotContains(t, script, `mkdir -p "$D"`)
	require.Contains(t, script, "iflag=skip_bytes,count_bytes")
	require.NotContains(t, script, "bs=1")
	require.Contains(t, strings.Repeat(script, 1), "meta.json")
	// 提交脚本绝不能 wait wrapper
	require.NotContains(t, script, "wait $wrapper")
	requireShellSyntax(t, script)
}

func TestSubmitScriptPersistsMaskedMetaNotExecCommand(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", Command: "make build", WorkDir: "/workspace"}
	script, _ := ShellJobSubmitScript(job, "SECRET=1 make build", 1000)
	require.Contains(t, script, `"command":"make build"`)
	require.Equal(t, 1, strings.Count(script, "SECRET=1 make build"))
}

func TestSubmitScriptDockerFollowsBackendNotSandboxID(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", SandboxID: "abc123def"}
	script, _ := ShellJobSubmitScript(job, "true", 1)
	require.NotContains(t, script, "weknora-sandbox-activity")
	script, _ = ShellJobSubmitScriptWithBackend(job, "true", 1, true)
	require.Contains(t, script, "weknora-sandbox-activity")
	script, _ = ShellJobSubmitScriptWithBackend(job, "true", 1, false)
	require.NotContains(t, script, "weknora-sandbox-activity")
}

func TestWrapperScriptRotatesAndTrapsTerm(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", Command: "sleep 100"}
	script := shellJobWrapperScript(job, "sleep 100", true)
	require.Contains(t, script, "trap")                     // TERM → 杀进程组 + cancelled
	require.Contains(t, script, "kill -")                   // 杀命令进程组
	require.Contains(t, script, "8192")                     // .head 保留 8 KiB
	require.Contains(t, script, "16777216")                 // 16 MiB 轮转阈值
	require.Contains(t, script, "weknora-sandbox-activity") // docker touch
	require.Contains(t, script, "touch_pid")
	require.Contains(t, script, ">>") // 追加写，轮转截断无空洞
	// 终态只写一次：构建残留的 printf … > /dev/null 不能留下。
	require.NotContains(t, script, "> /dev/null")
	require.Contains(t, script, "cancelled")
	require.Contains(t, script, `%Y-%m-%dT%H:%M:%S.%3NZ`)
	plain := shellJobWrapperScript(job, "sleep 100", false)
	require.NotContains(t, plain, "weknora-sandbox-activity")
	require.Contains(t, plain, "TOUCH=0")
	require.NotContains(t, plain, "> /dev/null")
	requireShellSyntax(t, script)
	requireShellSyntax(t, plain)
}

func TestMarkerFilterHoldsPartialPrefixAcrossChunks(t *testing.T) {
	marker := shellJobMarkerPrefix + "_abc123__"
	s := NewShellJobMarkerStream(marker)
	body, tail := s.Feed([]byte("out1"))
	require.Equal(t, "out1", string(body))
	require.Empty(t, tail)
	// 分块恰好把标记劈开
	half := len(marker) / 2
	body, tail = s.Feed([]byte(marker[:half]))
	require.Empty(t, body)
	require.Equal(t, marker[:half], string(tail))
	body, _ = s.Feed([]byte(marker[half:] + `{"state":"succeeded"}` + "\n" + "0 0"))
	require.Empty(t, body)
	// Final returns strings; body is already the []byte from Feed, so := cannot reuse it.
	finalBody, epilogue := s.Final()
	require.Empty(t, finalBody)
	require.Contains(t, epilogue, `"state":"succeeded"`)
}

func TestMarkerFilterHoldsOnlyPrefixLengthTail(t *testing.T) {
	s := NewShellJobMarkerStream(shellJobMarkerPrefix + "_xyz__")
	body, tail := s.Feed([]byte("keep__WEK"))
	require.Equal(t, "keep", string(body))
	require.Equal(t, "__WEK", string(tail))
	body, tail = s.Feed([]byte("NORA_SJ"))
	require.Equal(t, "__WEKNORA_SJ", string(tail), "非标记前缀最终全部放行")
	require.Empty(t, body)
}

func TestParseShellJobEpilogue(t *testing.T) {
	status, offsets, ok := ParseShellJobEpilogue(
		`{"state":"running","watcher_pid":7,"pgid":8,"started_at":"2026-10-16T00:00:00Z"}` + "\n123 45")
	require.True(t, ok)
	require.Equal(t, 7, status.WatcherPID)
	require.Equal(t, int64(123), offsets.Stdout)
	require.Equal(t, int64(45), offsets.Stderr)
	_, _, ok = ParseShellJobEpilogue("garbage")
	require.False(t, ok)
	// A blank line between the status record and the offsets is ignored.
	status, offsets, ok = ParseShellJobEpilogue(
		"\n" + `{"state":"running","watcher_pid":7,"pgid":8,"started_at":"2026-10-16T00:00:00Z"}` + "\n\n123 45\n")
	require.True(t, ok)
	require.Equal(t, int64(123), offsets.Stdout)
	require.Equal(t, int64(45), offsets.Stderr)
}

func TestShellJobBatchReadScriptReadsEachJob(t *testing.T) {
	script := ShellJobBatchReadScript("docker-box", []string{
		"sj_0123",
		"sj_abef",
		"sj_bad",
		"$(reboot)",
	})
	require.Contains(t, script, ShellJobsRoot+"/sj_0123")
	require.Contains(t, script, ShellJobsRoot+"/sj_abef")
	require.NotContains(t, script, "sj_bad")
	require.NotContains(t, script, "reboot")
	require.Contains(t, script, "cat")
	require.Contains(t, script, "meta.json")
	require.Contains(t, script, "status.json")
	require.Contains(t, script, "wc -c")
	require.Contains(t, script, "stdout")
	require.Contains(t, script, "stderr")
	require.Contains(t, script, "/proc/")
	require.Contains(t, script, "cmdline")
	require.Contains(t, script, "watcher_alive")
	require.Contains(t, script, `"meta"`)
	require.Contains(t, script, `"status"`)
	require.Contains(t, script, "total_bytes")
	require.Contains(t, script, "iflag=skip_bytes,count_bytes")
	require.NotContains(t, script, "bs=1")
	require.Contains(t, script, "sj_0123")
	require.Contains(t, script, "sj_abef")
	requireShellSyntax(t, script)
}

func TestShellJobBatchReadScriptEmitsDecodableJSON(t *testing.T) {
	root := t.TempDir()
	id := "sj_0123"
	dir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"),
		[]byte(`{"work_id":"sj_0123","command":"make"}`), 0o644))
	status := "{\"state\":\"running\",\"watcher_pid\":999999,\"pgid\":8," +
		"\"started_at\":\"2026-10-16T00:00:00Z\"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.json"), []byte(status), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stderr"), []byte("xyz"), 0o644))

	script := strings.ReplaceAll(
		ShellJobBatchReadScript("docker-box", []string{id, "sj_abef"}),
		ShellJobsRoot, root)
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	snaps := ParseShellJobBatch(string(out))
	require.Len(t, snaps, 2)
	require.True(t, snaps[1].Missing)
	require.Equal(t, id, snaps[0].Meta.ID)
	require.Equal(t, "make", snaps[0].Meta.Command)
	require.Equal(t, "running", snaps[0].Status.State)
	require.Equal(t, 999999, snaps[0].Status.WatcherPID)
	require.Equal(t, int64(5), snaps[0].Stdout.TotalBytes)
	require.Equal(t, int64(3), snaps[0].Stderr.TotalBytes)
	require.Equal(t, "hello", snaps[0].Stdout.Text)
	require.Equal(t, "xyz", snaps[0].Stderr.Text)
	require.False(t, snaps[0].WatcherAlive)
	require.False(t, snaps[0].Missing)
}

func TestShellJobBatchReadScriptReturnsHeadAndTail(t *testing.T) {
	root := t.TempDir()
	id := "sj_0123"
	dir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"state":"running","watcher_pid":1,"started_at":"2026-10-16T00:00:00Z"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"),
		[]byte(`{"work_id":"sj_0123"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout"), []byte("TAIL"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.1"), []byte("0123456789"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.head"), []byte("HEAD8K"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.rotated"), []byte("10\n"), 0o644))

	script := strings.ReplaceAll(ShellJobBatchReadScript("sbx", []string{id}), ShellJobsRoot, root)
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	snaps := ParseShellJobBatch(string(out))
	require.Len(t, snaps, 1)
	require.Equal(t, "HEAD8K6789TAIL", snaps[0].Stdout.Text)
	require.Equal(t, int64(14), snaps[0].Stdout.TotalBytes)
	require.Equal(t, int64(0), snaps[0].Stdout.OmittedBytes)
}

func TestSubmitScriptCapsForwardingForTheWholeWindow(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", Command: "true"}
	script, _ := ShellJobSubmitScript(job, "true", 10000)
	require.NotContains(t, script, "-le 1048576")
	budget := strings.Index(script, "ROOM=$(( 1048576 - BUD ))")
	clamp := strings.Index(script, `[ "$TAKE" -gt "$ROOM" ] && TAKE=$ROOM`)
	add := strings.Index(script, "BUD=$(( BUD + TAKE ))")
	advance := strings.Index(script, "CUR=$N")
	require.Greater(t, budget, 0)
	require.Less(t, budget, clamp)
	require.Less(t, clamp, add)
	require.Less(t, add, advance)
	// The allowance if closes before the cursor moves to the observed end,
	// so a capped poll still advances and a later poll does not reread it.
	require.Contains(t, script[add:advance], "fi")
	require.Contains(t, script, `cat "$D/stdout.rotated"`)
	require.Contains(t, script, `cat "$D/stderr.rotated"`)
	require.Contains(t, script, "SO=$(( RO + LN ))")
	require.Contains(t, script, "SE=$(( RE + LE ))")
	requireShellSyntax(t, script)
}

func TestWrapperScriptParentWaitsAndRotatesAfterReap(t *testing.T) {
	job := ShellJobMeta{ID: "sj_0123", Command: "sleep 100"}
	script := shellJobWrapperScript(job, "sleep 100", false)
	trapAt := strings.Index(script, "trap '")
	cmdAt := strings.Index(script, "setsid /bin/bash")
	loopAt := strings.Index(script, `while kill -0 "$C"`)
	rotAt := strings.Index(script, "rot &")
	waitAt := strings.Index(script, `wait "$C"`)
	require.GreaterOrEqual(t, trapAt, 0)
	require.Less(t, trapAt, cmdAt)
	require.Greater(t, loopAt, cmdAt)
	require.Less(t, loopAt, rotAt)
	require.Less(t, rotAt, waitAt)
	require.NotContains(t, script, "done; wait")
	require.NotContains(t, script, "done;\nwait")
	after := script[waitAt:]
	require.Contains(t, after, "r stdout")
	require.Contains(t, after, "r stderr")
	require.NotContains(t, script, "> /dev/null")
	require.Contains(t, script, "status.json.tmp")
	requireShellSyntax(t, script)
}

func TestShellJobBatchReadScriptAddsRotatedToLiveSegment(t *testing.T) {
	id := "sj_0123"
	script := ShellJobBatchReadScript("docker-box", []string{id})
	require.Contains(t, script, "wc -c")
	require.Contains(t, script, `cat "$ROTF"`)
	require.Contains(t, script, "TOTAL=$(( RO + LN ))")
	requireShellSyntax(t, script)

	root := t.TempDir()
	dir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"),
		[]byte(`{"work_id":"sj_0123","command":"make"}`), 0o644))
	status := "{\"state\":\"running\",\"watcher_pid\":999999,\"pgid\":8," +
		"\"started_at\":\"2026-10-16T00:00:00Z\"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.json"), []byte(status), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stderr"), []byte("xyz"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.rotated"), []byte("100\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stderr.rotated"), []byte("7\n"), 0o644))

	script = strings.ReplaceAll(script, ShellJobsRoot, root)
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	snaps := ParseShellJobBatch(string(out))
	require.Len(t, snaps, 1)
	require.Equal(t, int64(105), snaps[0].Stdout.TotalBytes)
	require.Equal(t, int64(10), snaps[0].Stderr.TotalBytes)
}

func TestKillOrphansScriptSkipsPidOne(t *testing.T) {
	script := ShellJobKillOrphansScript(99, 77)
	require.Contains(t, script, "PGID=99")
	require.Contains(t, script, "TOUCH=77")
	require.Contains(t, script, "kill -TERM -$PGID")
	require.Contains(t, script, "kill -KILL -$PGID")
	require.Contains(t, script, "weknora-sandbox-activity")
	require.Contains(t, script, `-gt 1`)
	require.Contains(t, script, "\ntrue\n")
	idle := ShellJobKillOrphansScript(1, 0)
	require.Contains(t, idle, "PGID=1")
	require.Contains(t, idle, "TOUCH=0")
	require.NotContains(t, idle, "kill -TERM -1")
	require.NotContains(t, idle, "kill -KILL -1")
	requireShellSyntax(t, script)
	requireShellSyntax(t, idle)
}

func TestParseShellJobBatchDecodesLinesWithoutMissingPlaceholders(t *testing.T) {
	running := `{"meta":{"work_id":"sj_0123","command":"make"},` +
		`"status":{"state":"running","watcher_pid":7,"pgid":8,"started_at":"2026-10-16T00:00:00Z"},` +
		`"stdout":{"total_bytes":123},"stderr":{"total_bytes":45},"watcher_alive":true}`
	done := `{"meta":{"work_id":"sj_abef"},` +
		`"status":{"state":"succeeded","watcher_pid":9,"pgid":10,"exit_code":0,` +
		`"started_at":"2026-10-16T00:00:00Z"},` +
		`"stdout":{"total_bytes":1},"stderr":{"total_bytes":2},"watcher_alive":false}`
	stdout := strings.Join([]string{running, done, "", "not-json"}, "\n")
	snaps := ParseShellJobBatch(stdout)
	require.Len(t, snaps, 2)
	require.Equal(t, "sj_0123", snaps[0].Meta.ID)
	require.Equal(t, 7, snaps[0].Status.WatcherPID)
	require.Equal(t, int64(123), snaps[0].Stdout.TotalBytes)
	require.Equal(t, int64(45), snaps[0].Stderr.TotalBytes)
	require.True(t, snaps[0].WatcherAlive)
	require.False(t, snaps[0].Missing)
	require.Equal(t, "sj_abef", snaps[1].Meta.ID)
	require.False(t, snaps[1].WatcherAlive)
	require.False(t, snaps[1].Missing)
	require.Empty(t, ParseShellJobBatch(""))
	require.Empty(t, ParseShellJobBatch("\n\n"))
}

func TestShellJobCursorCountsRawBytesBeforeSanitizingUTF8(t *testing.T) {
	// /w== is one invalid UTF-8 byte, followed by a valid 'x' in /3g=.
	snaps := ParseShellJobBatch(`{"meta":{"work_id":"sj_0123"},"status":{"state":"running"},` +
		`"stdout":{"slices":true,"total_bytes":1000,"from":100,"omitted_bytes":99,"body_b64":"/3g="}}`)
	require.Len(t, snaps, 1)
	require.Equal(t, "x", snaps[0].Stdout.Text)
	require.NotNil(t, snaps[0].Stdout.NextBytes)
	require.Equal(t, int64(102), *snaps[0].Stdout.NextBytes)
}

func TestShellJobLocalOutputBudgetIncludesBothStreams(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sj_0123")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, WriteShellJobJSON(filepath.Join(dir, "meta.json"), ShellJobMeta{ID: "sj_0123"}))
	require.NoError(t, WriteShellJobJSON(filepath.Join(dir, "status.json"), ShellJobStatus{State: "succeeded"}))
	for _, name := range []string{"stdout", "stderr"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat(name, 5000)), 0o600))
	}
	for _, budget := range []int{1, 2, 3, 2048} {
		for _, from := range []*ShellJobOffsets{nil, {Stdout: 1, Stderr: 1}} {
			snap, err := ReadShellJob(root, ShellJobRead{ID: "sj_0123", OutputBudget: budget, From: from})
			require.NoError(t, err)
			require.LessOrEqual(t, len(snap.Stdout.Text)+len(snap.Stderr.Text), budget)
			require.True(t, snap.Stdout.Truncated)
			require.True(t, snap.Stderr.Truncated)
		}
	}
}
