package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
)

type knowledgeFolderPlacementRepositoryStub struct {
	interfaces.KnowledgeFolderRepository
}

type knowledgeServiceDigDependencies struct {
	dig.Out

	Config                   *config.Config
	KnowledgeRepository      interfaces.KnowledgeRepository
	DocumentReader           interfaces.DocumentReader
	KnowledgeBaseService     interfaces.KnowledgeBaseService
	TenantRepository         interfaces.TenantRepository
	TenantService            interfaces.TenantService
	ChunkService             interfaces.ChunkService
	ChunkRepository          interfaces.ChunkRepository
	KnowledgeTagRepository   interfaces.KnowledgeTagRepository
	KnowledgeTagService      interfaces.KnowledgeTagService
	FileService              interfaces.FileService
	StorageBackendResolver   interfaces.StorageBackendResolver
	ModelService             interfaces.ModelService
	TaskEnqueuer             interfaces.TaskEnqueuer
	TaskInspector            interfaces.TaskInspector
	RetrieveGraphRepository  interfaces.RetrieveGraphRepository
	RetrieveEngineRegistry   interfaces.RetrieveEngineRegistry
	TenantStoreOwnership     retriever.TenantStoreOwnership
	RedisClient              *redis.Client
	KBShareService           interfaces.KBShareService
	ImageResolver            *docparser.ImageResolver
	WikiPageRepository       interfaces.WikiPageRepository
	WikiPageService          interfaces.WikiPageService
	TaskPendingOpsRepository interfaces.TaskPendingOpsRepository
	SpanTracker              service.SpanTracker
}

func provideKnowledgeServiceDigDependencies() knowledgeServiceDigDependencies {
	return knowledgeServiceDigDependencies{
		Config:        &config.Config{},
		RedisClient:   &redis.Client{},
		ImageResolver: &docparser.ImageResolver{},
	}
}

func TestProvideKnowledgeFolderReaderReturnsSameRepository(t *testing.T) {
	repo := &knowledgeFolderPlacementRepositoryStub{}

	reader := provideKnowledgeFolderReader(repo)

	require.Same(t, repo, reader)
}

func TestKnowledgeFolderPlacementDigGraphResolvesKnowledgeService(t *testing.T) {
	container := dig.New()
	repo := &knowledgeFolderPlacementRepositoryStub{}
	repositoryConstructorCalls := 0

	require.NoError(t, container.Provide(func() interfaces.KnowledgeFolderRepository {
		repositoryConstructorCalls++
		return repo
	}))
	require.NoError(t, container.Provide(provideKnowledgeFolderReader))
	require.NoError(t, container.Provide(service.NewKnowledgeFolderPlacementResolver))
	require.NoError(t, container.Provide(provideKnowledgeServiceDigDependencies))
	require.NoError(t, container.Provide(service.NewKnowledgeService))

	require.NoError(t, container.Invoke(func(
		resolvedRepo interfaces.KnowledgeFolderRepository,
		reader interfaces.KnowledgeFolderReader,
		resolver interfaces.KnowledgeFolderPlacementResolver,
		knowledgeService interfaces.KnowledgeService,
	) {
		require.Same(t, repo, resolvedRepo)
		require.Same(t, repo, reader)
		require.NotNil(t, resolver)
		require.NotNil(t, knowledgeService)
	}))
	require.Equal(t, 1, repositoryConstructorCalls)
}
