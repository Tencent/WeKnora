package container

import (
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/processingcache/gormstore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

func newProcessingCache(db *gorm.DB, cfg *config.Config) *processingcache.Runtime {
	if db == nil || cfg == nil {
		return nil
	}
	opts := cfg.ProcessingCache.Options()
	if !opts.ReadEnabled && !opts.WriteEnabled {
		return nil
	}
	return processingcache.New(gormstore.New(db), opts)
}

// Attach before registration so concurrent requests only see initialized engines.
func attachProcessingCache(engine interfaces.RetrieveEngineService, cache *processingcache.Runtime) {
	if setter, ok := engine.(interface {
		SetProcessingCache(*processingcache.Runtime)
	}); ok {
		setter.SetProcessingCache(cache)
	}
}

type processingCacheServices struct {
	dig.In
	Knowledge        interfaces.KnowledgeService
	ChunkExtractor   interfaces.TaskHandler `name:"chunkExtractor"`
	WikiIngest       interfaces.TaskHandler `name:"wikiIngest"`
	ImageMultimodal  interfaces.TaskHandler `name:"imageMultimodal"  optional:"true"`
	DataTableSummary interfaces.TaskHandler `name:"dataTableSummary" optional:"true"`
	DB               *gorm.DB
	Config           *config.Config
}

// wireProcessingCache initializes all processing stages with one shared runtime
// before workers can observe them. Setters are optional for alternate handlers.
func wireProcessingCache(in processingCacheServices) {
	cache := newProcessingCache(in.DB, in.Config)
	for _, target := range []any{
		in.Knowledge, in.ChunkExtractor, in.WikiIngest, in.ImageMultimodal, in.DataTableSummary,
	} {
		if setter, ok := target.(interface {
			SetProcessingCache(*processingcache.Runtime)
		}); ok {
			setter.SetProcessingCache(cache)
		}
	}
}
