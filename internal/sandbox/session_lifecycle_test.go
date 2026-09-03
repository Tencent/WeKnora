package sandbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeSessionExistenceChecker struct {
	mu      sync.Mutex
	exists  bool
	err     error
	results []bool
	calls   int
}

func (c *fakeSessionExistenceChecker) SessionExists(
	context.Context,
	SessionSandboxKey,
) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return false, c.err
	}
	if len(c.results) > 0 {
		result := c.results[0]
		c.results = c.results[1:]
		return result, nil
	}
	return c.exists, nil
}

func (c *fakeSessionExistenceChecker) setExists(exists bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exists = exists
}

type bindingStoreFaults struct {
	base             SessionSandboxBindingStore
	getErr           error
	createErr        error
	deleteErr        error
	lockErr          error
	forceCreateFalse bool
	beforeCreate     func()
	binding          *SessionSandboxBinding
}

type cancelableLifecycleStore struct {
	SessionSandboxBindingStore
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *cancelableLifecycleStore) WithLifecycleLock(
	ctx context.Context,
	_ SessionSandboxKey,
	fn func(context.Context) error,
) error {
	lockCtx, cancel := context.WithCancel(ctx)
	ownershipCtx, cancelOwnership := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancel = func() {
		cancelOwnership()
		cancel()
	}
	s.mu.Unlock()
	defer s.cancel()
	return fn(withLifecycleOwnershipContext(lockCtx, ownershipCtx))
}

func (s *cancelableLifecycleStore) cancelLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *bindingStoreFaults) Get(
	ctx context.Context,
	key SessionSandboxKey,
) (*SessionSandboxBinding, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.binding != nil {
		result := *s.binding
		return &result, nil
	}
	return s.base.Get(ctx, key)
}

func (s *bindingStoreFaults) Create(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) (bool, error) {
	if s.beforeCreate != nil {
		s.beforeCreate()
	}
	if s.createErr != nil {
		return false, s.createErr
	}
	if s.forceCreateFalse {
		return false, nil
	}
	return s.base.Create(ctx, key, binding)
}

func (s *bindingStoreFaults) DeleteIfMatch(
	ctx context.Context,
	key SessionSandboxKey,
	provider RemoteProvider,
	sandboxID string,
) (bool, error) {
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	return s.base.DeleteIfMatch(ctx, key, provider, sandboxID)
}

func (s *bindingStoreFaults) InvalidateByConfig(
	ctx context.Context,
	tenantID uint64,
	configID string,
) (int, error) {
	return s.base.InvalidateByConfig(ctx, tenantID, configID)
}

func (s *bindingStoreFaults) WithLifecycleLock(
	ctx context.Context,
	key SessionSandboxKey,
	fn func(context.Context) error,
) error {
	if s.lockErr != nil {
		return s.lockErr
	}
	return s.base.WithLifecycleLock(ctx, key, fn)
}

func newTestRemoteSessionLifecycle(
	t *testing.T,
	client RemoteSandboxClient,
	store SessionSandboxBindingStore,
	checker SessionExistenceChecker,
) *remoteSessionLifecycle {
	t.Helper()
	lifecycle, err := newRemoteSessionLifecycle(
		client,
		store,
		checker,
		RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:   RemoteTimeoutExplicit,
				Value:  time.Hour,
				Action: RemoteOnTimeoutPause,
			},
		},
		time.Second,
		"",
	)
	require.NoError(t, err)
	return lifecycle
}

func TestRemoteSessionLifecycleCreatesOnceAcrossCoordinators(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	first := newTestRemoteSessionLifecycle(t, client, store, checker)
	second := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	start := make(chan struct{})
	handles := make(chan RemoteSandboxHandle, 2)
	errs := make(chan error, 2)
	for _, lifecycle := range []*remoteSessionLifecycle{first, second} {
		go func(current *remoteSessionLifecycle) {
			<-start
			handle, err := current.Resolve(context.Background(), key)
			handles <- handle
			errs <- err
		}(lifecycle)
	}
	close(start)

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	firstHandle := <-handles
	secondHandle := <-handles
	require.NotNil(t, firstHandle)
	require.NotNil(t, secondHandle)
	require.Equal(t, firstHandle.ID(), secondHandle.ID())
	creates, _, _, _, _ := client.counts()
	require.Equal(t, 1, creates)
}

func TestRemoteSessionLifecycleAllowsDifferentKeysInParallel(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	firstKey := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	secondKey := SessionSandboxKey{TenantID: 42, SessionID: "session-b"}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})

	client.createHook = func(ctx context.Context, req RemoteCreateRequest) error {
		if req.Metadata[remoteMetadataSessionID] != firstKey.SessionID {
			return nil
		}
		close(firstEntered)
		select {
		case <-releaseFirst:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := lifecycle.Resolve(context.Background(), firstKey)
		firstDone <- err
	}()
	<-firstEntered

	secondDone := make(chan error, 1)
	go func() {
		_, err := lifecycle.Resolve(context.Background(), secondKey)
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("different session was serialized behind the first")
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
}

func TestRemoteSessionLifecycleReplacesOnlyTerminalBinding(t *testing.T) {
	tests := []struct {
		name       string
		getErr     error
		state      RemoteSandboxState
		wantCreate int
		wantErr    bool
	}{
		{
			name:       "terminal state",
			state:      RemoteStateTerminal,
			wantCreate: 1,
		},
		{
			name:    "unavailable error",
			state:   RemoteStateRunning,
			getErr:  NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindUnavailable, "offline", nil),
			wantErr: true,
		},
		{
			name:    "internal error",
			state:   RemoteStateRunning,
			getErr:  NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindInternal, "unknown", nil),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemorySessionSandboxBindingStore()
			client := newFakeRemoteClient(SandboxTypeCube)
			checker := &fakeSessionExistenceChecker{exists: true}
			lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
			client.addSandbox("existing", "template-a", tt.state, nil, time.Now())
			client.getErrs["existing"] = tt.getErr
			created, err := store.Create(
				context.Background(),
				key,
				validSessionSandboxBinding(key, "existing"),
			)
			require.NoError(t, err)
			require.True(t, created)

			handle, err := lifecycle.Resolve(context.Background(), key)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, handle)
				binding, getErr := store.Get(context.Background(), key)
				require.NoError(t, getErr)
				require.Equal(t, "existing", binding.SandboxID)
			} else {
				require.NoError(t, err)
				require.NotEqual(t, "existing", handle.ID())
			}
			creates, _, _, _, _ := client.counts()
			require.Equal(t, tt.wantCreate, creates)
		})
	}
}

func TestRemoteSessionLifecycleProviderMismatchNeverUsesOldID(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeE2B)
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	old := validSessionSandboxBinding(key, "cube-old")
	old.Provider = SandboxTypeCube
	created, err := store.Create(context.Background(), key, old)
	require.NoError(t, err)
	require.True(t, created)

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, SandboxTypeE2B, handle.Provider())
	creates, connects, gets, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Zero(t, connects)
	require.Zero(t, gets)
	require.Zero(t, deletes)

	binding, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, SandboxTypeE2B, binding.Provider)
	require.Equal(t, handle.ID(), binding.SandboxID)
}

func TestRemoteSessionLifecycleRecoversOldestMetadataCandidate(t *testing.T) {
	for _, provider := range []RemoteProvider{SandboxTypeCube, SandboxTypeE2B} {
		t.Run(string(provider), func(t *testing.T) {
			store := NewMemorySessionSandboxBindingStore()
			client := newFakeRemoteClient(provider)
			checker := &fakeSessionExistenceChecker{exists: true}
			lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
			metadata := lifecycle.metadata(key)
			client.addSandbox("newer", "template-a", RemoteStateRunning, metadata, time.Unix(200, 0))
			client.addSandbox("older", "template-a", RemoteStateRunning, metadata, time.Unix(100, 0))

			handle, err := lifecycle.Resolve(context.Background(), key)
			require.NoError(t, err)
			require.Equal(t, "older", handle.ID())
			creates, connects, _, lists, _ := client.counts()
			require.Zero(t, creates)
			require.Equal(t, 1, connects)
			require.Equal(t, 1, lists)
		})
	}
}

func TestRemoteSessionLifecycleDeletesDuplicateMetadataCandidates(t *testing.T) {
	for _, provider := range []RemoteProvider{SandboxTypeCube, SandboxTypeE2B} {
		t.Run(string(provider), func(t *testing.T) {
			store := NewMemorySessionSandboxBindingStore()
			client := newFakeRemoteClient(provider)
			checker := &fakeSessionExistenceChecker{exists: true}
			lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
			metadata := lifecycle.metadata(key)
			client.addSandbox("selected", "template-a", RemoteStateRunning, metadata, time.Unix(100, 0))
			client.addSandbox("duplicate", "template-a", RemoteStateRunning, metadata, time.Unix(200, 0))

			handle, err := lifecycle.Resolve(context.Background(), key)
			require.NoError(t, err)
			require.Equal(t, "selected", handle.ID())
			require.True(t, client.hasSandbox("selected"))
			require.False(t, client.hasSandbox("duplicate"))
			_, _, _, _, deletes := client.counts()
			require.Equal(t, 1, deletes)
		})
	}
}

func TestRemoteSessionLifecycleStopsDuplicateCleanupAfterLockLoss(t *testing.T) {
	for _, provider := range []RemoteProvider{SandboxTypeCube, SandboxTypeE2B} {
		t.Run(string(provider), func(t *testing.T) {
			base := NewMemorySessionSandboxBindingStore()
			store := &cancelableLifecycleStore{SessionSandboxBindingStore: base}
			client := newFakeRemoteClient(provider)
			checker := &fakeSessionExistenceChecker{exists: true}
			lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
			metadata := lifecycle.metadata(key)
			client.addSandbox("selected", "template-a", RemoteStateRunning, metadata, time.Unix(100, 0))
			client.addSandbox("duplicate", "template-a", RemoteStateRunning, metadata, time.Unix(200, 0))
			client.deleteHook = func(ctx context.Context, sandboxID string) error {
				if sandboxID == "duplicate" {
					store.cancelLock()
				}
				return ctx.Err()
			}

			handle, err := lifecycle.Resolve(context.Background(), key)
			require.Error(t, err)
			require.Nil(t, handle)
			require.True(t, client.hasSandbox("selected"))
			require.True(t, client.hasSandbox("duplicate"))
			binding, getErr := base.Get(context.Background(), key)
			require.NoError(t, getErr)
			require.Nil(t, binding)
		})
	}
}

func TestE2BRemoteSessionLifecycleBindingStoreContract(t *testing.T) {
	tests := []struct {
		name     string
		newStore func(*testing.T) SessionSandboxBindingStore
	}{
		{
			name: "memory",
			newStore: func(*testing.T) SessionSandboxBindingStore {
				return NewMemorySessionSandboxBindingStore()
			},
		},
		{
			name: "redis",
			newStore: func(t *testing.T) SessionSandboxBindingStore {
				store, _, _ := newRedisBindingTestStore(t)
				return store
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.newStore(t)
			client := newFakeRemoteClient(SandboxTypeE2B)
			checker := &fakeSessionExistenceChecker{exists: true}
			first := newTestRemoteSessionLifecycle(t, client, store, checker)
			second := newTestRemoteSessionLifecycle(t, client, store, checker)
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-e2b"}

			firstHandle, err := first.Resolve(context.Background(), key)
			require.NoError(t, err)
			secondHandle, err := second.Resolve(context.Background(), key)
			require.NoError(t, err)
			require.Equal(t, firstHandle.ID(), secondHandle.ID())
			require.Equal(t, SandboxTypeE2B, secondHandle.Provider())

			creates, _, _, _, _ := client.counts()
			require.Equal(t, 1, creates)
			binding, err := store.Get(context.Background(), key)
			require.NoError(t, err)
			require.NotNil(t, binding)
			require.Equal(t, SandboxTypeE2B, binding.Provider)
			require.Equal(t, firstHandle.ID(), binding.SandboxID)

			require.NoError(t, second.Destroy(context.Background(), key))
			require.False(t, client.hasSandbox(firstHandle.ID()))
			binding, err = store.Get(context.Background(), key)
			require.NoError(t, err)
			require.Nil(t, binding)
		})
	}
}

func TestRemoteSessionLifecycleCleansCreatedSandboxWhenSessionDisappears(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{results: []bool{true, false}}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	require.Nil(t, handle)
	creates, _, _, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 1, deletes)
	binding, getErr := store.Get(context.Background(), key)
	require.NoError(t, getErr)
	require.Nil(t, binding)
}

func TestRemoteSessionLifecycleCleansCreatedSandboxAfterCallerCancellation(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	ctx, cancel := context.WithCancel(context.Background())
	client.afterCreate = func(RemoteSandboxHandle) {
		cancel()
	}

	handle, err := lifecycle.Resolve(ctx, key)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, handle)
	require.False(t, client.hasSandbox("cube-1"))
	creates, _, _, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 1, deletes)
	binding, getErr := store.Get(context.Background(), key)
	require.NoError(t, getErr)
	require.Nil(t, binding)
}

func TestRemoteSessionLifecycleCleansCreateLoserAndUsesWinner(t *testing.T) {
	base := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	client.addSandbox("winner", "template-a", RemoteStateRunning, nil, time.Now())
	store := &bindingStoreFaults{base: base, forceCreateFalse: true}
	store.beforeCreate = func() {
		created, err := base.Create(
			context.Background(),
			key,
			validSessionSandboxBinding(key, "winner"),
		)
		require.NoError(t, err)
		require.True(t, created)
	}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, "winner", handle.ID())
	creates, connects, _, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 1, connects)
	require.Equal(t, 1, deletes)
}

func TestRemoteSessionLifecycleDoesNotDeleteCreatedSandboxChosenAsWinner(t *testing.T) {
	base := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	store := &bindingStoreFaults{base: base, forceCreateFalse: true}
	store.beforeCreate = func() {
		created, err := base.Create(
			context.Background(),
			key,
			validSessionSandboxBinding(key, "cube-1"),
		)
		require.NoError(t, err)
		require.True(t, created)
	}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, "cube-1", handle.ID())
	require.True(t, client.hasSandbox("cube-1"))
	_, _, _, _, deletes := client.counts()
	require.Zero(t, deletes)
}

func TestRemoteSessionLifecycleCleansCreatedSandboxWhenBindingWriteFails(t *testing.T) {
	base := NewMemorySessionSandboxBindingStore()
	bindErr := errors.New("binding write failed")
	cleanupErr := NewRemoteError(
		SandboxTypeCube,
		"Delete",
		RemoteErrorKindUnavailable,
		"cleanup unavailable",
		nil,
	)
	store := &bindingStoreFaults{base: base, createErr: bindErr}
	client := newFakeRemoteClient(SandboxTypeCube)
	client.deleteErrs["cube-1"] = cleanupErr
	lifecycle := newTestRemoteSessionLifecycle(
		t,
		client,
		store,
		&fakeSessionExistenceChecker{exists: true},
	)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.Nil(t, handle)
	require.ErrorIs(t, err, bindErr)
	require.ErrorIs(t, err, cleanupErr)
	creates, _, _, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 1, deletes)
	binding, getErr := base.Get(context.Background(), key)
	require.NoError(t, getErr)
	require.Nil(t, binding)
}

func TestRemoteSessionLifecycleDoesNotCreateWhenRecoveryListFails(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	listErr := NewRemoteError(
		SandboxTypeCube,
		"List",
		RemoteErrorKindUnavailable,
		"list unavailable",
		nil,
	)
	client.listErr = listErr
	lifecycle := newTestRemoteSessionLifecycle(
		t,
		client,
		store,
		&fakeSessionExistenceChecker{exists: true},
	)

	handle, err := lifecycle.Resolve(
		context.Background(),
		SessionSandboxKey{TenantID: 42, SessionID: "session-a"},
	)
	require.Nil(t, handle)
	require.ErrorIs(t, err, listErr)
	creates, _, _, lists, _ := client.counts()
	require.Zero(t, creates)
	require.Equal(t, 1, lists)
}

func TestRemoteSessionLifecycleFailsClosedOnBindingErrors(t *testing.T) {
	fault := errors.New("binding unavailable")
	tests := []struct {
		name  string
		store SessionSandboxBindingStore
	}{
		{
			name: "lock",
			store: &bindingStoreFaults{
				base:    NewMemorySessionSandboxBindingStore(),
				lockErr: fault,
			},
		},
		{
			name: "get",
			store: &bindingStoreFaults{
				base:   NewMemorySessionSandboxBindingStore(),
				getErr: fault,
			},
		},
		{
			name: "malformed binding",
			store: &bindingStoreFaults{
				base: NewMemorySessionSandboxBindingStore(),
				binding: &SessionSandboxBinding{
					Version:   SessionSandboxBindingVersion,
					TenantID:  42,
					SessionID: "session-a",
					SandboxID: "sandbox-a",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeRemoteClient(SandboxTypeCube)
			lifecycle := newTestRemoteSessionLifecycle(
				t,
				client,
				tt.store,
				&fakeSessionExistenceChecker{exists: true},
			)
			handle, err := lifecycle.Resolve(
				context.Background(),
				SessionSandboxKey{TenantID: 42, SessionID: "session-a"},
			)
			require.Error(t, err)
			require.Nil(t, handle)
			creates, connects, gets, lists, deletes := client.counts()
			require.Zero(t, creates+connects+gets+lists+deletes)
		})
	}
}

func TestRemoteSessionLifecycleSerializesResolveAndDestroy(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	createEntered := make(chan struct{})
	releaseCreate := make(chan struct{})
	client.createHook = func(ctx context.Context, _ RemoteCreateRequest) error {
		close(createEntered)
		select {
		case <-releaseCreate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	resolveDone := make(chan error, 1)
	go func() {
		_, err := lifecycle.Resolve(context.Background(), key)
		resolveDone <- err
	}()
	<-createEntered

	destroyDone := make(chan error, 1)
	go func() {
		destroyDone <- lifecycle.Destroy(context.Background(), key)
	}()
	select {
	case err := <-destroyDone:
		t.Fatalf("Destroy completed before Resolve released the lifecycle lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseCreate)
	require.NoError(t, <-resolveDone)
	require.NoError(t, <-destroyDone)
	binding, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.Nil(t, binding)
	creates, _, _, _, deletes := client.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 1, deletes)
}

func TestRemoteSessionLifecycleDoesNotCreateForDeletedSession(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: false}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	require.NoError(t, lifecycle.Destroy(context.Background(), key))
	handle, err := lifecycle.Resolve(context.Background(), key)
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	require.Nil(t, handle)
	creates, _, _, _, _ := client.counts()
	require.Zero(t, creates)
}

func TestRemoteSessionLifecycleCleansBindingAfterSessionDeletion(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	client := newFakeRemoteClient(SandboxTypeCube)
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle := newTestRemoteSessionLifecycle(t, client, store, checker)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	handle, err := lifecycle.Resolve(context.Background(), key)
	require.NoError(t, err)
	checker.setExists(false)

	resolved, err := lifecycle.Resolve(context.Background(), key)
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	require.Nil(t, resolved)
	require.False(t, client.hasSandbox(handle.ID()))
	binding, getErr := store.Get(context.Background(), key)
	require.NoError(t, getErr)
	require.Nil(t, binding)
}

func TestRemoteSessionLifecycleDestroySemantics(t *testing.T) {
	t.Run("transient delete preserves binding", func(t *testing.T) {
		store := NewMemorySessionSandboxBindingStore()
		client := newFakeRemoteClient(SandboxTypeCube)
		lifecycle := newTestRemoteSessionLifecycle(
			t,
			client,
			store,
			&fakeSessionExistenceChecker{exists: true},
		)
		key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
		client.addSandbox("existing", "template-a", RemoteStateRunning, nil, time.Now())
		client.deleteErrs["existing"] = NewRemoteError(
			SandboxTypeCube,
			"Delete",
			RemoteErrorKindUnavailable,
			"offline",
			nil,
		)
		created, err := store.Create(
			context.Background(),
			key,
			validSessionSandboxBinding(key, "existing"),
		)
		require.NoError(t, err)
		require.True(t, created)

		require.Error(t, lifecycle.Destroy(context.Background(), key))
		binding, err := store.Get(context.Background(), key)
		require.NoError(t, err)
		require.NotNil(t, binding)
	})

	t.Run("not found delete removes binding", func(t *testing.T) {
		store := NewMemorySessionSandboxBindingStore()
		client := newFakeRemoteClient(SandboxTypeCube)
		lifecycle := newTestRemoteSessionLifecycle(
			t,
			client,
			store,
			&fakeSessionExistenceChecker{exists: true},
		)
		key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
		created, err := store.Create(
			context.Background(),
			key,
			validSessionSandboxBinding(key, "missing"),
		)
		require.NoError(t, err)
		require.True(t, created)

		require.NoError(t, lifecycle.Destroy(context.Background(), key))
		binding, err := store.Get(context.Background(), key)
		require.NoError(t, err)
		require.Nil(t, binding)
	})

	t.Run("provider mismatch only deletes binding", func(t *testing.T) {
		store := NewMemorySessionSandboxBindingStore()
		client := newFakeRemoteClient(SandboxTypeE2B)
		lifecycle := newTestRemoteSessionLifecycle(
			t,
			client,
			store,
			&fakeSessionExistenceChecker{exists: true},
		)
		key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
		old := validSessionSandboxBinding(key, "cube-old")
		old.Provider = SandboxTypeCube
		created, err := store.Create(context.Background(), key, old)
		require.NoError(t, err)
		require.True(t, created)

		require.NoError(t, lifecycle.Destroy(context.Background(), key))
		_, _, _, _, deletes := client.counts()
		require.Zero(t, deletes)
		binding, err := store.Get(context.Background(), key)
		require.NoError(t, err)
		require.Nil(t, binding)
	})

	t.Run("binding delete failure is reported after remote deletion", func(t *testing.T) {
		base := NewMemorySessionSandboxBindingStore()
		deleteErr := errors.New("binding delete failed")
		store := &bindingStoreFaults{base: base, deleteErr: deleteErr}
		client := newFakeRemoteClient(SandboxTypeCube)
		lifecycle := newTestRemoteSessionLifecycle(
			t,
			client,
			store,
			&fakeSessionExistenceChecker{exists: true},
		)
		key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
		client.addSandbox("existing", "template-a", RemoteStateRunning, nil, time.Now())
		created, err := base.Create(
			context.Background(),
			key,
			validSessionSandboxBinding(key, "existing"),
		)
		require.NoError(t, err)
		require.True(t, created)

		err = lifecycle.Destroy(context.Background(), key)
		require.ErrorIs(t, err, deleteErr)
		require.False(t, client.hasSandbox("existing"))
		binding, getErr := base.Get(context.Background(), key)
		require.NoError(t, getErr)
		require.NotNil(t, binding)
	})
}

// Two configs in one workspace can point at the SAME provider account. Without
// config_id in metadata, cleaning one config would delete the other's
// sandboxes, so this tag is a correctness requirement, not a nicety.
func TestLifecycleTagsSandboxWithConfigID(t *testing.T) {
	lifecycle := newTestLifecycleWithConfigID(t, "cfg-42")

	md := lifecycle.metadata(SessionSandboxKey{TenantID: 7, SessionID: "s-1"})

	require.Equal(t, "cfg-42", md[remoteMetadataConfigID])
	require.Equal(t, "7", md[remoteMetadataTenantID])
	require.Equal(t, "s-1", md[remoteMetadataSessionID])
}

func TestLifecycleTagsGlobalDefaultConfigWithSentinel(t *testing.T) {
	lifecycle := newTestLifecycleWithConfigID(t, "")

	md := lifecycle.metadata(SessionSandboxKey{TenantID: 7, SessionID: "s-1"})

	require.Equal(t, types.SandboxConfigIDGlobalDefault, md[remoteMetadataConfigID],
		"an empty config ID must still be tagged, so listing can target it")
}

// lifecycleFixture is the smallest complete lifecycle: one memory binding
// store, one fake provider, one live session bound to config "cfg-1".
type lifecycleFixture struct {
	lifecycle *remoteSessionLifecycle
	bindings  *MemorySessionSandboxBindingStore
	client    *fakeRemoteClient
	checker   *fakeSessionExistenceChecker
	key       SessionSandboxKey
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	client := newFakeRemoteClient(SandboxTypeCube)
	bindings := NewMemorySessionSandboxBindingStore()
	checker := &fakeSessionExistenceChecker{exists: true}
	lifecycle, err := newRemoteSessionLifecycle(
		client,
		bindings,
		checker,
		RemoteCreateRequest{TemplateID: "template-a"},
		time.Minute,
		"cfg-1",
	)
	require.NoError(t, err)
	return &lifecycleFixture{
		lifecycle: lifecycle,
		bindings:  bindings,
		client:    client,
		checker:   checker,
		key:       SessionSandboxKey{TenantID: 42, SessionID: "session-a"},
	}
}

func TestResolveRecreatesSandboxAfterImageChange(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	first, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	second, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	require.NotEqual(t, first.ID(), second.ID(),
		"a session must pick up the new skill image on its next sandbox use")
	require.Contains(t, fx.client.deleteIDs, first.ID(),
		"the stale sandbox must be released, not leaked")
}

func TestResolveKeepsStaleSandboxDuringAnOpenTurn(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	require.NoError(t, fx.bindings.BeginTurn(ctx, fx.key))
	first, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	second, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	require.Equal(t, first.ID(), second.ID(),
		"a turn already using the sandbox must keep it after an install")
	require.NotContains(t, fx.client.deleteIDs, first.ID())
}

func TestResolveRebuildsStaleSandboxOnFirstUseOfNextTurn(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	require.NoError(t, fx.bindings.BeginTurn(ctx, fx.key))
	first, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.NoError(t, fx.bindings.EndTurn(ctx, fx.key))
	require.NoError(t, fx.bindings.BeginTurn(ctx, fx.key))

	second, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	require.NotEqual(t, first.ID(), second.ID(),
		"the next turn's first resolve must pick up the new skill image")
	require.Contains(t, fx.client.deleteIDs, first.ID())

	third, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	require.Equal(t, second.ID(), third.ID(),
		"later resolves of the new turn must keep the rebuilt sandbox")
}

func TestInvalidateDoesNotDisturbOtherConfigs(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	_, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-other")
	require.NoError(t, err)
	require.Zero(t, n)
}

// Marking is not the same as tearing down: a turn already running in the old
// sandbox must keep working, and the new image arrives on its next use.
func TestInvalidateLeavesTheRunningSandboxAlone(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	handle, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.True(t, fx.client.hasSandbox(handle.ID()),
		"invalidation must not destroy a sandbox a turn may be using")
	binding, err := fx.bindings.Get(ctx, fx.key)
	require.NoError(t, err)
	require.NotNil(t, binding, "the binding is marked, never dropped outside the lock")
	require.NotNil(t, binding.StaleAt)
}

// A second invalidation between two uses must not report the same binding
// twice: the caller logs the count as "how many sessions were affected".
func TestInvalidateSkipsBindingsAlreadyMarked(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	_, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	n, err = fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)
	require.Zero(t, n)
}

// The rebuilt sandbox must not inherit the mark, or every later use would
// destroy and recreate a perfectly current sandbox.
func TestResolveClearsStalenessAfterRecreating(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	_, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	_, err = fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
	require.NoError(t, err)

	second, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	binding, err := fx.bindings.Get(ctx, fx.key)
	require.NoError(t, err)
	require.NotNil(t, binding)
	require.Nil(t, binding.StaleAt)
	require.Equal(t, "cfg-1", binding.ConfigID,
		"the rebuilt binding must record the config it belongs to")

	third, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)
	require.Equal(t, second.ID(), third.ID())
}

// Another workspace's session may sit on the same config ID; marking must stay
// inside the workspace whose image changed.
func TestInvalidateStaysWithinTheWorkspace(t *testing.T) {
	ctx := context.Background()
	fx := newLifecycleFixture(t)
	_, err := fx.lifecycle.Resolve(ctx, fx.key)
	require.NoError(t, err)

	n, err := fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID+1, "cfg-1")
	require.NoError(t, err)
	require.Zero(t, n)
}

func newTestLifecycleWithConfigID(t *testing.T, configID string) *remoteSessionLifecycle {
	t.Helper()
	lifecycle, err := newRemoteSessionLifecycle(
		newFakeRemoteClient(SandboxTypeCube),
		NewMemorySessionSandboxBindingStore(),
		&fakeSessionExistenceChecker{exists: true},
		RemoteCreateRequest{TemplateID: "template-a"},
		time.Minute,
		configID,
	)
	require.NoError(t, err)
	return lifecycle
}
