package service

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type processingArtifactInvalidator interface {
	Invalidate(context.Context, types.ProcessingArtifactKey, []byte) error
}

type historicalArtifactStorageResolver struct {
	backendID string
	service   interfaces.FileService
}

func (r historicalArtifactStorageResolver) ResolveFileService(
	context.Context,
	*types.Tenant,
	string,
	string,
	string,
) (interfaces.FileService, string, error) {
	return nil, "", errors.New("storage backend is not active")
}

func (r historicalArtifactStorageResolver) ResolveHistoricalFileService(
	_ context.Context,
	_ *types.Tenant,
	backendID, _ string,
) (interfaces.FileService, string, error) {
	if backendID != r.backendID {
		return nil, "", errors.New("storage backend not found")
	}
	return filesvc.NewBackendScopedFileService(backendID, r.service), "cos", nil
}

func (r historicalArtifactStorageResolver) ResolveBackend(
	context.Context,
	*types.Tenant,
	string,
	string,
) (*types.StorageBackend, error) {
	return nil, nil
}

func requireProcessingArtifactInvalidator(t *testing.T, store interfaces.ProcessingArtifactStore) processingArtifactInvalidator {
	t.Helper()
	invalidator, ok := store.(processingArtifactInvalidator)
	require.True(t, ok, "processing artifact store must support exact-key invalidation")
	return invalidator
}

func TestProcessingArtifactStoreInvalidatesTenantScopedInlineManifest(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidate-inline")
	otherTenantKey := key
	otherTenantKey.TenantID++
	for _, candidate := range []struct {
		key   types.ProcessingArtifactKey
		value []byte
	}{
		{key: key, value: []byte("tenant-seven")},
		{key: otherTenantKey, value: []byte("tenant-eight")},
	} {
		_, created, err := state.store.PutIfAbsent(context.Background(), candidate.key, candidate.value)
		require.NoError(t, err)
		require.True(t, created)
	}

	require.NoError(t, requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, []byte("tenant-seven")))
	_, hit, err := state.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	value, hit, err := state.store.Get(context.Background(), otherTenantKey)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, []byte("tenant-eight"), value)
}

func TestProcessingArtifactStoreInvalidationIsIdempotentForMissingKey(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidate-missing")

	err := requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, []byte("missing"))
	require.NoError(t, err)
}

func TestProcessingArtifactStoreInvalidatesObjectThroughItsOriginalStorageBackend(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidation-original-backend")
	backendA := newArtifactFakeFileService()
	backendB := newArtifactFakeFileService()
	path := "cos://shared-key"
	backendA.objects[path] = []byte("cached")
	backendB.objects[path] = []byte("user file")
	defaultBackendID := "backend-b"
	tenant := &types.Tenant{ID: key.TenantID, DefaultStorageBackendID: &defaultBackendID}
	store := newProcessingArtifactStoreWithDependencies(
		state.repository,
		&artifactTenantRepository{tenant: tenant},
		state.files,
		filesvc.NewFileServiceFromStorageConfig,
		&artifactStorageBackendResolver{services: map[string]interfaces.FileService{
			"backend-a": backendA,
			"backend-b": backendB,
		}},
		ProcessingArtifactMaxPayload,
		NewProcessingArtifactCounterRegistry(),
	)
	seedProcessingArtifact(
		t,
		state.repository,
		key,
		nil,
		types.BuildStorageBackendPath("backend-a", path),
		int64(len("cached")),
		artifactSHA([]byte("cached")),
	)

	require.NoError(t, requireProcessingArtifactInvalidator(t, store).Invalidate(
		context.Background(),
		key,
		[]byte("cached"),
	))

	_, _, objectsA := backendA.snapshot()
	_, _, objectsB := backendB.snapshot()
	assert.NotContains(t, objectsA, path)
	assert.Equal(t, []byte("user file"), objectsB[path])
}

func TestProcessingArtifactStoreReadsAndPurgesObjectAfterBackendRetirementRace(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	backend := newArtifactFakeFileService()
	backendID := "backend-a"
	path := "cos://cached-object"
	want := []byte("cached")
	backend.objects[path] = bytes.Clone(want)
	key := processingArtifactServiceKey(t, "retired-storage-backend")
	seedProcessingArtifact(
		t,
		state.repository,
		key,
		nil,
		types.BuildStorageBackendPath(backendID, path),
		int64(len(want)),
		artifactSHA(want),
	)
	store := newProcessingArtifactStoreWithDependencies(
		state.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: key.TenantID}},
		state.files,
		filesvc.NewFileServiceFromStorageConfig,
		historicalArtifactStorageResolver{backendID: backendID, service: backend},
		ProcessingArtifactMaxPayload,
		NewProcessingArtifactCounterRegistry(),
	)

	value, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, want, value)

	result, err := store.PurgeExpired(context.Background(), time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), result.Deleted)
	_, _, objects := backend.snapshot()
	assert.NotContains(t, objects, path)
	_, err = state.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}

func TestProcessingArtifactStoreInvalidatesOwnedObjectAndKeepsNonAuthoritativeObject(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactServiceKey(t, "invalidate-object")
		value := bytes.Repeat([]byte("o"), ProcessingArtifactInlineLimit+1)
		_, created, err := state.store.PutIfAbsent(context.Background(), key, value)
		require.NoError(t, err)
		require.True(t, created)
		artifact, err := state.repository.Get(context.Background(), key)
		require.NoError(t, err)
		objectPath, ok := processingArtifactObjectPath(artifact.ObjectPath)
		require.True(t, ok)

		require.NoError(t, requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, value))
		_, err = state.repository.Get(context.Background(), key)
		assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
		_, deletes, objects := state.files.snapshot()
		assert.Equal(t, []string{objectPath}, deletes)
		assert.NotContains(t, objects, objectPath)
	})

	t.Run("non-authoritative", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactServiceKey(t, "invalidate-non-authoritative")
		path := "minio://historical-bucket/object"
		state.files.objects[path] = []byte("historical")
		seedProcessingArtifact(t, state.repository, key, nil, path, 10, artifactSHA([]byte("historical")))
		store := NewProcessingArtifactStore(
			state.repository,
			&artifactTenantRepository{tenant: &types.Tenant{ID: key.TenantID}},
			artifactMetadataFileService{FileService: state.files, provider: "local"},
		)

		err := requireProcessingArtifactInvalidator(t, store).Invalidate(context.Background(), key, []byte("historical"))
		require.ErrorContains(t, err, "not authoritatively owned")
		_, err = state.repository.Get(context.Background(), key)
		require.NoError(t, err)
		_, deletes, objects := state.files.snapshot()
		assert.Empty(t, deletes)
		assert.Equal(t, []byte("historical"), objects[path])
	})

	t.Run("same-provider path without artifact ownership reference", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactServiceKey(t, "invalidate-unowned-local")
		path := "local://7/exports/user-document.bin"
		state.files.objects[path] = []byte("user document")
		seedProcessingArtifactReference(
			t,
			state.repository,
			key,
			nil,
			path,
			int64(len(state.files.objects[path])),
			artifactSHA(state.files.objects[path]),
		)

		err := requireProcessingArtifactInvalidator(t, state.store).Invalidate(
			context.Background(), key, state.files.objects[path],
		)

		require.ErrorContains(t, err, "not authoritatively owned")
		_, manifestErr := state.repository.Get(context.Background(), key)
		require.NoError(t, manifestErr)
		_, deletes, objects := state.files.snapshot()
		assert.Empty(t, deletes)
		assert.Equal(t, []byte("user document"), objects[path])
	})
}

func TestProcessingArtifactStoreInvalidationKeepsManifestWhenObjectDeleteFails(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidate-object-delete-failure")
	path := "local://tenant-7/object"
	state.files.objects[path] = []byte("object")
	state.files.deleteErrors[path] = errors.New("delete object failed")
	seedProcessingArtifact(t, state.repository, key, nil, path, 6, artifactSHA([]byte("object")))

	err := requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, []byte("object"))
	assert.ErrorIs(t, err, state.files.deleteErrors[path])
	_, manifestErr := state.repository.Get(context.Background(), key)
	require.NoError(t, manifestErr)
	_, deletes, objects := state.files.snapshot()
	assert.Equal(t, []string{path}, deletes)
	assert.Equal(t, []byte("object"), objects[path])
}

func TestProcessingArtifactStoreInvalidationDeletesObjectBeforeManifest(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidate-manifest-delete-failure")
	path := "local://tenant-7/object"
	state.files.objects[path] = []byte("object")
	seedProcessingArtifact(t, state.repository, key, nil, path, 6, artifactSHA([]byte("object")))
	deleteErr := errors.New("delete manifest failed")
	store := NewProcessingArtifactStore(
		&artifactDeleteErrorRepository{ProcessingArtifactRepository: state.repository, err: deleteErr},
		&artifactTenantRepository{tenant: &types.Tenant{ID: key.TenantID}},
		state.files,
	)

	err := requireProcessingArtifactInvalidator(t, store).Invalidate(context.Background(), key, []byte("object"))
	assert.ErrorIs(t, err, deleteErr)
	_, err = state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	_, deletes, objects := state.files.snapshot()
	assert.Equal(t, []string{path}, deletes)
	assert.NotContains(t, objects, path)
}

func TestProcessingArtifactStoreRejectsOversizedCandidatesBeforeMutation(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	oversized := bytes.Repeat([]byte("x"), 64<<20+1)

	_, created, err := state.store.PutIfAbsent(
		context.Background(), processingArtifactServiceKey(t, "oversized-single"), oversized,
	)
	require.Error(t, err)
	assert.False(t, created)
	saves, deletes, objects := state.files.snapshot()
	assert.Empty(t, saves)
	assert.Empty(t, deletes)
	assert.Empty(t, objects)

	batchStore := state.store.(interfaces.ProcessingArtifactBatchStore)
	_, err = batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		processingArtifactServiceKey(t, "oversized-batch"): oversized,
		processingArtifactServiceKey(t, "small-batch"):     []byte("small"),
	})
	require.Error(t, err)
	_, err = state.repository.Get(context.Background(), processingArtifactServiceKey(t, "small-batch"))
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	saves, deletes, objects = state.files.snapshot()
	assert.Empty(t, saves)
	assert.Empty(t, deletes)
	assert.Empty(t, objects)
}

func TestProcessingArtifactStoreAcceptsDefaultMaximumPayload(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	value := bytes.Repeat([]byte("x"), ProcessingArtifactMaxPayload)

	canonical, created, err := state.store.PutIfAbsent(
		context.Background(), processingArtifactServiceKey(t, "default-maximum"), value,
	)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, value, canonical)
	saves, _, _ := state.files.snapshot()
	assert.Len(t, saves, 1)
}

func TestProcessingArtifactStoreHonorsConfiguredMaximumPayload(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	store := NewProcessingArtifactStoreWithMaxPayload(
		state.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		state.files,
		2,
	)

	_, created, err := store.PutIfAbsent(
		context.Background(), processingArtifactServiceKey(t, "configured-maximum"), []byte("ok"),
	)
	require.NoError(t, err)
	assert.True(t, created)
	_, created, err = store.PutIfAbsent(
		context.Background(), processingArtifactServiceKey(t, "configured-oversized"), []byte("big"),
	)
	require.Error(t, err)
	assert.False(t, created)
}

func TestProcessingArtifactStoreInvalidationPreservesNewerWinner(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidation-newer-winner")
	observed := bytes.Repeat([]byte("o"), ProcessingArtifactInlineLimit+1)
	_, created, err := state.store.PutIfAbsent(context.Background(), key, observed)
	require.NoError(t, err)
	require.True(t, created)
	old, err := state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	oldObjectPath, ok := processingArtifactObjectPath(old.ObjectPath)
	require.True(t, ok)
	require.NoError(t, state.files.DeleteFile(context.Background(), oldObjectPath))
	require.NoError(t, state.repository.DeleteByID(context.Background(), key.TenantID, old.ID))

	healthy := bytes.Repeat([]byte("h"), ProcessingArtifactInlineLimit+2)
	_, created, err = state.store.PutIfAbsent(context.Background(), key, healthy)
	require.NoError(t, err)
	require.True(t, created)
	current, err := state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	currentObjectPath, ok := processingArtifactObjectPath(current.ObjectPath)
	require.True(t, ok)

	require.NoError(t, requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, observed))
	value, hit, err := state.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, healthy, value)
	_, deletes, objects := state.files.snapshot()
	assert.NotContains(t, deletes, currentObjectPath)
	assert.Contains(t, objects, currentObjectPath)
}

func TestProcessingArtifactStoreInvalidationTreatsMissingObjectAsCleaned(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidation-missing-object")
	path := "local://tenant-7/missing-object"
	state.files.deleteErrors[path] = fs.ErrNotExist
	seedProcessingArtifact(t, state.repository, key, nil, path, 6, artifactSHA([]byte("object")))

	require.NoError(t, requireProcessingArtifactInvalidator(t, state.store).Invalidate(context.Background(), key, []byte("object")))
	_, err := state.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}

func TestProcessingArtifactStoreInvalidationAllowsConcurrentObservedGeneration(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidation-concurrent")
	path := "local://tenant-7/concurrent-object"
	state.files.objects[path] = []byte("object")
	state.files.missingDeleteErr = true
	seedProcessingArtifact(t, state.repository, key, nil, path, 6, artifactSHA([]byte("object")))

	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	store := NewProcessingArtifactStore(
		&artifactInvalidationBarrierRepository{
			ProcessingArtifactRepository: state.repository,
			entered:                      entered,
			release:                      release,
		},
		&artifactTenantRepository{tenant: &types.Tenant{ID: key.TenantID}},
		state.files,
	)
	invalidator := requireProcessingArtifactInvalidator(t, store)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- invalidator.Invalidate(context.Background(), key, []byte("object"))
		}()
	}
	<-entered
	<-entered
	release <- struct{}{}
	release <- struct{}{}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}

	_, err := state.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, deletes, objects := state.files.snapshot()
	assert.Len(t, deletes, 2)
	assert.NotContains(t, objects, path)
}

func TestProcessingArtifactStoreInvalidationRepairsMissingObjectAfterManifestDeleteFailure(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "invalidation-manifest-repair")
	path := "local://tenant-7/manifest-repair"
	state.files.objects[path] = []byte("object")
	seedProcessingArtifact(t, state.repository, key, nil, path, 6, artifactSHA([]byte("object")))
	deleteErr := errors.New("delete manifest failed")
	store := NewProcessingArtifactStore(
		&artifactDeleteErrorRepository{ProcessingArtifactRepository: state.repository, err: deleteErr},
		&artifactTenantRepository{tenant: &types.Tenant{ID: key.TenantID}},
		state.files,
	)

	err := requireProcessingArtifactInvalidator(t, store).Invalidate(context.Background(), key, []byte("object"))
	assert.ErrorIs(t, err, deleteErr)
	_, _, objects := state.files.snapshot()
	assert.NotContains(t, objects, path)

	value, hit, err := state.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = state.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}
