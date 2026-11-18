package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeJobDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, "sj_0123", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	return root
}

func TestValidShellJobID(t *testing.T) {
	require.True(t, ValidShellJobID("sj_0123"))
	id, err := NewShellJobID()
	require.NoError(t, err)
	require.True(t, ValidShellJobID(id))
	require.False(t, ValidShellJobID("sj_012"))
	require.False(t, ValidShellJobID("sj_01234"))
	require.False(t, ValidShellJobID("sj_0123456789ab"))
	require.False(t, ValidShellJobID("sa_0123"))
	require.False(t, ValidShellJobID("sj_012Z"))
	require.False(t, ValidShellJobID(""))
}

// 结尾 = 当前段（不足补 .1 末尾）；开头 = .head（没有则当前段开头）；
// 总字节 = .rotated + 当前段大小（.1 存 [.rotated-size(.1), .rotated)）。
func TestReadShellJobHeadAndTailAcrossRotation(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":         "TAIL",
		"stdout.1":       "0123456789",
		"stdout.head":    "HEAD8K",
		"stdout.rotated": "10",
		"status.json":    `{"state":"running","watcher_pid":9,"pgid":9,"started_at":"2026-10-16T00:00:00Z"}`,
	})
	snap, err := ReadShellJob(root, ShellJobRead{ID: "sj_0123"})
	require.NoError(t, err)
	// .head 占 [0, 6)，.1 占 [0, 10)，当前段占 [10, 14)。开头用 .head，
	// 与 .1 重叠的前缀不重复，结尾补上 .1 的剩余部分和当前段。
	require.Equal(t, "HEAD8K6789TAIL", snap.Stdout.Text)
	require.Equal(t, int64(0), snap.Stdout.OmittedBytes)
	require.Equal(t, int64(14), snap.Stdout.TotalBytes)
	require.Equal(t, int64(0), snap.Stdout.FromBytes)
	require.False(t, snap.Stdout.Truncated)
	require.True(t, snap.WatcherAlive)
}

func TestReadShellJobTailPadWithoutHeadFile(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":         "TAIL",
		"stdout.1":       "0123456789",
		"stdout.rotated": "10",
		"status.json":    `{"state":"running","watcher_pid":9,"started_at":"2026-10-16T00:00:00Z"}`,
	})
	snap, err := ReadShellJob(root, ShellJobRead{ID: "sj_0123"})
	require.NoError(t, err)
	require.Equal(t, "0123456789TAIL", snap.Stdout.Text)
	require.Equal(t, int64(0), snap.Stdout.FromBytes)
	require.Equal(t, int64(0), snap.Stdout.OmittedBytes)
	require.Equal(t, int64(14), snap.Stdout.TotalBytes)
}

func TestReadShellJobRotatedCountAllowsTrailingNewline(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":         "TAIL",
		"stdout.rotated": "10\n",
		"status.json":    `{"state":"running","watcher_pid":9,"started_at":"2026-10-16T00:00:00Z"}`,
	})
	snap, err := ReadShellJob(root, ShellJobRead{ID: "sj_0123"})
	require.NoError(t, err)
	require.Equal(t, int64(14), snap.Stdout.TotalBytes)
}

func TestReadShellJobShortStreamIsNotDoubled(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":      "hello",
		"status.json": `{"state":"running","watcher_pid":9,"started_at":"2026-10-16T00:00:00Z"}`,
	})
	snap, err := ReadShellJob(root, ShellJobRead{ID: "sj_0123"})
	require.NoError(t, err)
	require.Equal(t, "hello", snap.Stdout.Text)
	require.Equal(t, int64(5), snap.Stdout.TotalBytes)
	require.False(t, snap.Stdout.Truncated)
}

func TestReadShellJobIncrementalAndOmitted(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":         "CDEFGH",
		"stdout.1":       "AB",
		"stdout.rotated": "2",
		"status.json":    `{"state":"running","started_at":"2026-10-16T00:00:00Z"}`,
	})
	// 读取位置 4 在当前段 [2, 8) 内，只返回这个位置之后的字节。
	snap, err := ReadShellJob(root, ShellJobRead{
		ID:   "sj_0123",
		From: &ShellJobOffsets{Stdout: 4, Stderr: 0},
	})
	require.NoError(t, err)
	require.Equal(t, "EFGH", snap.Stdout.Text)
	require.Equal(t, int64(0), snap.Stdout.OmittedBytes)
	require.Equal(t, int64(4), snap.Stdout.FromBytes)
	require.Equal(t, int64(8), snap.Stdout.TotalBytes)
	require.False(t, snap.Stdout.Truncated)
}

func TestReadShellJobCursorInDiscardedRangeStartsAtEarliestByte(t *testing.T) {
	root := writeJobDir(t, map[string]string{
		"stdout":         "CDEFGH",
		"stdout.1":       "XY",
		"stdout.rotated": "5",
		"status.json":    `{"state":"running","started_at":"2026-10-16T00:00:00Z"}`,
	})
	// .1 占 [3, 5)，当前段占 [5, 11)。位置 1 已经不在磁盘上，从 3 开始读。
	snap, err := ReadShellJob(root, ShellJobRead{
		ID:   "sj_0123",
		From: &ShellJobOffsets{Stdout: 1},
	})
	require.NoError(t, err)
	require.Equal(t, "XYCDEFGH", snap.Stdout.Text)
	require.Equal(t, int64(2), snap.Stdout.OmittedBytes)
	require.Equal(t, int64(3), snap.Stdout.FromBytes)
	require.Equal(t, int64(11), snap.Stdout.TotalBytes)
}

func TestReadShellJobMissingDirIsMissingNotError(t *testing.T) {
	snap, err := ReadShellJob(t.TempDir(), ShellJobRead{ID: "sj_0123"})
	require.NoError(t, err)
	require.True(t, snap.Missing)
}

func TestWriteShellJobJSONIsAtomicAndRFC3339(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	require.NoError(t, WriteShellJobJSON(path, ShellJobStatus{State: "running"}))
	require.NoFileExists(t, filepath.Dir(path)+"/.status.json-*")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"state":"running"`)
}
