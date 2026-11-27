package container

import (
	"testing"

	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/require"
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
	provide(repository.NewVectorStoreRepository)
	provide(NewEngineFactory)
	provide(initRetrieveEngineRegistry)

	err = c.Invoke(func(registry interfaces.RetrieveEngineRegistry) {
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

func TestGraphRepositoriesWiringAndConfiguration(t *testing.T) {
	for _, tc := range []struct {
		backend, legacy, wantError string
	}{
		{"", "false", ""},
		{"", "true", ""},
		{"none", "true", ""},
		{"neo4j", "false", ""},
		{"hologres", "true", "HOLOGRES_GRAPH_DSN is required"},
		{"invalid", "false", "GRAPH_BACKEND must be"},
	} {
		t.Run(tc.backend+"/"+tc.legacy, func(t *testing.T) {
			t.Setenv("GRAPH_BACKEND", tc.backend)
			t.Setenv("NEO4J_ENABLE", tc.legacy)
			t.Setenv("HOLOGRES_GRAPH_DSN", "")
			t.Setenv("HOLOGRES_GRAPH_AUTO_CREATE", "false")
			t.Setenv("HOLOGRES_GRAPH_NAME", "")
			c := dig.New()
			require.NoError(t, c.Provide(func() neo4j.Driver { return nil }))
			require.NoError(t, c.Provide(NewResourceCleaner))
			require.NoError(t, c.Provide(initGraphRepositories))
			called := false
			err := c.Invoke(func(retrieve interfaces.RetrieveGraphRepository, search interfaces.GraphSearchRepository) {
				called = true
				require.NotNil(t, retrieve)
				require.NotNil(t, search)
			})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.True(t, called)
			}
		})
	}
}

func TestHologresSelectionDoesNotInitializeNeo4j(t *testing.T) {
	t.Setenv("GRAPH_BACKEND", "hologres")
	t.Setenv("NEO4J_ENABLE", "true")
	t.Setenv("NEO4J_URI", "invalid://must-not-connect")
	driver, err := initNeo4jClient()
	require.NoError(t, err)
	require.Nil(t, driver)
}
