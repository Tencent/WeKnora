package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalJobLogRotatesKeepsHeadAndPrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout")
	log, err := newLocalJobLog(path)
	require.NoError(t, err)
	_, err = log.Write(bytes.Repeat([]byte("a"), localJobSegmentBytes))
	require.NoError(t, err)
	_, err = log.Write(bytes.Repeat([]byte("b"), localJobSegmentBytes+7))
	require.NoError(t, err)
	require.NoError(t, log.Close())

	head, err := os.ReadFile(path + ".head")
	require.NoError(t, err)
	require.Equal(t, bytes.Repeat([]byte("a"), 8192), head)
	prev, err := os.ReadFile(path + ".1")
	require.NoError(t, err)
	require.Equal(t, bytes.Repeat([]byte("b"), localJobSegmentBytes), prev)
	cur, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("bbbbbbb"), cur)
	rot, err := os.ReadFile(path + ".rotated")
	require.NoError(t, err)
	require.Equal(t, "33554432", string(rot))
}

func TestLocalJobLogAppendNoSparseHoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr")
	log, err := newLocalJobLog(path)
	require.NoError(t, err)
	_, err = log.Write(bytes.Repeat([]byte("x"), localJobSegmentBytes+3))
	require.NoError(t, err)
	require.NoError(t, log.Close())
	cur, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("xxx"), cur, "追加写 + 截断重开，无空洞")
}
