package logger

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildFilenameKeepsRequestIDInsideDir(t *testing.T) {
	require.Equal(t, "3f2a-9c_1.log", buildFilename("3f2a-9c_1"))
	require.Equal(t, "______etc_cron_d_x.log", buildFilename("../../etc/cron.d/x"))
	require.Len(t, buildFilename(string(make([]byte, 500))), len("20060102_150405.000.log"),
		"an id with nothing usable falls back to a timestamp")
	long := buildFilename(string(func() []byte {
		b := make([]byte, 300)
		for i := range b {
			b[i] = 'a'
		}
		return b
	}()))
	require.Len(t, long, maxDebugFilenameID+len(".log"))
}

func TestLLMDebugLogWritesPrivateFileInsideDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "llm_debug")
	t.Setenv("LLM_DEBUG_LOG", dir)
	saved := llmDebug.dir
	savedEnabled := llmDebug.enabled
	t.Cleanup(func() {
		llmDebug.dir = saved
		llmDebug.enabled = savedEnabled
	})
	configureLLMDebugLog()

	ctx := WithRequestID(context.Background(), "../escaped")
	LLMDebugLog(ctx, &LLMCallRecord{CallType: "Chat", Model: "m"})

	_, err := os.Stat(filepath.Join(root, "escaped.log"))
	require.True(t, os.IsNotExist(err), "the request id must not choose the directory")
	info, err := os.Stat(filepath.Join(dir, "___escaped.log"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		dirInfo, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	}
}
