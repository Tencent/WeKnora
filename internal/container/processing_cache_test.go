package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type cacheEngineSpy struct {
	interfaces.RetrieveEngineService
	cache *processingcache.Runtime
}

func (s *cacheEngineSpy) SetProcessingCache(cache *processingcache.Runtime) { s.cache = cache }

func TestProcessingCacheWiring(t *testing.T) {
	if newProcessingCache(nil, nil) != nil || newProcessingCache(&gorm.DB{}, &config.Config{}) != nil {
		t.Fatal("disabled configuration must preserve the original indexing path")
	}
	cache := newProcessingCache(
		&gorm.DB{},
		&config.Config{ProcessingCache: &config.ProcessingCacheConfig{ReadEnabled: true}},
	)
	if cache == nil {
		t.Fatal("enabled cache not constructed")
	}
	engine := &cacheEngineSpy{}
	attachProcessingCache(engine, cache)
	if engine.cache != cache {
		t.Fatal("engine did not receive runtime")
	}
}
