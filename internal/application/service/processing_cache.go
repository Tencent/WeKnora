package service

import "github.com/Tencent/WeKnora/internal/processingcache"

func (s *DataTableSummaryService) SetProcessingCache(r *processingcache.Runtime) {
	s.processingCache = r
}

// SetProcessingCache attaches the optional runtime before any tasks are started.
func (s *knowledgeService) SetProcessingCache(r *processingcache.Runtime)    { s.processingCache = r }
func (s *ChunkExtractService) SetProcessingCache(r *processingcache.Runtime) { s.processingCache = r }
func (s *wikiIngestService) SetProcessingCache(r *processingcache.Runtime)   { s.processingCache = r }

func (s *ImageMultimodalService) SetProcessingCache(r *processingcache.Runtime) {
	s.processingCache = r
}
