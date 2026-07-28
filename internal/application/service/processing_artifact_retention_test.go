package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type processingArtifactRetainer interface {
	PurgeExpired(context.Context, time.Time, int) (types.ProcessingArtifactPurgeResult, error)
}

func requireProcessingArtifactRetainer(t *testing.T, store interfaces.ProcessingArtifactStore) processingArtifactRetainer {
	t.Helper()
	retainer, ok := store.(processingArtifactRetainer)
	require.True(t, ok, "processing artifact store must support retention")
	return retainer
}

func setProcessingArtifactCreatedAt(t *testing.T, state processingArtifactTestStore, artifact *types.ProcessingArtifact, createdAt time.Time) {
	t.Helper()
	require.NoError(t, state.db.Model(&types.ProcessingArtifact{}).
		Where("id = ?", artifact.ID).
		Update("created_at", createdAt).Error)
}

func TestProcessingArtifactRetentionDeletesInlineAndOwnedObject(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	inlineKey := processingArtifactServiceKey(t, "retention-inline")
	_, created, err := state.store.PutIfAbsent(context.Background(), inlineKey, []byte("inline"))
	require.NoError(t, err)
	require.True(t, created)
	objectKey := processingArtifactServiceKey(t, "retention-object")
	objectPath := "local://tenant-7/retention-object"
	state.files.objects[objectPath] = []byte("object")
	object := seedProcessingArtifact(t, state.repository, objectKey, nil, objectPath, 6, artifactSHA([]byte("object")))
	inlineManifest, err := state.repository.Get(context.Background(), inlineKey)
	require.NoError(t, err)
	setProcessingArtifactCreatedAt(t, state, inlineManifest, cutoff.Add(-time.Hour))
	setProcessingArtifactCreatedAt(t, state, object, cutoff.Add(-time.Hour))

	result, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(context.Background(), cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, types.ProcessingArtifactPurgeResult{Scanned: 2, Deleted: 2, DeletedBytes: 12}, result)
	_, err = state.repository.Get(context.Background(), inlineKey)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, err = state.repository.Get(context.Background(), objectKey)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, deletes, objects := state.files.snapshot()
	assert.Equal(t, []string{objectPath}, deletes)
	assert.NotContains(t, objects, objectPath)
}

func TestProcessingArtifactRetentionKeepsRetryStateForUnownedAndFailedObjects(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	nonAuthoritativeKey := processingArtifactServiceKey(t, "retention-unowned")
	nonAuthoritative := seedProcessingArtifact(t, state.repository, nonAuthoritativeKey, nil, "minio://historical/object", 4, artifactSHA([]byte("data")))
	failedObjectKey := processingArtifactServiceKey(t, "retention-object-failure")
	failedPath := "local://tenant-7/retention-object-failure"
	state.files.objects[failedPath] = []byte("data")
	state.files.deleteErrors[failedPath] = errors.New("delete failed")
	failedObject := seedProcessingArtifact(t, state.repository, failedObjectKey, nil, failedPath, 4, artifactSHA([]byte("data")))
	setProcessingArtifactCreatedAt(t, state, nonAuthoritative, cutoff.Add(-time.Hour))
	setProcessingArtifactCreatedAt(t, state, failedObject, cutoff.Add(-time.Hour))

	result, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(context.Background(), cutoff, 100)
	require.Error(t, err)
	assert.Equal(t, "ownership", processingArtifactRetentionFailureKind(err))
	assert.Equal(t, types.ProcessingArtifactPurgeResult{Scanned: 2, Failed: 2}, result)
	_, err = state.repository.Get(context.Background(), nonAuthoritativeKey)
	require.NoError(t, err)
	_, err = state.repository.Get(context.Background(), failedObjectKey)
	require.NoError(t, err)
}

func TestProcessingArtifactRetentionPreservesSameProviderPathWithoutArtifactOwnershipReference(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	key := processingArtifactServiceKey(t, "retention-unowned-local")
	path := "local://7/exports/user-document.bin"
	state.files.objects[path] = []byte("user document")
	artifact := seedProcessingArtifactReference(
		t,
		state.repository,
		key,
		nil,
		path,
		int64(len(state.files.objects[path])),
		artifactSHA(state.files.objects[path]),
	)
	setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))

	result, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(
		context.Background(), cutoff, 100,
	)

	require.ErrorContains(t, err, "not authoritatively owned")
	assert.Equal(t, types.ProcessingArtifactPurgeResult{Scanned: 1, Failed: 1}, result)
	_, manifestErr := state.repository.Get(context.Background(), key)
	require.NoError(t, manifestErr)
	_, deletes, objects := state.files.snapshot()
	assert.Empty(t, deletes)
	assert.Equal(t, []byte("user document"), objects[path])
}

func TestProcessingArtifactRetentionTreatsMissingObjectAsCleanedAndAdvancesPastFailure(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	failedKey := processingArtifactServiceKey(t, "retention-poison")
	failed := seedProcessingArtifact(t, state.repository, failedKey, []byte("failed"), "", 6, artifactSHA([]byte("failed")))
	missingKey := processingArtifactServiceKey(t, "retention-missing")
	missingPath := "local://tenant-7/retention-missing"
	state.files.missingDeleteErr = true
	missing := seedProcessingArtifact(t, state.repository, missingKey, nil, missingPath, 7, artifactSHA([]byte("missing")))
	goodKey := processingArtifactServiceKey(t, "retention-good")
	good := seedProcessingArtifact(t, state.repository, goodKey, []byte("good"), "", 4, artifactSHA([]byte("good")))
	for _, artifact := range []*types.ProcessingArtifact{failed, missing, good} {
		setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))
	}

	store := NewProcessingArtifactStore(
		&artifactDeleteErrorForIDRepository{ProcessingArtifactRepository: state.repository, id: failed.ID, err: errors.New("poison row")},
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}}, state.files,
	)
	result, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), cutoff, 1)
	require.Error(t, err)
	assert.Equal(t, "manifest_delete", processingArtifactRetentionFailureKind(err))
	assert.Equal(t, types.ProcessingArtifactPurgeResult{Scanned: 3, Deleted: 2, Failed: 1, DeletedBytes: 11}, result)
	_, err = state.repository.Get(context.Background(), failedKey)
	require.NoError(t, err)
	for _, key := range []types.ProcessingArtifactKey{missingKey, goodKey} {
		_, err = state.repository.Get(context.Background(), key)
		assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	}
	_, deletes, _ := state.files.snapshot()
	assert.Equal(t, []string{missingPath}, deletes)
}

type artifactDeleteErrorForIDRepository struct {
	interfaces.ProcessingArtifactRepository
	id  uint64
	err error
}

func (r *artifactDeleteErrorForIDRepository) DeleteByIDWithResult(ctx context.Context, tenantID, id uint64) (bool, error) {
	if id == r.id {
		return false, r.err
	}
	return r.ProcessingArtifactRepository.DeleteByIDWithResult(ctx, tenantID, id)
}

func TestProcessingArtifactRetentionRejectsInvalidBatchSize(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	_, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(context.Background(), time.Now(), 0)
	assert.Error(t, err)
}

func TestProcessingArtifactRetentionCachesTenantProviderResolversPerSweep(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tenants := &retentionTenantRepository{tenants: map[uint64]*types.Tenant{
		7: {ID: 7, StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "minio", MinIO: &types.MinIOEngineConfig{}}},
		8: {ID: 8, StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "local", Local: &types.LocalEngineConfig{}}},
	}}
	factoryCalls := make(map[string]int)
	store := newProcessingArtifactStore(state.repository, tenants, state.files, func(provider string, _ *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
		factoryCalls[provider]++
		return artifactMetadataFileService{FileService: state.files, provider: provider}, provider, nil
	})
	for index, candidate := range []struct {
		tenantID uint64
		path     string
	}{
		{7, "minio://tenant-7/first"},
		{7, "minio://tenant-7/second"},
		{8, "local://tenant-8/third"},
	} {
		key := processingArtifactServiceKey(t, fmt.Sprintf("retention-resolver-%d", index))
		key.TenantID = candidate.tenantID
		state.files.objects[candidate.path] = []byte("value")
		artifact := seedProcessingArtifact(t, state.repository, key, nil, candidate.path, 5, artifactSHA([]byte("value")))
		setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))
	}

	result, err := store.PurgeExpired(context.Background(), cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), result.Deleted)
	assert.Equal(t, map[string]int{"minio": 1, "local": 1}, factoryCalls)
	assert.Equal(t, map[uint64]int{7: 1, 8: 1}, tenants.calls)
}

func TestProcessingArtifactRetentionSeparatesBackendsForSameProvider(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	backendA := newArtifactFakeFileService()
	backendB := newArtifactFakeFileService()
	defaultBackendID := "backend-b"
	tenant := &types.Tenant{ID: 7, DefaultStorageBackendID: &defaultBackendID}
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
	for index, candidate := range []struct {
		backendID string
		service   *artifactFakeFileService
	}{
		{backendID: "backend-a", service: backendA},
		{backendID: "backend-b", service: backendB},
	} {
		key := processingArtifactServiceKey(t, fmt.Sprintf("retention-backend-%d", index))
		path := fmt.Sprintf("cos://shared/%d", index)
		candidate.service.objects[path] = []byte("value")
		artifact := seedProcessingArtifact(
			t,
			state.repository,
			key,
			nil,
			types.BuildStorageBackendPath(candidate.backendID, path),
			5,
			artifactSHA([]byte("value")),
		)
		setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))
	}

	result, err := store.PurgeExpired(context.Background(), cutoff, 1)

	require.NoError(t, err)
	assert.Equal(t, uint64(2), result.Deleted)
	_, deletesA, _ := backendA.snapshot()
	_, deletesB, _ := backendB.snapshot()
	assert.Equal(t, []string{"cos://shared/0"}, deletesA)
	assert.Equal(t, []string{"cos://shared/1"}, deletesB)
}

func TestProcessingArtifactRetentionCachesResolverFailuresPerSweep(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tenants := &retentionTenantRepository{err: errors.New("tenant lookup failed")}
	store := newProcessingArtifactStore(state.repository, tenants, state.files, nil)
	for index := 0; index < 2; index++ {
		key := processingArtifactServiceKey(t, fmt.Sprintf("retention-resolver-error-%d", index))
		artifact := seedProcessingArtifact(t, state.repository, key, nil, fmt.Sprintf("minio://tenant-7/%d", index), 1, artifactSHA([]byte("x")))
		setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))
	}

	result, err := store.PurgeExpired(context.Background(), cutoff, 1)
	require.Error(t, err)
	assert.Equal(t, "storage_resolve", processingArtifactRetentionFailureKind(err))
	assert.Equal(t, uint64(2), result.Failed)
	assert.Equal(t, map[uint64]int{7: 1}, tenants.calls)
}

func TestProcessingArtifactRetentionBoundsErrorPayload(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 10; index++ {
		key := processingArtifactServiceKey(t, fmt.Sprintf("retention-error-%d", index))
		artifact := seedProcessingArtifact(t, state.repository, key, nil, fmt.Sprintf("minio://historical/%d", index), 1, artifactSHA([]byte("x")))
		setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))
	}

	result, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(context.Background(), cutoff, 2)
	require.Error(t, err)
	assert.Equal(t, uint64(10), result.Failed)
	assert.Equal(t, 1, strings.Count(err.Error(), "not authoritatively owned"))
}

func TestProcessingArtifactRetentionStopsOnNilManifestPage(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	repository := &retentionNilPageRepository{ProcessingArtifactRepository: state.repository}
	store := NewProcessingArtifactStore(repository, &artifactTenantRepository{tenant: &types.Tenant{ID: 7}}, state.files)

	result, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), time.Now(), 1)
	require.Error(t, err)
	assert.Equal(t, "manifest_invalid", processingArtifactRetentionFailureKind(err))
	assert.Equal(t, uint64(1), result.Failed)
	assert.Equal(t, 1, repository.calls)
}

func TestProcessingArtifactRetentionClassifiesManifestListFailure(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	repository := &retentionListErrorRepository{
		ProcessingArtifactRepository: state.repository,
		err:                          errors.New("list failed"),
	}
	store := NewProcessingArtifactStore(
		repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		state.files,
	)

	_, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), time.Now(), 1)

	require.Error(t, err)
	assert.Equal(t, "manifest_list", processingArtifactRetentionFailureKind(err))
}

func TestProcessingArtifactRetentionClassifiesObjectDeleteFailure(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	key := processingArtifactServiceKey(t, "retention-object-delete-failure")
	path := "local://tenant-7/retention-object-delete-failure"
	state.files.objects[path] = []byte("data")
	state.files.deleteErrors[path] = errors.New("delete failed")
	artifact := seedProcessingArtifact(t, state.repository, key, nil, path, 4, artifactSHA([]byte("data")))
	setProcessingArtifactCreatedAt(t, state, artifact, cutoff.Add(-time.Hour))

	_, err := requireProcessingArtifactRetainer(t, state.store).PurgeExpired(context.Background(), cutoff, 1)

	require.Error(t, err)
	assert.Equal(t, "object_delete", processingArtifactRetentionFailureKind(err))
}

func TestProcessingArtifactRetentionContinuesPastNilManifestInPage(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "retention-nil-mixed")
	artifact := seedProcessingArtifact(t, state.repository, key, []byte("valid"), "", 5, artifactSHA([]byte("valid")))
	repository := &retentionMixedPageRepository{
		ProcessingArtifactRepository: state.repository,
		artifacts:                    []*types.ProcessingArtifact{nil, artifact},
	}
	store := NewProcessingArtifactStore(repository, &artifactTenantRepository{tenant: &types.Tenant{ID: 7}}, state.files)

	result, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), time.Now(), 2)
	require.Error(t, err)
	assert.Equal(t, types.ProcessingArtifactPurgeResult{Scanned: 1, Deleted: 1, Failed: 1, DeletedBytes: 5}, result)
	assert.Equal(t, 2, repository.calls)
	_, err = state.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}

func TestProcessingArtifactRetentionPreservesConcurrentReplacementByOldID(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	key := processingArtifactServiceKey(t, "retention-replacement")
	old := seedProcessingArtifact(t, state.repository, key, []byte("old"), "", 3, artifactSHA([]byte("old")))
	setProcessingArtifactCreatedAt(t, state, old, cutoff.Add(-time.Hour))
	replacement := &types.ProcessingArtifact{
		TenantID: key.TenantID, Stage: key.Stage, KeyVersion: key.KeyVersion, InputFingerprint: key.InputFingerprint,
		Payload: []byte("new"), ContentSHA256: artifactSHA([]byte("new")), SizeBytes: 3,
	}
	store := NewProcessingArtifactStore(
		&retentionReplacementRepository{ProcessingArtifactRepository: state.repository, oldID: old.ID, replacement: replacement},
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}}, state.files,
	)

	result, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), result.Deleted)
	current, err := state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), current.Payload)
}

func TestProcessingArtifactRetentionRecordsEvictedAndErrorCounters(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	cutoff := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	registry := NewProcessingArtifactCounterRegistry()
	store := NewProcessingArtifactStoreWithMaxPayloadAndCounterRegistry(
		state.repository, &artifactTenantRepository{tenant: &types.Tenant{ID: 7}}, state.files, ProcessingArtifactMaxPayload, registry,
	)
	goodKey := processingArtifactServiceKey(t, "retention-counter-good")
	good := seedProcessingArtifact(t, state.repository, goodKey, []byte("good"), "", 4, artifactSHA([]byte("good")))
	badKey := processingArtifactServiceKey(t, "retention-counter-bad")
	bad := seedProcessingArtifact(t, state.repository, badKey, nil, "minio://historical/counter", 1, artifactSHA([]byte("x")))
	setProcessingArtifactCreatedAt(t, state, good, cutoff.Add(-time.Hour))
	setProcessingArtifactCreatedAt(t, state, bad, cutoff.Add(-time.Hour))

	_, err := requireProcessingArtifactRetainer(t, store).PurgeExpired(context.Background(), cutoff, 100)
	require.Error(t, err)
	counts := make(map[string]uint64)
	for _, counter := range registry.Snapshot() {
		counts[counter.Outcome] += counter.Count
	}
	assert.Equal(t, uint64(1), counts["evicted"])
	assert.Equal(t, uint64(1), counts["error"])
}

type retentionTenantRepository struct {
	interfaces.TenantRepository
	tenants map[uint64]*types.Tenant
	calls   map[uint64]int
	err     error
}

func (r *retentionTenantRepository) GetTenantByID(_ context.Context, tenantID uint64) (*types.Tenant, error) {
	if r.calls == nil {
		r.calls = make(map[uint64]int)
	}
	r.calls[tenantID]++
	if r.err != nil {
		return nil, r.err
	}
	return r.tenants[tenantID], nil
}

type retentionNilPageRepository struct {
	interfaces.ProcessingArtifactRepository
	calls int
}

type retentionListErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r *retentionListErrorRepository) ListExpired(
	context.Context,
	time.Time,
	uint64,
	int,
) ([]*types.ProcessingArtifact, error) {
	return nil, r.err
}

type retentionMixedPageRepository struct {
	interfaces.ProcessingArtifactRepository
	artifacts []*types.ProcessingArtifact
	calls     int
}

func (r *retentionMixedPageRepository) ListExpired(context.Context, time.Time, uint64, int) ([]*types.ProcessingArtifact, error) {
	r.calls++
	if r.calls == 1 {
		return r.artifacts, nil
	}
	return nil, nil
}

func (r *retentionNilPageRepository) ListExpired(context.Context, time.Time, uint64, int) ([]*types.ProcessingArtifact, error) {
	r.calls++
	if r.calls == 1 {
		return []*types.ProcessingArtifact{nil}, nil
	}
	return nil, errors.New("unexpected repeated expired-artifact page")
}

type retentionReplacementRepository struct {
	interfaces.ProcessingArtifactRepository
	oldID       uint64
	replacement *types.ProcessingArtifact
	replaced    bool
}

func (r *retentionReplacementRepository) DeleteByIDWithResult(ctx context.Context, tenantID, id uint64) (bool, error) {
	if id != r.oldID || r.replaced {
		return r.ProcessingArtifactRepository.DeleteByIDWithResult(ctx, tenantID, id)
	}
	r.replaced = true
	if _, err := r.ProcessingArtifactRepository.DeleteByIDWithResult(ctx, tenantID, id); err != nil {
		return false, err
	}
	_, err := r.ProcessingArtifactRepository.PutIfAbsent(ctx, r.replacement)
	return false, err
}
