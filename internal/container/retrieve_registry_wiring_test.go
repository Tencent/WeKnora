package container

import (
	"testing"

	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// TestRetrieveEngineRegistryWiring checks that the container can still build
// the retrieval engine registry, and that what it builds can rebuild a missing
// store engine.
//
// The registry depends on the vector store repository and the engine factory,
// and the engine factory must not in turn depend on the registry. A cycle or a
// missing provider surfaces only when the process starts, so it is worth
// pinning here rather than discovering it at deploy time. The nil check at the
// end is the part that matters: a registry resolved without those two
// dependencies still satisfies the interface and still serves lookups, so it
// would pass every other test while silently never rebuilding anything.
func TestRetrieveEngineRegistryWiring(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-mem db: %v", err)
	}

	c := dig.New()
	provide := func(constructor interface{}) {
		t.Helper()
		if err := c.Provide(constructor); err != nil {
			t.Fatalf("provide: %v", err)
		}
	}
	provide(func() *gorm.DB { return db })
	provide(func() *config.Config { return &config.Config{} })
	provide(func() interfaces.AuditLogService { return &fakeAuditSvc{} })
	provide(func() interfaces.TaskEnqueuer { return nil })
	provide(repository.NewVectorStoreRepository)
	provide(NewEngineFactory)
	provide(initRetrieveEngineRegistry)
	provide(func() *redis.Client { return nil })
	provide(repository.NewTenantRepository)
	provide(retriever.NewVectorStoreRepoOwnership)
	provide(service.NewDocumentTagSyncService)
	provide(func(s *service.DocumentTagSyncService) interfaces.DocumentTagSync { return s })
	provide(repository.NewKnowledgeRepositoryWithTagSync)
	provide(repository.NewKnowledgeTagRepositoryWithTagSync)

	err = c.Invoke(func(registry interfaces.RetrieveEngineRegistry, syncer interfaces.DocumentTagSync, knowledge interfaces.KnowledgeRepository, tags interfaces.KnowledgeTagRepository) {
		if syncer == nil || knowledge == nil || tags == nil {
			t.Fatal("tag synchronization dependencies must be wired")
		}
		concrete, ok := registry.(*retriever.RetrieveEngineRegistry)
		if !ok {
			t.Fatalf("expected *retriever.RetrieveEngineRegistry, got %T", registry)
		}
		if !concrete.CanRebuildStores() {
			t.Error("registry was built without the dependencies it needs to rebuild a store engine")
		}
	})
	if err != nil {
		t.Fatalf("container could not build the registry: %v", err)
	}
}
