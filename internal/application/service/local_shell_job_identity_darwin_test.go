//go:build desktop && darwin

package service

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryDoesNotSignalReusedPID(t *testing.T) {
	stamp, err := localProcessStartedAt(context.Background(), os.Getpid())
	require.NoError(t, err)
	require.NotZero(t, stamp)
	// 启动时间不匹配（PID 被复用）→ 不发信号。本测试用错误时间戳，永远到不了 kill。
	require.NoError(t, localRecoverProcess(context.Background(), os.Getpid(), stamp+1))
	require.NoError(t, localRecoverProcess(context.Background(), os.Getpid(), 1))
}
