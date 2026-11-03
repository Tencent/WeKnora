package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/processingcache"
)

func TestProcessingCacheSetters(t *testing.T) {
	r := processingcache.New(nil, processingcache.Options{})
	k := &knowledgeService{}
	c := &ChunkExtractService{}
	w := &wikiIngestService{}
	k.SetProcessingCache(r)
	c.SetProcessingCache(r)
	w.SetProcessingCache(r)
	if k.processingCache != r || c.processingCache != r || w.processingCache != r {
		t.Fatal("cache not attached")
	}
}
