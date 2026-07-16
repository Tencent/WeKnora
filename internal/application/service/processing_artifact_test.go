package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const processingArtifactServiceTestDDL = `
CREATE TABLE processing_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    stage VARCHAR(64) NOT NULL,
    key_version INTEGER NOT NULL,
    input_fingerprint CHAR(64) NOT NULL,
    payload BLOB NULL,
    object_path TEXT NOT NULL DEFAULT '',
    content_sha256 CHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, stage, key_version, input_fingerprint)
);`

type artifactFakeFileService struct {
	mu           sync.Mutex
	objects      map[string][]byte
	getErrors    map[string]error
	streamErrors map[string]error
	closeErrors  map[string]error
	readers      map[string]io.Reader
	saves        []string
	deletes      []string
	events       []string
	savePath     string
	saveStarted  chan struct{}
	saveRelease  chan struct{}
}

type artifactMetadataFileService struct {
	interfaces.FileService
	provider       string
	canonicalPaths map[string]string
	servicePaths   map[string]string
}

func (s artifactMetadataFileService) StorageProvider() string { return s.provider }

func (s artifactMetadataFileService) CanonicalStoredPath(path string) string {
	if mapped := s.canonicalPaths[path]; mapped != "" {
		return mapped
	}
	return path
}

func (s artifactMetadataFileService) ServiceStoredPath(path string) string {
	if mapped := s.servicePaths[path]; mapped != "" {
		return mapped
	}
	return path
}

type artifactFileServiceWithoutMetadata struct {
	interfaces.FileService
}

func newArtifactFakeFileService() *artifactFakeFileService {
	return &artifactFakeFileService{
		objects:      make(map[string][]byte),
		getErrors:    make(map[string]error),
		streamErrors: make(map[string]error),
		closeErrors:  make(map[string]error),
		readers:      make(map[string]io.Reader),
	}
}

func (f *artifactFakeFileService) StorageProvider() string { return "local" }

func (f *artifactFakeFileService) CheckConnectivity(context.Context) error { return nil }

func (f *artifactFakeFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", errors.New("unexpected SaveFile call")
}

func (f *artifactFakeFileService) SaveBytes(
	_ context.Context,
	data []byte,
	tenantID uint64,
	fileName string,
	_ bool,
) (string, error) {
	f.mu.Lock()
	path := fmt.Sprintf("local://tenant-%d/artifact-%d-%s", tenantID, len(f.saves)+1, fileName)
	if f.savePath != "" {
		path = f.savePath
	} else {
		f.objects[path] = bytes.Clone(data)
	}
	f.saves = append(f.saves, path)
	started, release := f.saveStarted, f.saveRelease
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return path, nil
}

func (f *artifactFakeFileService) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.getErrors[path]; err != nil {
		return nil, err
	}
	var reader io.Reader
	if configured := f.readers[path]; configured != nil {
		reader = configured
	} else if err := f.streamErrors[path]; err != nil {
		reader = artifactErrorReader{err: err}
	} else {
		data, ok := f.objects[path]
		if !ok {
			return nil, fs.ErrNotExist
		}
		reader = bytes.NewReader(bytes.Clone(data))
	}
	return &artifactTrackedReadCloser{
		Reader:   reader,
		closeErr: f.closeErrors[path],
		onClose: func() {
			f.mu.Lock()
			f.events = append(f.events, "close:"+path)
			f.mu.Unlock()
		},
	}, nil
}

type artifactErrorReader struct{ err error }

func (r artifactErrorReader) Read([]byte) (int, error) { return 0, r.err }

type artifactTrackedReadCloser struct {
	io.Reader
	closeErr error
	onClose  func()
}

func (r *artifactTrackedReadCloser) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}

func (f *artifactFakeFileService) GetFileURL(context.Context, string) (string, error) {
	return "", errors.New("unexpected GetFileURL call")
}

func (f *artifactFakeFileService) DeleteFile(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, path)
	f.events = append(f.events, "delete:"+path)
	delete(f.objects, path)
	return nil
}

func (f *artifactFakeFileService) eventSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *artifactFakeFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", errors.New("unexpected CopyFile call")
}

func (f *artifactFakeFileService) snapshot() (saves, deletes []string, objects map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	objects = make(map[string][]byte, len(f.objects))
	for path, data := range f.objects {
		objects[path] = bytes.Clone(data)
	}
	return append([]string(nil), f.saves...), append([]string(nil), f.deletes...), objects
}

type artifactTenantRepository struct {
	interfaces.TenantRepository
	mu       sync.Mutex
	tenant   *types.Tenant
	err      error
	getCalls int
}

type corruptWinnerRepository struct {
	interfaces.ProcessingArtifactRepository
	mu       sync.Mutex
	attempts int
}

type artifactPutBarrierRepository struct {
	interfaces.ProcessingArtifactRepository
	arrived chan struct{}
	release <-chan struct{}
}

func (r *artifactPutBarrierRepository) PutIfAbsent(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
) (bool, error) {
	r.arrived <- struct{}{}
	<-r.release
	return r.ProcessingArtifactRepository.PutIfAbsent(ctx, artifact)
}

type artifactPutErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r *artifactPutErrorRepository) PutIfAbsent(context.Context, *types.ProcessingArtifact) (bool, error) {
	return false, r.err
}

type artifactGetErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r *artifactGetErrorRepository) Get(context.Context, types.ProcessingArtifactKey) (*types.ProcessingArtifact, error) {
	return nil, r.err
}

type artifactDeleteErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r *artifactDeleteErrorRepository) DeleteByID(context.Context, uint64, uint64) error {
	return r.err
}

type artifactCountingReader struct {
	mu    sync.Mutex
	count int
}

func (r *artifactCountingReader) Read(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range data {
		data[i] = 'x'
	}
	r.count += len(data)
	return len(data), nil
}

func (r *artifactCountingReader) bytesRead() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *corruptWinnerRepository) PutIfAbsent(
	ctx context.Context,
	candidate *types.ProcessingArtifact,
) (bool, error) {
	r.mu.Lock()
	r.attempts++
	r.mu.Unlock()
	corrupt := *candidate
	corrupt.Payload = []byte("corrupt-winner")
	corrupt.ObjectPath = ""
	corrupt.SizeBytes = int64(len(corrupt.Payload))
	corrupt.ContentSHA256 = artifactSHA([]byte("different"))
	_, err := r.ProcessingArtifactRepository.PutIfAbsent(ctx, &corrupt)
	return false, err
}

func (r *corruptWinnerRepository) attemptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

func (r *artifactTenantRepository) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getCalls++
	return r.tenant, r.err
}

func (r *artifactTenantRepository) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getCalls
}

type processingArtifactTestStore struct {
	store      interfaces.ProcessingArtifactStore
	repository interfaces.ProcessingArtifactRepository
	db         *gorm.DB
	files      *artifactFakeFileService
}

func newProcessingArtifactTestStore(t *testing.T) processingArtifactTestStore {
	t.Helper()
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "processing-artifacts.db"))
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", dbPath)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec(processingArtifactServiceTestDDL).Error)

	repo := repository.NewProcessingArtifactRepository(db)
	files := newArtifactFakeFileService()
	tenantRepo := &artifactTenantRepository{tenant: &types.Tenant{ID: 7}}
	return processingArtifactTestStore{
		store:      NewProcessingArtifactStore(repo, tenantRepo, files),
		repository: repo,
		db:         db,
		files:      files,
	}
}

func processingArtifactServiceKey(t *testing.T, suffix string) types.ProcessingArtifactKey {
	t.Helper()
	key, err := types.NewProcessingArtifactKey(7, "chunking", 1, []byte(suffix))
	require.NoError(t, err)
	return key
}

func artifactSHA(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func seedProcessingArtifact(
	t *testing.T,
	repo interfaces.ProcessingArtifactRepository,
	key types.ProcessingArtifactKey,
	payload []byte,
	objectPath string,
	size int64,
	hash string,
) *types.ProcessingArtifact {
	t.Helper()
	row := &types.ProcessingArtifact{
		TenantID:         key.TenantID,
		Stage:            key.Stage,
		KeyVersion:       key.KeyVersion,
		InputFingerprint: key.InputFingerprint,
		Payload:          bytes.Clone(payload),
		ObjectPath:       objectPath,
		ContentSHA256:    hash,
		SizeBytes:        size,
	}
	created, err := repo.PutIfAbsent(context.Background(), row)
	require.NoError(t, err)
	require.True(t, created)
	return row
}

func TestProcessingArtifactStoreKeepsSmallValuesInline(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "inline-limit")
	want := bytes.Repeat([]byte("a"), ProcessingArtifactInlineLimit)

	canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, want)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, want, canonical)
	saves, _, _ := testStore.files.snapshot()
	assert.Empty(t, saves)

	row, err := testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, want, row.Payload)
	assert.Empty(t, row.ObjectPath)
	assert.Equal(t, int64(len(want)), row.SizeBytes)
	assert.Equal(t, artifactSHA(want), row.ContentSHA256)

	canonical[0] = 'x'
	got, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, want, got)
	got[0] = 'y'
	again, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, want, again)
}

func TestProcessingArtifactStoreBatchOperationsPreserveCanonicalValues(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	batchStore, ok := state.store.(interface {
		GetMany(context.Context, []types.ProcessingArtifactKey) (
			map[types.ProcessingArtifactKey][]byte, error,
		)
		PutManyIfAbsent(context.Context, map[types.ProcessingArtifactKey][]byte) (
			map[types.ProcessingArtifactKey][]byte, error,
		)
	})
	require.True(t, ok)

	winnerKey := processingArtifactServiceKey(t, "batch-winner")
	newKey := processingArtifactServiceKey(t, "batch-new")
	missingKey := processingArtifactServiceKey(t, "batch-missing")
	_, created, err := state.store.PutIfAbsent(context.Background(), winnerKey, []byte("winner"))
	require.NoError(t, err)
	require.True(t, created)

	canonical, err := batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		winnerKey: []byte("loser"),
		newKey:    []byte("new-value"),
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("winner"), canonical[winnerKey])
	assert.Equal(t, []byte("new-value"), canonical[newKey])

	got, err := batchStore.GetMany(
		context.Background(),
		[]types.ProcessingArtifactKey{winnerKey, newKey, missingKey},
	)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []byte("winner"), got[winnerKey])
	assert.Equal(t, []byte("new-value"), got[newKey])
	assert.NotContains(t, got, missingKey)
}

func TestProcessingArtifactStoreBatchRepairsCorruptWinner(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	batchStore := state.store.(interfaces.ProcessingArtifactBatchStore)
	key := processingArtifactServiceKey(t, "batch-corrupt-winner")
	seedProcessingArtifact(
		t,
		state.repository,
		key,
		[]byte("corrupt"),
		"",
		int64(len("corrupt")),
		artifactSHA([]byte("different")),
	)

	canonical, err := batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		key: []byte("replacement"),
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("replacement"), canonical[key])

	artifact, err := state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, []byte("replacement"), artifact.Payload)
	assert.Equal(t, artifactSHA([]byte("replacement")), artifact.ContentSHA256)
}

func TestProcessingArtifactStoreBatchFallsBackForLargeValues(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	batchStore := state.store.(interfaces.ProcessingArtifactBatchStore)
	key := processingArtifactServiceKey(t, "batch-large-value")
	value := bytes.Repeat([]byte("x"), ProcessingArtifactInlineLimit+1)

	canonical, err := batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		key: value,
	})
	require.NoError(t, err)
	assert.Equal(t, value, canonical[key])

	artifact, err := state.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Nil(t, artifact.Payload)
	assert.NotEmpty(t, artifact.ObjectPath)
	saves, deletes, _ := state.files.snapshot()
	assert.Len(t, saves, 1)
	assert.Empty(t, deletes)
}

func TestProcessingArtifactStoreKeepsEmptyValueInline(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "empty-inline")

	canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, nil)
	require.NoError(t, err)
	require.True(t, created)
	assert.NotNil(t, canonical)
	assert.Empty(t, canonical)

	row, err := testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.NotNil(t, row.Payload)
	value, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.NotNil(t, value)
	assert.Empty(t, value)
}

func TestProcessingArtifactStoreStoresLargeValuesByManifest(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "limit-plus-one")
	want := bytes.Repeat([]byte("b"), ProcessingArtifactInlineLimit+1)

	canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, want)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, want, canonical)

	saves, _, objects := testStore.files.snapshot()
	require.Len(t, saves, 1)
	assert.Equal(t, want, objects[saves[0]])
	row, err := testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Nil(t, row.Payload)
	assert.Equal(t, saves[0], row.ObjectPath)
	assert.Equal(t, int64(len(want)), row.SizeBytes)
	assert.Equal(t, artifactSHA(want), row.ContentSHA256)
	assert.Contains(t, filepath.Base(row.ObjectPath), artifactSHA(want))
}

func TestProcessingArtifactStoreConcurrentDifferentValuesReturnWinner(t *testing.T) {
	for _, tc := range []struct {
		name  string
		large bool
	}{
		{name: "inline"},
		{name: "large", large: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			putRelease := make(chan struct{})
			barrierRepository := &artifactPutBarrierRepository{
				ProcessingArtifactRepository: testStore.repository,
				arrived:                      make(chan struct{}, 2),
				release:                      putRelease,
			}
			testStore.store = NewProcessingArtifactStore(
				barrierRepository,
				&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
				testStore.files,
			)
			key := processingArtifactServiceKey(t, "concurrent-"+tc.name)
			values := [][]byte{[]byte("candidate-one"), []byte("candidate-two")}
			if tc.large {
				values = [][]byte{
					bytes.Repeat([]byte("c"), ProcessingArtifactInlineLimit+1),
					bytes.Repeat([]byte("d"), ProcessingArtifactInlineLimit+2),
				}
				release := make(chan struct{})
				testStore.files.saveStarted = make(chan struct{}, 2)
				testStore.files.saveRelease = release
				t.Cleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
				})
			}

			type result struct {
				canonical []byte
				created   bool
				err       error
			}
			start := make(chan struct{})
			results := make(chan result, len(values))
			var ready sync.WaitGroup
			ready.Add(len(values))
			for _, value := range values {
				value := bytes.Clone(value)
				go func() {
					ready.Done()
					<-start
					canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, value)
					results <- result{canonical: canonical, created: created, err: err}
				}()
			}
			ready.Wait()
			close(start)
			if tc.large {
				<-testStore.files.saveStarted
				<-testStore.files.saveStarted
				close(testStore.files.saveRelease)
			}
			<-barrierRepository.arrived
			<-barrierRepository.arrived
			close(putRelease)

			got := make([]result, 0, len(values))
			for range values {
				got = append(got, <-results)
			}
			createdCount := 0
			for _, result := range got {
				require.NoError(t, result.err)
				if result.created {
					createdCount++
				}
				require.Equal(t, got[0].canonical, result.canonical)
			}
			require.Equal(t, 1, createdCount)
			assert.True(t, bytes.Equal(got[0].canonical, values[0]) || bytes.Equal(got[0].canonical, values[1]))

			got[0].canonical[0] ^= 0xff
			stored, hit, err := testStore.store.Get(context.Background(), key)
			require.NoError(t, err)
			require.True(t, hit)
			assert.Equal(t, got[1].canonical, stored)

			if tc.large {
				saves, deletes, objects := testStore.files.snapshot()
				require.Len(t, saves, 2)
				require.Len(t, deletes, 1)
				row, err := testStore.repository.Get(context.Background(), key)
				require.NoError(t, err)
				assert.Equal(t, deletes[0], func() string {
					if saves[0] == row.ObjectPath {
						return saves[1]
					}
					return saves[0]
				}())
				assert.Contains(t, objects, row.ObjectPath)
				assert.NotContains(t, objects, deletes[0])
			}
		})
	}
}

func TestProcessingArtifactStoreEvictsCorruptInlineValue(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "corrupt-inline")
	seedProcessingArtifact(t, testStore.repository, key, []byte("bad"), "", 99, artifactSHA([]byte("other")))

	value, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)

	want := []byte("repaired")
	canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, want)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, want, canonical)
}

func TestProcessingArtifactStoreEvictsMissingOrCorruptObject(t *testing.T) {
	for _, tc := range []struct {
		name          string
		missing       bool
		missingStream bool
	}{
		{name: "missing", missing: true},
		{name: "missing while reading", missingStream: true},
		{name: "wrong hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			key := processingArtifactServiceKey(t, "corrupt-object-"+tc.name)
			path := "local://tenant-7/old-object"
			expected := []byte("expected")
			if tc.missingStream {
				testStore.files.streamErrors[path] = fs.ErrNotExist
			} else if !tc.missing {
				testStore.files.objects[path] = []byte("wrong")
			}
			seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(expected)), artifactSHA(expected))

			value, hit, err := testStore.store.Get(context.Background(), key)
			require.NoError(t, err)
			assert.False(t, hit)
			assert.Nil(t, value)
			_, err = testStore.repository.Get(context.Background(), key)
			assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
			_, deletes, _ := testStore.files.snapshot()
			assert.Equal(t, []string{path}, deletes)
			if tc.missingStream {
				assert.Equal(t, []string{"close:" + path, "delete:" + path}, testStore.files.eventSnapshot())
			}
		})
	}
}

func TestProcessingArtifactStoreKeepsManifestOnTransientReadError(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		name := "get file"
		if duringRead {
			name = "read"
		}
		t.Run(name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			key := processingArtifactServiceKey(t, "transient-"+name)
			path := "local://tenant-7/transient"
			transportErr := errors.New("storage transport unavailable")
			if duringRead {
				testStore.files.streamErrors[path] = transportErr
			} else {
				testStore.files.getErrors[path] = transportErr
			}
			seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))

			value, hit, err := testStore.store.Get(context.Background(), key)
			assert.ErrorIs(t, err, transportErr)
			assert.False(t, hit)
			assert.Nil(t, value)
			_, err = testStore.repository.Get(context.Background(), key)
			require.NoError(t, err)
			_, deletes, _ := testStore.files.snapshot()
			assert.Empty(t, deletes)
		})
	}
}

func TestProcessingArtifactStoreUsesMatchingContextTenantBeforeRepository(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	tenantRepo := &artifactTenantRepository{err: errors.New("tenant lookup must not run")}
	store := NewProcessingArtifactStore(testStore.repository, tenantRepo, testStore.files)
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 7})
	key := processingArtifactServiceKey(t, "context-tenant")
	want := bytes.Repeat([]byte("e"), ProcessingArtifactInlineLimit+1)

	canonical, created, err := store.PutIfAbsent(ctx, key, want)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, want, canonical)
	assert.Zero(t, tenantRepo.calls())
}

func TestProcessingArtifactStoreInfersReadProviderFromObjectPath(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	localConfig := &types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{},
	}
	localService, _, err := filesvc.NewFileServiceFromStorageConfig("local", localConfig, baseDir)
	require.NoError(t, err)
	want := []byte("historical-object")
	path, err := localService.SaveBytes(context.Background(), want, 7, "processing-artifact-history.bin", false)
	require.NoError(t, err)

	key := processingArtifactServiceKey(t, "provider-change")
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(want)), artifactSHA(want))
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "minio",
	}}
	store := NewProcessingArtifactStore(testStore.repository, &artifactTenantRepository{tenant: tenant}, testStore.files)

	got, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, want, got)
	_, _, fallbackObjects := testStore.files.snapshot()
	assert.Empty(t, fallbackObjects)
}

func TestProcessingArtifactStoreFallsBackForEmptyTenantStorageConfig(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{}}},
		testStore.files,
	)
	want := bytes.Repeat([]byte("f"), ProcessingArtifactInlineLimit+1)

	canonical, created, err := store.PutIfAbsent(
		context.Background(),
		processingArtifactServiceKey(t, "empty-storage-config"),
		want,
	)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, want, canonical)
	saves, _, _ := testStore.files.snapshot()
	assert.Len(t, saves, 1)
}

func TestProcessingArtifactStoreUsesConfiguredTenantDefaultForWrites(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{},
	}}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		testStore.files,
	)
	key := processingArtifactServiceKey(t, "configured-write-provider")
	want := bytes.Repeat([]byte("p"), ProcessingArtifactInlineLimit+1)

	canonical, created, err := store.PutIfAbsent(context.Background(), key, want)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, want, canonical)
	fallbackSaves, _, _ := testStore.files.snapshot()
	assert.Empty(t, fallbackSaves)
	row, err := testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(row.ObjectPath, "local://"))
	got, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, want, got)
}

func TestProcessingArtifactStoreFallsBackWhenInferredProviderIsMissingFromConfig(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "missing-inferred-provider")
	path := "minio://historical-bucket/artifact"
	want := []byte("historical-global-object")
	testStore.files.objects[path] = bytes.Clone(want)
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(want)), artifactSHA(want))
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{},
	}}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		testStore.files,
	)

	got, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, want, got)
}

func TestProcessingArtifactStoreKeepsManifestWhenGlobalFallbackCannotReadInferredProvider(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "historical-minio-global-local")
	path := "minio://historical-bucket/artifact"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "local",
			Local:           &types.LocalEngineConfig{},
		}}},
		testStore.files,
	)

	value, hit, err := store.Get(context.Background(), key)

	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	_, deletes, _ := testStore.files.snapshot()
	assert.Empty(t, deletes)
}

func TestProcessingArtifactStoreGlobalFallbackAuthorityUsesAdapterProvider(t *testing.T) {
	for name, config := range map[string]*types.StorageEngineConfig{
		"nil config":   nil,
		"empty config": {},
	} {
		t.Run(name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			key := processingArtifactServiceKey(t, "global-local-minio-mismatch-"+name)
			path := "minio://historical-bucket/artifact"
			seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
			global := artifactMetadataFileService{FileService: testStore.files, provider: "local"}
			store := NewProcessingArtifactStore(
				testStore.repository,
				&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: config}},
				global,
			)

			value, hit, err := store.Get(context.Background(), key)

			assert.ErrorIs(t, err, fs.ErrNotExist)
			assert.False(t, hit)
			assert.Nil(t, value)
			_, err = testStore.repository.Get(context.Background(), key)
			require.NoError(t, err)
			_, deletes, _ := testStore.files.snapshot()
			assert.Empty(t, deletes)
		})
	}
}

func TestProcessingArtifactStoreMatchingGlobalProviderEvictsMissingObject(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "global-minio-match")
	path := "minio://historical-bucket/artifact"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	global := artifactMetadataFileService{FileService: testStore.files, provider: "minio"}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		global,
	)

	value, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, deletes, _ := testStore.files.snapshot()
	assert.Equal(t, []string{path}, deletes)
}

func TestProcessingArtifactStoreUnknownGlobalProviderKeepsInferredManifest(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "unknown-global-minio")
	path := "minio://historical-bucket/artifact"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	global := artifactFileServiceWithoutMetadata{FileService: testStore.files}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		global,
	)

	value, hit, err := store.Get(context.Background(), key)

	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
}

func TestProcessingArtifactStoreDoesNotAssignGenericHTTPSPathToGlobalProvider(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "generic-https-global")
	path := "https://files.example.com/object.bin"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	global := artifactMetadataFileService{FileService: testStore.files, provider: "local"}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		global,
	)

	value, hit, err := store.Get(context.Background(), key)

	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
}

func TestProcessingArtifactStoreAdapterMappingProvesProxyPathOwnership(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "global-obs-proxy-ownership")
	proxyPath := "https://files.example.com/obs/weknora/7/object.bin"
	canonicalPath := "obs://global-bucket/weknora/7/object.bin"
	seedProcessingArtifact(t, testStore.repository, key, nil, proxyPath, 10, artifactSHA([]byte("0123456789")))
	global := artifactMetadataFileService{
		FileService:    testStore.files,
		provider:       "obs",
		canonicalPaths: map[string]string{proxyPath: canonicalPath},
	}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		global,
	)

	value, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}

func TestProcessingArtifactStoreOwnershipUsesCanonicalGenericScheme(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		provider       string
		canonicalPaths map[string]string
		authoritative  bool
	}{
		{name: "custom URI through local", path: "custom://bucket/object", provider: "local"},
		{name: "mixed-case custom URI through custom", path: "CuStOm://bucket/object", provider: "custom", authoritative: true},
		{name: "dummy URI through local", path: "dummy://object", provider: "local"},
		{name: "dummy URI through dummy", path: "dummy://object", provider: "dummy", authoritative: true},
		{name: "bare path through minio", path: "legacy/object.bin", provider: "minio"},
		{name: "bare path through custom", path: "legacy/object.bin", provider: "custom"},
		{name: "bare path through local", path: "legacy/object.bin", provider: "local", authoritative: true},
		{name: "absolute path through local", path: "/data/files/object.bin", provider: "local", authoritative: true},
		{name: "Windows path through local", path: `C:\data\files\object.bin`, provider: "local", authoritative: true},
		{name: "local URI through local", path: "local://tenant/object.bin", provider: "local", authoritative: true},
		{name: "COS HTTPS through cos", path: "https://bucket.cos.ap-guangzhou.myqcloud.com/object.bin", provider: "cos"},
		{
			name:           "OBS HTTPS mapped to OBS URI",
			path:           "https://files.example.com/obs/object.bin",
			provider:       "obs",
			canonicalPaths: map[string]string{"https://files.example.com/obs/object.bin": "obs://bucket/object.bin"},
			authoritative:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			key := processingArtifactServiceKey(t, "generic-ownership-"+tt.name)
			seedProcessingArtifact(t, testStore.repository, key, nil, tt.path, 10, artifactSHA([]byte("0123456789")))
			global := artifactMetadataFileService{
				FileService:    testStore.files,
				provider:       tt.provider,
				canonicalPaths: tt.canonicalPaths,
			}
			store := NewProcessingArtifactStore(
				testStore.repository,
				&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
				global,
			)

			value, hit, err := store.Get(context.Background(), key)

			assert.False(t, hit)
			assert.Nil(t, value)
			_, manifestErr := testStore.repository.Get(context.Background(), key)
			if tt.authoritative {
				require.NoError(t, err)
				assert.ErrorIs(t, manifestErr, types.ErrProcessingArtifactNotFound)
				return
			}
			assert.ErrorIs(t, err, fs.ErrNotExist)
			require.NoError(t, manifestErr)
		})
	}
}

func TestProcessingArtifactStoreGlobalOBSWriteUsesAdapterMetadata(t *testing.T) {
	for name, config := range map[string]*types.StorageEngineConfig{
		"nil config":   nil,
		"empty config": {},
	} {
		t.Run(name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			proxyPath := "https://files.example.com/obs/weknora/7/object.bin"
			canonicalPath := "obs://global-bucket/weknora/7/object.bin"
			want := bytes.Repeat([]byte("o"), ProcessingArtifactInlineLimit+1)
			testStore.files.savePath = proxyPath
			testStore.files.objects[proxyPath] = bytes.Clone(want)
			global := artifactMetadataFileService{
				FileService:    testStore.files,
				provider:       "obs",
				canonicalPaths: map[string]string{proxyPath: canonicalPath},
				servicePaths:   map[string]string{canonicalPath: proxyPath},
			}
			store := NewProcessingArtifactStore(
				testStore.repository,
				&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: config}},
				global,
			)
			key := processingArtifactServiceKey(t, "global-obs-write-"+name)

			_, created, err := store.PutIfAbsent(context.Background(), key, want)

			require.NoError(t, err)
			assert.True(t, created)
			row, err := testStore.repository.Get(context.Background(), key)
			require.NoError(t, err)
			assert.Equal(t, canonicalPath, row.ObjectPath)
			got, hit, err := store.Get(context.Background(), key)
			require.NoError(t, err)
			assert.True(t, hit)
			assert.Equal(t, want, got)
		})
	}
}

func TestProcessingArtifactStoreGlobalOBSMapsReadAndDeleteAfterTenantDefaultChange(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "global-obs-provider-change")
	proxyPath := "https://files.example.com/obs/weknora/7/object.bin"
	canonicalPath := "obs://global-bucket/weknora/7/object.bin"
	want := []byte("global-obs-object")
	testStore.files.objects[proxyPath] = bytes.Clone(want)
	seedProcessingArtifact(t, testStore.repository, key, nil, canonicalPath, int64(len(want)), artifactSHA(want))
	global := artifactMetadataFileService{
		FileService:    testStore.files,
		provider:       "obs",
		canonicalPaths: map[string]string{proxyPath: canonicalPath},
		servicePaths:   map[string]string{canonicalPath: proxyPath},
	}
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "local",
			Local:           &types.LocalEngineConfig{},
		}}},
		global,
	)

	got, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, want, got)

	testStore.files.mu.Lock()
	testStore.files.objects[proxyPath] = []byte("corrupt")
	testStore.files.mu.Unlock()
	value, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, deletes, _ := testStore.files.snapshot()
	assert.Equal(t, []string{proxyPath}, deletes)
}

func TestProcessingArtifactStoreEvictsMissingObjectThroughEmptyConfigGlobalFallback(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "empty-config-global-missing")
	path := "historical-object"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	store := NewProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{}}},
		testStore.files,
	)

	value, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, deletes, _ := testStore.files.snapshot()
	assert.Equal(t, []string{path}, deletes)
}

func TestProcessingArtifactStoreEvictsMissingObjectFromConfiguredProvider(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "configured-provider-missing")
	path := "minio://tenant-bucket/object"
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 10, artifactSHA([]byte("0123456789")))
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "minio",
		MinIO:           &types.MinIOEngineConfig{},
	}}
	store := newProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		testStore.files,
		func(provider string, _ *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
			assert.Equal(t, "minio", provider)
			return artifactMetadataFileService{FileService: testStore.files, provider: provider}, provider, nil
		},
	)

	value, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	_, deletes, _ := testStore.files.snapshot()
	assert.Equal(t, []string{path}, deletes)
}

func TestProcessingArtifactStorePersistsCanonicalOBSProxyPath(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "obs",
		OBS: &types.OBSEngineConfig{
			BucketName: "tenant-bucket",
			PathPrefix: "weknora/artifacts",
		},
	}}
	proxyPath := "https://files.example.com/obs/weknora/artifacts/7/object.bin"
	canonicalPath := "obs://tenant-bucket/weknora/artifacts/7/object.bin"
	testStore.files.savePath = proxyPath
	service := artifactMetadataFileService{
		FileService:    testStore.files,
		provider:       "obs",
		canonicalPaths: map[string]string{proxyPath: canonicalPath},
		servicePaths:   map[string]string{canonicalPath: proxyPath},
	}
	store := newProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		service,
		func(provider string, config *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
			assert.Equal(t, "obs", provider)
			assert.Same(t, tenant.StorageEngineConfig, config)
			return service, provider, nil
		},
	)

	_, created, err := store.PutIfAbsent(
		context.Background(),
		processingArtifactServiceKey(t, "obs-proxy-manifest"),
		bytes.Repeat([]byte("o"), ProcessingArtifactInlineLimit+1),
	)

	require.NoError(t, err)
	assert.True(t, created)
	row, err := testStore.repository.Get(context.Background(), processingArtifactServiceKey(t, "obs-proxy-manifest"))
	require.NoError(t, err)
	assert.Equal(t, canonicalPath, row.ObjectPath)
}

func TestProcessingArtifactStoreResolvesCanonicalOBSPathAfterDefaultProviderChange(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "obs-provider-change")
	path := "obs://tenant-bucket/weknora/artifacts/7/object.bin"
	want := []byte("historical-obs-object")
	testStore.files.objects[path] = bytes.Clone(want)
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(want)), artifactSHA(want))
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{},
		OBS:             &types.OBSEngineConfig{BucketName: "tenant-bucket"},
	}}
	store := newProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		testStore.files,
		func(provider string, config *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
			assert.Equal(t, "obs", provider)
			assert.Same(t, tenant.StorageEngineConfig, config)
			return artifactMetadataFileService{FileService: testStore.files, provider: provider}, provider, nil
		},
	)

	got, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, want, got)
}

func TestProcessingArtifactStoreReadsCanonicalOBSPathThroughProxyFileService(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "obs-proxy-read")
	canonicalPath := "obs://tenant-bucket/weknora/artifacts/7/object.bin"
	proxyPath := "https://files.example.com/obs/weknora/artifacts/7/object.bin"
	want := []byte("proxy-backed-obs-object")
	testStore.files.objects[proxyPath] = bytes.Clone(want)
	service := artifactMetadataFileService{
		FileService:  testStore.files,
		provider:     "obs",
		servicePaths: map[string]string{canonicalPath: proxyPath},
	}
	seedProcessingArtifact(
		t, testStore.repository, key, nil, canonicalPath, int64(len(want)), artifactSHA(want),
	)
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "obs",
		OBS:             &types.OBSEngineConfig{BucketName: "tenant-bucket"},
	}}
	store := newProcessingArtifactStore(
		testStore.repository,
		&artifactTenantRepository{tenant: tenant},
		service,
		func(provider string, config *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
			return service, provider, nil
		},
	)

	got, hit, err := store.Get(context.Background(), key)

	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, want, got)
}

func TestProcessingArtifactStoreDeletesCanonicalOBSLoserThroughProxyFileService(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "obs-proxy-loser")
	seedProcessingArtifact(
		t, testStore.repository, key, []byte("existing-winner"), "",
		int64(len("existing-winner")), artifactSHA([]byte("existing-winner")),
	)
	proxyPath := "https://files.example.com/obs/weknora/artifacts/7/loser.bin"
	canonicalPath := "obs://tenant-bucket/weknora/artifacts/7/loser.bin"
	testStore.files.savePath = proxyPath
	service := artifactMetadataFileService{
		FileService:    testStore.files,
		provider:       "obs",
		canonicalPaths: map[string]string{proxyPath: canonicalPath},
		servicePaths:   map[string]string{canonicalPath: proxyPath},
	}
	repository := &artifactGetErrorRepository{
		ProcessingArtifactRepository: testStore.repository,
		err:                          errors.New("winner read unavailable"),
	}
	tenant := &types.Tenant{ID: 7, StorageEngineConfig: &types.StorageEngineConfig{
		DefaultProvider: "obs",
		OBS:             &types.OBSEngineConfig{BucketName: "tenant-bucket"},
	}}
	store := newProcessingArtifactStore(
		repository,
		&artifactTenantRepository{tenant: tenant},
		service,
		func(provider string, config *types.StorageEngineConfig, _ string) (interfaces.FileService, string, error) {
			return service, provider, nil
		},
	)

	_, created, err := store.PutIfAbsent(
		context.Background(), key, bytes.Repeat([]byte("g"), ProcessingArtifactInlineLimit+1),
	)

	require.Error(t, err)
	assert.False(t, created)
	_, deletes, _ := testStore.files.snapshot()
	assert.Equal(t, []string{proxyPath}, deletes)
}

func TestProcessingArtifactStoreEvictsInvalidObjectSizeWithoutReading(t *testing.T) {
	for name, size := range map[string]int64{
		"negative":  -1,
		"max int64": int64(^uint64(0) >> 1),
	} {
		t.Run(name, func(t *testing.T) {
			testStore := newProcessingArtifactTestStore(t)
			key := processingArtifactServiceKey(t, "invalid-size-"+name)
			path := "local://tenant-7/invalid-size"
			require.NoError(t, testStore.db.Exec(
				`INSERT INTO processing_artifacts
		(tenant_id, stage, key_version, input_fingerprint, payload, object_path, content_sha256, size_bytes)
		VALUES (?, ?, ?, ?, NULL, ?, ?, ?)`,
				key.TenantID, key.Stage, key.KeyVersion, key.InputFingerprint, path, strings.Repeat("a", 64), size,
			).Error)
			testStore.files.getErrors[path] = errors.New("must not read invalid manifest")

			value, hit, err := testStore.store.Get(context.Background(), key)
			require.NoError(t, err)
			assert.False(t, hit)
			assert.Nil(t, value)
			_, err = testStore.repository.Get(context.Background(), key)
			assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
		})
	}
}

func TestProcessingArtifactStoreStopsAfterThreeCorruptWinnerRaces(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	racingRepository := &corruptWinnerRepository{ProcessingArtifactRepository: testStore.repository}
	store := NewProcessingArtifactStore(
		racingRepository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		testStore.files,
	)

	canonical, created, err := store.PutIfAbsent(
		context.Background(),
		processingArtifactServiceKey(t, "repeated-corrupt-winner"),
		[]byte("candidate"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contention did not converge after 3 attempts")
	assert.False(t, created)
	assert.Nil(t, canonical)
	assert.Equal(t, 3, racingRepository.attemptCount())
}

func TestProcessingArtifactStoreKeepsCandidateOnAmbiguousInsertError(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "ambiguous-insert")
	seedProcessingArtifact(
		t, testStore.repository, key, []byte("existing-winner"), "",
		int64(len("existing-winner")), artifactSHA([]byte("existing-winner")),
	)
	repository := &artifactPutErrorRepository{
		ProcessingArtifactRepository: testStore.repository,
		err:                          errors.New("commit result unavailable"),
	}
	store := NewProcessingArtifactStore(
		repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		testStore.files,
	)

	_, created, err := store.PutIfAbsent(
		context.Background(),
		key,
		bytes.Repeat([]byte("z"), ProcessingArtifactInlineLimit+1),
	)
	require.Error(t, err)
	assert.False(t, created)
	saves, deletes, objects := testStore.files.snapshot()
	require.Len(t, saves, 1)
	assert.Empty(t, deletes)
	assert.Contains(t, objects, saves[0])
}

func TestProcessingArtifactStoreDeletesKnownLoserOnWinnerReadError(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "ambiguous-winner-read")
	seedProcessingArtifact(
		t, testStore.repository, key, []byte("existing-winner"), "",
		int64(len("existing-winner")), artifactSHA([]byte("existing-winner")),
	)
	repository := &artifactGetErrorRepository{
		ProcessingArtifactRepository: testStore.repository,
		err:                          errors.New("winner read unavailable"),
	}
	store := NewProcessingArtifactStore(
		repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		testStore.files,
	)

	_, created, err := store.PutIfAbsent(
		context.Background(),
		key,
		bytes.Repeat([]byte("g"), ProcessingArtifactInlineLimit+1),
	)
	require.Error(t, err)
	assert.False(t, created)
	saves, deletes, objects := testStore.files.snapshot()
	require.Len(t, saves, 1)
	assert.Equal(t, []string{saves[0]}, deletes)
	assert.NotContains(t, objects, saves[0])
}

func TestProcessingArtifactStoreRejectsReusedSaveBytesPath(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "reused-save-path")
	path := "local://tenant-7/shared-path"
	winner := bytes.Repeat([]byte("w"), ProcessingArtifactInlineLimit+1)
	testStore.files.objects[path] = bytes.Clone(winner)
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(winner)), artifactSHA(winner))
	testStore.files.savePath = path

	canonical, created, err := testStore.store.PutIfAbsent(
		context.Background(),
		key,
		bytes.Repeat([]byte("c"), ProcessingArtifactInlineLimit+1),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SaveBytes path uniqueness contract")
	assert.False(t, created)
	assert.Nil(t, canonical)
	_, deletes, objects := testStore.files.snapshot()
	assert.Empty(t, deletes)
	assert.Contains(t, objects, path)
}

func TestProcessingArtifactStoreClosesReaderBeforeEvictingObject(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "close-before-evict")
	path := "local://tenant-7/corrupt"
	testStore.files.objects[path] = []byte("corrupt")
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len("expected")), artifactSHA([]byte("expected")))

	value, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	assert.Equal(t, []string{"close:" + path, "delete:" + path}, testStore.files.eventSnapshot())
}

func TestProcessingArtifactStoreKeepsManifestOnCloseError(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "close-error")
	path := "local://tenant-7/close-error"
	want := []byte("valid-object")
	closeErr := errors.New("close transport error")
	testStore.files.objects[path] = bytes.Clone(want)
	testStore.files.closeErrors[path] = closeErr
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len(want)), artifactSHA(want))

	value, hit, err := testStore.store.Get(context.Background(), key)
	assert.ErrorIs(t, err, closeErr)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, err = testStore.repository.Get(context.Background(), key)
	require.NoError(t, err)
	_, deletes, _ := testStore.files.snapshot()
	assert.Empty(t, deletes)
}

func TestProcessingArtifactStoreBoundsObjectReadAtSizePlusOne(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "bounded-read")
	path := "local://tenant-7/unbounded-source"
	reader := &artifactCountingReader{}
	testStore.files.readers[path] = reader
	seedProcessingArtifact(t, testStore.repository, key, nil, path, 4, artifactSHA([]byte("xxxx")))

	value, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, value)
	assert.Equal(t, 5, reader.bytesRead())
}

func TestProcessingArtifactStoreDoesNotDeleteObjectWhenManifestEvictionFails(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "delete-row-fails")
	path := "local://tenant-7/corrupt-protected"
	testStore.files.objects[path] = []byte("corrupt")
	seedProcessingArtifact(t, testStore.repository, key, nil, path, int64(len("expected")), artifactSHA([]byte("expected")))
	deleteErr := errors.New("delete row failed")
	store := NewProcessingArtifactStore(
		&artifactDeleteErrorRepository{ProcessingArtifactRepository: testStore.repository, err: deleteErr},
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		testStore.files,
	)

	value, hit, err := store.Get(context.Background(), key)
	assert.ErrorIs(t, err, deleteErr)
	assert.False(t, hit)
	assert.Nil(t, value)
	_, deletes, objects := testStore.files.snapshot()
	assert.Empty(t, deletes)
	assert.Contains(t, objects, path)
}

func TestProcessingArtifactStoreRepairsCorruptWinnerWithRealRepositoryRetry(t *testing.T) {
	testStore := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "real-repair-retry")
	seedProcessingArtifact(t, testStore.repository, key, []byte("corrupt"), "", 99, artifactSHA([]byte("different")))
	want := []byte("repaired")

	canonical, created, err := testStore.store.PutIfAbsent(context.Background(), key, want)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, want, canonical)
	stored, hit, err := testStore.store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, want, stored)
}
