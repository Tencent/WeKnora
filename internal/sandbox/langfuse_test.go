package sandbox

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestWrapLangfuseRemoteClientPreservesSnapshotCapability(t *testing.T) {
	inner := newFakeRemoteClient(SandboxTypeCube)
	inner.capabilities.SupportsSnapshots = true

	wrapped := wrapLangfuseRemoteClient(inner)
	mgr, ok := SnapshotManagerFrom(wrapped)
	require.True(t, ok, "wrapping must not hide RemoteSnapshotManager from SnapshotManagerFrom")
	require.NotNil(t, mgr)

	ref, err := mgr.CreateSnapshot(context.Background(), "sb-1", "snap-1")
	require.NoError(t, err)
	require.NotEmpty(t, ref.ID)
}

func TestWrapLangfuseRemoteClientDoesNotInventSnapshotSupport(t *testing.T) {
	inner := &noSnapshotClient{}
	wrapped := wrapLangfuseRemoteClient(inner)
	mgr, ok := SnapshotManagerFrom(wrapped)
	require.False(t, ok)
	require.Nil(t, mgr)
}

func TestWrapLangfuseRemoteClientIdempotent(t *testing.T) {
	inner := newFakeRemoteClient(SandboxTypeDocker)
	once := wrapLangfuseRemoteClient(inner)
	twice := wrapLangfuseRemoteClient(once)
	require.Equal(t, once, twice)
}

func TestWrapLangfuseRemoteClientExecForwards(t *testing.T) {
	inner := newFakeRemoteClient(SandboxTypeDocker)
	handle, err := inner.Create(context.Background(), RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	wrapped := wrapLangfuseRemoteClient(inner)
	result, err := wrapped.Exec(context.Background(), handle, RemoteExecRequest{
		Command: "echo hi",
		Shell:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, inner.execRequests, 1)
	require.Equal(t, "echo hi", inner.execRequests[0].Command)
}

func TestTruncateSandboxPreview(t *testing.T) {
	require.Equal(t, "short", truncateSandboxPreview("short"))
	long := strings.Repeat("x", sandboxSpanPreviewRunes+8)
	got := truncateSandboxPreview(long)
	require.True(t, strings.HasSuffix(got, "…"))
	require.Equal(t, sandboxSpanPreviewRunes+1, utf8.RuneCountInString(got))
}

func TestSnapshotDeleteSpanResultDefersConflict(t *testing.T) {
	out, spanErr := snapshotDeleteSpanResult(NewRemoteError(
		SandboxTypeE2B, "DeleteSnapshot", RemoteErrorKindConflict,
		"paused sandboxes using it", nil,
	))
	require.NoError(t, spanErr, "in-use must not mark the Langfuse span ERROR")
	require.Equal(t, true, out["deferred"])
	require.Equal(t, "in_use", out["reason"])

	boom := NewRemoteError(SandboxTypeE2B, "DeleteSnapshot", RemoteErrorKindInternal, "boom", nil)
	out, spanErr = snapshotDeleteSpanResult(boom)
	require.Equal(t, boom, spanErr)
	require.Nil(t, out)
}
