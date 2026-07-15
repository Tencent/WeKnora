package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const processingArtifactsTestDDL = `
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
);
`

func setupProcessingArtifactTestDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "processing-artifacts.db"))
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", dbPath)

	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		return db
	}

	first := open()
	require.NoError(t, first.Exec(processingArtifactsTestDDL).Error)
	return first, open()
}

func processingArtifact(tenantID uint64, stage string, keyVersion uint16, fingerprint, payload string) *types.ProcessingArtifact {
	return &types.ProcessingArtifact{
		TenantID:         tenantID,
		Stage:            stage,
		KeyVersion:       keyVersion,
		InputFingerprint: fingerprint,
		Payload:          []byte(payload),
		ContentSHA256:    strings.Repeat("a", 64),
		SizeBytes:        int64(len(payload)),
	}
}

func artifactKey(artifact *types.ProcessingArtifact) types.ProcessingArtifactKey {
	return types.ProcessingArtifactKey{
		TenantID:         artifact.TenantID,
		Stage:            artifact.Stage,
		KeyVersion:       artifact.KeyVersion,
		InputFingerprint: artifact.InputFingerprint,
	}
}

func TestProcessingArtifactPutIfAbsentConverges(t *testing.T) {
	db1, db2 := setupProcessingArtifactTestDBs(t)
	repositories := []interface {
		PutIfAbsent(context.Context, *types.ProcessingArtifact) (bool, error)
		Get(context.Context, types.ProcessingArtifactKey) (*types.ProcessingArtifact, error)
	}{
		NewProcessingArtifactRepository(db1),
		NewProcessingArtifactRepository(db2),
	}
	artifacts := []*types.ProcessingArtifact{
		processingArtifact(7, "chunking", 1, strings.Repeat("1", 64), "winner-one"),
		processingArtifact(7, "chunking", 1, strings.Repeat("1", 64), "winner-two-with-more-bytes"),
	}
	artifacts[1].ContentSHA256 = strings.Repeat("b", 64)

	start := make(chan struct{})
	type putResult struct {
		candidate int
		created   bool
		err       error
	}
	results := make(chan putResult, len(repositories))
	var ready sync.WaitGroup
	var wg sync.WaitGroup
	ready.Add(len(repositories))
	for i := range repositories {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ready.Done()
			<-start
			wasCreated, err := repositories[i].PutIfAbsent(context.Background(), artifacts[i])
			results <- putResult{candidate: i, created: wasCreated, err: err}
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()
	close(results)

	createdCount := 0
	var winner *types.ProcessingArtifact
	for result := range results {
		require.NoError(t, result.err)
		if result.created {
			createdCount++
			winner = artifacts[result.candidate]
		}
	}
	require.Equal(t, 1, createdCount)
	require.NotNil(t, winner)

	key := artifactKey(artifacts[0])
	var persistedID uint64
	for i, repo := range repositories {
		persisted, err := repo.Get(context.Background(), key)
		require.NoError(t, err)
		assert.Equal(t, winner.Payload, persisted.Payload)
		assert.Equal(t, winner.ContentSHA256, persisted.ContentSHA256)
		assert.Equal(t, winner.SizeBytes, persisted.SizeBytes)
		if i == 0 {
			persistedID = persisted.ID
		} else {
			assert.Equal(t, persistedID, persisted.ID)
		}
	}
}

func TestProcessingArtifactRepositoryScopesByFullKey(t *testing.T) {
	db, _ := setupProcessingArtifactTestDBs(t)
	repo := NewProcessingArtifactRepository(db)
	fingerprint := strings.Repeat("2", 64)
	fingerprintVariant := processingArtifact(7, "chunking", 1, strings.Repeat("7", 64), "fingerprint-variant")
	fingerprintVariant.ContentSHA256 = strings.Repeat("b", 64)
	artifacts := []*types.ProcessingArtifact{
		processingArtifact(7, "chunking", 1, fingerprint, "tenant-seven"),
		processingArtifact(8, "chunking", 1, fingerprint, "tenant-eight"),
		processingArtifact(7, "embedding", 1, fingerprint, "embedding"),
		processingArtifact(7, "chunking", 2, fingerprint, "version-two"),
		fingerprintVariant,
	}

	for _, artifact := range artifacts {
		created, err := repo.PutIfAbsent(context.Background(), artifact)
		require.NoError(t, err)
		require.True(t, created)
	}

	for _, want := range artifacts {
		got, err := repo.Get(context.Background(), artifactKey(want))
		require.NoError(t, err)
		assert.Equal(t, want.Payload, got.Payload)
		assert.Equal(t, want.TenantID, got.TenantID)
		assert.Equal(t, want.Stage, got.Stage)
		assert.Equal(t, want.KeyVersion, got.KeyVersion)
		assert.Equal(t, want.InputFingerprint, got.InputFingerprint)
		assert.Equal(t, want.ContentSHA256, got.ContentSHA256)
		assert.Equal(t, want.SizeBytes, got.SizeBytes)
	}
}

func TestProcessingArtifactDeleteByIDRequiresTenant(t *testing.T) {
	db, _ := setupProcessingArtifactTestDBs(t)
	repo := NewProcessingArtifactRepository(db)
	artifact := processingArtifact(7, "chunking", 1, strings.Repeat("3", 64), "protected")
	created, err := repo.PutIfAbsent(context.Background(), artifact)
	require.NoError(t, err)
	require.True(t, created)

	stored, err := repo.Get(context.Background(), artifactKey(artifact))
	require.NoError(t, err)
	require.NoError(t, repo.DeleteByID(context.Background(), 8, stored.ID))
	_, err = repo.Get(context.Background(), artifactKey(artifact))
	require.NoError(t, err)

	require.NoError(t, repo.DeleteByID(context.Background(), 7, stored.ID))
	_, err = repo.Get(context.Background(), artifactKey(artifact))
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
}

func TestProcessingArtifactPutIfAbsentValidatesImmutableFields(t *testing.T) {
	db, _ := setupProcessingArtifactTestDBs(t)
	repo := NewProcessingArtifactRepository(db)
	valid := processingArtifact(7, "chunking", 1, strings.Repeat("4", 64), "inline")

	tests := map[string]func(*types.ProcessingArtifact){
		"zero tenant":            func(a *types.ProcessingArtifact) { a.TenantID = 0 },
		"invalid stage":          func(a *types.ProcessingArtifact) { a.Stage = "Chunking" },
		"zero key version":       func(a *types.ProcessingArtifact) { a.KeyVersion = 0 },
		"invalid fingerprint":    func(a *types.ProcessingArtifact) { a.InputFingerprint = strings.Repeat("A", 64) },
		"invalid content hash":   func(a *types.ProcessingArtifact) { a.ContentSHA256 = strings.Repeat("g", 64) },
		"negative size":          func(a *types.ProcessingArtifact) { a.SizeBytes = -1 },
		"inline without payload": func(a *types.ProcessingArtifact) { a.Payload = nil },
		"object with payload":    func(a *types.ProcessingArtifact) { a.ObjectPath = "tenant/7/artifact" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := *valid
			artifact.Payload = append([]byte(nil), valid.Payload...)
			mutate(&artifact)
			created, err := repo.PutIfAbsent(context.Background(), &artifact)
			assert.False(t, created)
			assert.Error(t, err)
		})
	}

	created, err := repo.PutIfAbsent(context.Background(), nil)
	assert.False(t, created)
	assert.Error(t, err)

	emptyInline := processingArtifact(7, "chunking", 1, strings.Repeat("8", 64), "")
	emptyInline.Payload = []byte{}
	require.NotNil(t, emptyInline.Payload)
	created, err = repo.PutIfAbsent(context.Background(), emptyInline)
	require.NoError(t, err)
	assert.True(t, created)

	emptyObject := processingArtifact(7, "chunking", 1, strings.Repeat("9", 64), "")
	emptyObject.Payload = []byte{}
	emptyObject.ObjectPath = "tenant/7/invalid-empty-object"
	created, err = repo.PutIfAbsent(context.Background(), emptyObject)
	assert.False(t, created)
	assert.Error(t, err)

	objectArtifact := processingArtifact(7, "chunking", 1, strings.Repeat("5", 64), "")
	objectArtifact.Payload = nil
	objectArtifact.ObjectPath = "tenant/7/artifact"
	objectArtifact.SizeBytes = 42
	created, err = repo.PutIfAbsent(context.Background(), objectArtifact)
	require.NoError(t, err)
	assert.True(t, created)
}

func TestProcessingArtifactGetMapsOnlyRecordNotFound(t *testing.T) {
	db, _ := setupProcessingArtifactTestDBs(t)
	repo := NewProcessingArtifactRepository(db)
	key := types.ProcessingArtifactKey{
		TenantID:         7,
		Stage:            "chunking",
		KeyVersion:       1,
		InputFingerprint: strings.Repeat("6", 64),
	}

	_, err := repo.Get(context.Background(), key)
	assert.ErrorIs(t, err, types.ErrProcessingArtifactNotFound)
	assert.False(t, errors.Is(err, gorm.ErrRecordNotFound))
}
