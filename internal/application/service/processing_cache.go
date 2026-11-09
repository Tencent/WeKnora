package service

import "github.com/Tencent/WeKnora/internal/processingcache"

// SetProcessingCache attaches the runtime before summary tasks are started.
func (s *DataTableSummaryService) SetProcessingCache(r *processingcache.Runtime) {
	s.processingCache = r
}

// SetProcessingCache attaches the optional runtime before any tasks are started.
func (s *knowledgeService) SetProcessingCache(r *processingcache.Runtime) { s.processingCache = r }

// SetProcessingCache attaches the runtime before graph tasks are started.
func (s *ChunkExtractService) SetProcessingCache(r *processingcache.Runtime) { s.processingCache = r }
func (s *wikiIngestService) SetProcessingCache(r *processingcache.Runtime)   { s.processingCache = r }

// SetProcessingCache attaches the runtime before image tasks are started.
func (s *ImageMultimodalService) SetProcessingCache(r *processingcache.Runtime) {
	s.processingCache = r
}
