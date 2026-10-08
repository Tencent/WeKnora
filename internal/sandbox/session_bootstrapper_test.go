package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

var testSessionKey = SessionSandboxKey{TenantID: 1, SessionID: "session-bootstrap"}

type recordingBootstrapper struct {
	override      string
	overrideErr   error
	afterErr      error
	afterCalls    int
	afterHandleID string
}

func (b *recordingBootstrapper) TemplateOverride(
	context.Context, SessionSandboxKey,
) (string, error) {
	return b.override, b.overrideErr
}

func (b *recordingBootstrapper) AfterCreate(
	_ context.Context, _ SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	b.afterCalls++
	if handle != nil {
		b.afterHandleID = handle.ID()
	}
	return b.afterErr
}

func newLifecycleForTest(t *testing.T) (*remoteSessionLifecycle, *fakeRemoteClient, SessionSandboxBindingStore) {
	t.Helper()
	client := newFakeRemoteClient(SandboxTypeCube)
	store := NewMemorySessionSandboxBindingStore()
	lc := newTestRemoteSessionLifecycle(
		t, client, store, &fakeSessionExistenceChecker{exists: true},
	)
	return lc, client, store
}

func TestCreateAndBindUsesTemplateOverride(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1"}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, "snap-1", client.lastCreateRequest.TemplateID)
}

func TestCreateAndBindKeepsConfigTemplateWithoutOverride(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: ""}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, lc.createRequest.TemplateID, client.lastCreateRequest.TemplateID)
}

// 最重要的一条：override 绝不能污染跨 session 复用的 l.createRequest。
func TestTemplateOverrideDoesNotMutateSharedCreateRequest(t *testing.T) {
	lc, _, _ := newLifecycleForTest(t)
	baseTemplate := lc.createRequest.TemplateID
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1"}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, baseTemplate, lc.createRequest.TemplateID,
		"共享的 createRequest 被改了，同 config 下其他会话会从别人的快照启动")
}

func TestAfterCreateRunsWithTheNewHandle(t *testing.T) {
	lc, _, _ := newLifecycleForTest(t)
	b := &recordingBootstrapper{override: "snap-1"}
	lc.bootstrapper = b

	handle, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, 1, b.afterCalls)
	require.Equal(t, handle.ID(), b.afterHandleID)
}

// 全有或全无：引导失败必须销毁沙箱并删掉 binding，否则用户会在一个
// 「文件系统停在分叉时刻而不是分叉点」的沙箱里干活。
func TestAfterCreateFailureDestroysSandboxAndBinding(t *testing.T) {
	lc, client, store := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1", afterErr: errors.New("git reset failed")}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Contains(t, client.deleteIDs, client.lastCreatedID)

	binding, getErr := store.Get(context.Background(), testSessionKey)
	require.NoError(t, getErr)
	require.Nil(t, binding, "引导失败后不得留下 binding")
}

func TestTemplateOverrideErrorAbortsCreate(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{overrideErr: errors.New("db down")}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Empty(t, client.lastCreatedID, "读不到 override 就不该建沙箱")
}

type recordingCreateFailureHandler struct {
	recordingBootstrapper
	failedCalls int
	lastFailErr error
}

func (b *recordingCreateFailureHandler) OnCreateFailed(_ context.Context, _ SessionSandboxKey, err error) {
	b.failedCalls++
	b.lastFailErr = err
}

func TestNilBootstrapperIsANoOp(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = nil

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, lc.createRequest.TemplateID, client.lastCreateRequest.TemplateID)
}

// A missing fork snapshot must not be retried forever. Create using the
// override failed, so the bootstrapper has to be told — AfterCreate never
// runs, and abandon on git-reset-failure cannot fire.
func TestCreateFailureWithOverrideNotifiesBootstrapper(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	handler := &recordingCreateFailureHandler{
		recordingBootstrapper: recordingBootstrapper{override: "snap-1"},
	}
	lc.bootstrapper = handler
	client.createErr = NewRemoteError(
		SandboxTypeCube, "Create", RemoteErrorKindNotFound, "template gone", nil,
	)

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Equal(t, 1, handler.failedCalls)
	require.True(t, IsRemoteNotFound(handler.lastFailErr))
	require.Empty(t, client.lastCreatedID)
}

func TestCreateFailureWithoutOverrideDoesNotNotifyBootstrapper(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	handler := &recordingCreateFailureHandler{}
	lc.bootstrapper = handler
	client.createErr = NewRemoteError(
		SandboxTypeCube, "Create", RemoteErrorKindNotFound, "template gone", nil,
	)

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Zero(t, handler.failedCalls)
}

func TestTransientCreateFailureWithOverrideStillNotifiesBootstrapper(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	handler := &recordingCreateFailureHandler{
		recordingBootstrapper: recordingBootstrapper{override: "snap-1"},
	}
	lc.bootstrapper = handler
	client.createErr = NewRemoteError(
		SandboxTypeCube, "Create", RemoteErrorKindUnavailable, "provider down", nil,
	)

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Equal(t, 1, handler.failedCalls)
}
