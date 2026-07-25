package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type CacheLayer string

const (
	CacheLayerVLM         CacheLayer = "vlm"
	CacheLayerEmbedding   CacheLayer = "embedding"
	CacheLayerSummary    CacheLayer = "summary"
	CacheLayerQuestion   CacheLayer = "question"
	CacheLayerWikiMap     CacheLayer = "wiki_map"
	CacheLayerGraphEntity CacheLayer = "graph_entity"
)

type CacheEvent struct {
	Layer     CacheLayer
	Hit       bool
	Key       string
	Duration  time.Duration
	Timestamp time.Time
}
type ReparseStats struct {
	mu             sync.Mutex
	KnowledgeID    string
	Attempt        int
	StartedAt      time.Time
	Events         []CacheEvent
	OldChunkCount  int
	NewChunkCount  int
	UnchangedCount int
	ChangedCount   int
	AddedCount     int
	RemovedCount   int
}

var (
	globalStatsMu sync.Mutex
	globalStats   = make(map[string]*ReparseStats)
)

func StartReparseStats(knowledgeID string, attempt int) *ReparseStats {
	s := &ReparseStats{KnowledgeID: knowledgeID, Attempt: attempt, StartedAt: time.Now()}
	globalStatsMu.Lock()
	globalStats[knowledgeID] = s
	globalStatsMu.Unlock()
	return s
}

func (s *ReparseStats) RecordChunkDiff(oldCount, newCount, unchanged, changed, added, removed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OldChunkCount = oldCount
	s.NewChunkCount = newCount
	s.UnchangedCount = unchanged
	s.ChangedCount = changed
	s.AddedCount = added
	s.RemovedCount = removed
}

func (s *ReparseStats) RecordCacheHit(layer CacheLayer, key string, dur time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, CacheEvent{Layer: layer, Hit: true, Key: key, Duration: dur, Timestamp: time.Now()})
}

func (s *ReparseStats) RecordCacheMiss(layer CacheLayer, key string, dur time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, CacheEvent{Layer: layer, Hit: false, Key: key, Duration: dur, Timestamp: time.Now()})
}
type LayerStats struct {
	Layer      CacheLayer    `json:"layer"`
	Hits       int           `json:"hits"`
	Misses     int           `json:"misses"`
	HitRate    float64       `json:"hit_rate"`
	TotalTime  time.Duration `json:"total_time_ns"`
}

func (s *ReparseStats) Summary() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	layerMap := make(map[CacheLayer]*LayerStats)
	for _, e := range s.Events {
		ls, ok := layerMap[e.Layer]
		if  !ok { ls = &LayerStats{Layer: e.Layer}; layerMap[e.Layer] = ls }
		if e.Hit { ls.Hits++ } else { ls.Misses++ }
		ls.TotalTime += e.Duration
	}
	layers := make([]LayerStats, 0, len(layerMap))
	for _, ls := range layerMap {
		total := ls.Hits + ls.Misses
		if total > 0 { ls.HitRate = float64(ls.Hits) / float64(total) }
		layers = append(layers, *ls)
	}
	return map[string]interface{}{
		"knowledge_id":    s.KnowledgeID,
		"attempt":          s.Attempt,
		"elapsed":          time.Since(s.StartedAt).String(),
		"old_chunk_count": s.OldChunkCount,
		"new_chunk_count": s.NewChunkCount,
		"unchanged":       s.UnchangedCount,
		"changed":         s.ChangedCount,
		"added":           s.AddedCount,
		"removed":         s.RemovedCount,
		"cache_layers":    layers,
		"total_events":    len(s.Events),
	}
}

func (s *ReparseStats) SummaryJSON() ([]byte, error) {
	return json.Marshal(s.Summary())
}

func GetReparseStats(knowledgeID string) *ReparseStats {
	globalStatsMu.Lock()
	defer globalStatsMu.Unlock()
	return globalStats[knowledgeID]
}
func (s *ReparseStats) FormatStatsTable() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	layerMap := make(map[CacheLayer]*LayerStats)
	for _, e := range s.Events {
		ls, ok := layerMap[e.Layer]
		if !ok { ls = &LayerStats{Layer: e.Layer}; layerMap[e.Layer] = ls }
		if e.Hit { ls.Hits++ } else { ls.Misses++ }
		ls.TotalTime += e.Duration
	}
	var b strings.Builder
	b.WriteString("=== Reparse Cache Stats ===\n")
	fmt.Fprintf(&b, "Knowledge: %s  Attempt: %d  Elapsed: %s\n", s.KnowledgeID, s.Attempt, time.Since(s.StartedAt))
	fmt.Fprintf(&b, "Chunks: old=%d new=%d unchanged=%d changed=%d added=%d removed=%d\n", s.OldChunkCount, s.NewChunkCount, s.UnchangedCount, s.ChangedCount, s.AddedCount, s.RemovedCount)
	b.WriteString("\nCache Layer       Hits  Misses  Hit Rate\n")
	b.WriteString("-----------       ----  ------  --------\n")
	for layer, ls := range layerMap {
		rate := 0.0
		total := ls.Hits + ls.Misses
		if total > 0 { rate = float64(ls.Hits) / float64(total) * 100 }
		fmt.Fprintf(&b, "%-17s %4d  %6d  %7.1f%%\n", layer, ls.Hits, ls.Misses, rate)
	}
	return b.String()
}
