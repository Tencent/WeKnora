package container

import (
	"context"
	"time"

	hologresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/hologres"
	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/dig"
)

type graphRepositories struct {
	dig.Out
	Retrieve interfaces.RetrieveGraphRepository
	Search   interfaces.GraphSearchRepository
}

func initGraphRepositories(driver neo4j.Driver, cleaner interfaces.ResourceCleaner) (graphRepositories, error) {
	if err := config.ValidateGraphBackend(); err != nil {
		return graphRepositories{}, err
	}
	if config.GraphBackend() != config.GraphBackendHologres {
		return graphRepositories{
			Retrieve: neo4jRepo.NewNeo4jRepository(driver),
			Search:   neo4jRepo.NewGraphSearchRepository(driver),
		}, nil
	}
	cfg, err := hologresRepo.ConfigFromEnv()
	if err != nil {
		return graphRepositories{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := hologresRepo.Open(ctx, cfg)
	if err != nil {
		return graphRepositories{}, err
	}
	cleaner.RegisterWithName("Hologres graph connection pool", repo.Close)
	return graphRepositories{Retrieve: repo, Search: repo}, nil
}
