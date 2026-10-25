//go:build mysqlintegration

package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMySQLBindingAndDeletionSerialize(t *testing.T) {
	for _, kind := range []string{"vector", "storage", "resource"} {
		for _, first := range []string{"binding", "deletion"} {
			t.Run(kind+"/"+first, func(t *testing.T) {
				db := testutil.MySQL(t)
				ctx := context.Background()
				kbRepo := repository.NewKnowledgeBaseRepository(db)
				id := "parent"
				table := "vector_stores"
				var remove func(context.Context) error
				if kind == "vector" {
					require.NoError(t, db.Create(&types.VectorStore{ID: id, TenantID: 1, Name: "parent", EngineType: types.QdrantRetrieverEngineType}).Error)
					s := NewVectorStoreService(repository.NewVectorStoreRepository(db), kbRepo, nil, nil, db)
					remove = func(ctx context.Context) error { return s.DeleteStore(ctx, 1, id) }
				} else {
					table = "storage_backends"
					require.NoError(t, db.Create(&types.StorageBackend{ID: id, TenantID: 1, Name: "parent", Provider: "local", Source: types.StorageBackendSourceUser, Status: types.StorageBackendStatusActive}).Error)
					s := NewStorageBackendService(repository.NewStorageBackendRepository(db), db)
					remove = func(ctx context.Context) error { return s.Delete(ctx, 1, id) }
				}
				bind := func(ctx context.Context) error {
					if kind == "resource" {
						return repository.NewResourceRepository(db).Create(ctx, &types.StoredResource{ID: "object", Handle: "object", TenantID: 1, StorageBackendID: id, Provider: "local", PhysicalPath: "file", LocationHash: "hash"})
					}
					kb := &types.KnowledgeBase{ID: "kb", TenantID: 1, Name: "kb"}
					if kind == "vector" {
						kb.VectorStoreID = &id
					} else {
						kb.StorageBackendID = &id
					}
					return kbRepo.CreateKnowledgeBase(ctx, kb)
				}
				if first == "binding" {
					locked := make(chan struct{})
					release := make(chan struct{})
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test_binding_barrier", func(tx *gorm.DB) {
						if tx.Statement.Table == "knowledge_bases" || tx.Statement.Table == "resources" {
							close(locked)
							<-release
						}
					}))
					result := make(chan error, 1)
					go func() { result <- bind(ctx) }()
					select {
					case <-locked:
					case <-time.After(5 * time.Second):
						t.Fatal("binding did not reach insertion")
					}
					deleteCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
					err := remove(deleteCtx)
					cancel()
					close(release)
					require.Error(t, err, "deletion must wait for the binding's parent lock")
					require.NoError(t, <-result)
					require.Error(t, remove(ctx), "committed binding must prevent deletion")
				} else {
					locked := make(chan struct{})
					release := make(chan struct{})
					attempted := make(chan struct{})
					type roleKey struct{}
					require.NoError(t, db.Callback().Query().After("gorm:query").Register("test_delete_barrier", func(tx *gorm.DB) {
						if tx.Statement.Table == table && tx.Statement.Context.Value(roleKey{}) == "delete" {
							close(locked)
							<-release
						}
					}))
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test_bind_attempt", func(tx *gorm.DB) {
						if tx.Statement.Table == table && tx.Statement.Context.Value(roleKey{}) == "bind" {
							close(attempted)
						}
					}))
					deleted := make(chan error, 1)
					bound := make(chan error, 1)
					go func() { deleted <- remove(context.WithValue(ctx, roleKey{}, "delete")) }()
					select {
					case <-locked:
					case <-time.After(5 * time.Second):
						t.Fatal("deletion did not lock parent")
					}
					go func() { bound <- bind(context.WithValue(ctx, roleKey{}, "bind")) }()
					select {
					case <-attempted:
					case <-time.After(5 * time.Second):
						close(release)
						t.Fatal("binding did not query parent")
					}
					close(release)
					require.NoError(t, <-deleted)
					require.Error(t, <-bound, fmt.Sprintf("%s binding must reject the deleted parent", kind))
				}
			})
		}
	}
}

func TestMySQLStorageDisableRechecksConcurrentBinding(t *testing.T) {
	db := testutil.MySQL(t)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	ctx := context.Background()
	id := "backend"
	backend := &types.StorageBackend{ID: id, TenantID: 1, Name: "backend", Provider: "local", Source: types.StorageBackendSourceUser, Status: types.StorageBackendStatusActive}
	require.NoError(t, db.Create(backend).Error)
	s := NewStorageBackendService(repository.NewStorageBackendRepository(db), db)
	checked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	type roleKey struct{}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test_disable_barrier", func(tx *gorm.DB) {
		if tx.Statement.Table == "knowledge_bases" && tx.Statement.Context.Value(roleKey{}) == "disable" {
			once.Do(func() { close(checked); <-release })
		}
	}))
	disabled := *backend
	disabled.Status = types.StorageBackendStatusDisabled
	result := make(chan error, 1)
	go func() { result <- s.Update(context.WithValue(ctx, roleKey{}, "disable"), &disabled) }()
	select {
	case <-checked:
	case <-time.After(5 * time.Second):
		t.Fatal("disable did not check bindings")
	}
	require.NoError(t, repository.NewKnowledgeBaseRepository(db).CreateKnowledgeBase(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 1, Name: "kb", StorageBackendID: &id}))
	close(release)
	require.ErrorContains(t, <-result, "cannot be disabled")
	var status string
	require.NoError(t, db.Table("storage_backends").Select("status").Where("id=?", id).Scan(&status).Error)
	require.Equal(t, types.StorageBackendStatusActive, status)
}
