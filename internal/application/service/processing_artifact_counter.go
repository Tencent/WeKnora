package service

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	processingArtifactOutcomeHit = iota
	processingArtifactOutcomeMiss
	processingArtifactOutcomeError
	processingArtifactOutcomeEvicted
	processingArtifactOutcomeWrite
	processingArtifactOutcomeCount
)

var processingArtifactOutcomes = [...]string{
	"hit",
	"miss",
	"error",
	"evicted",
	"write",
}

type processingArtifactStageCounters struct {
	values [processingArtifactOutcomeCount]atomic.Uint64
}

type processingArtifactCounterRegistry struct {
	stages sync.Map
}

func NewProcessingArtifactCounterRegistry() interfaces.ProcessingArtifactCounterRegistry {
	return &processingArtifactCounterRegistry{}
}

func (r *processingArtifactCounterRegistry) Record(stage, outcome string) {
	if !types.IsValidProcessingArtifactStage(stage) {
		return
	}
	index := processingArtifactOutcomeIndex(outcome)
	if index < 0 {
		return
	}
	current, _ := r.stages.LoadOrStore(stage, &processingArtifactStageCounters{})
	current.(*processingArtifactStageCounters).values[index].Add(1)
}

func (r *processingArtifactCounterRegistry) Snapshot() []types.ProcessingArtifactCounter {
	counters := make([]types.ProcessingArtifactCounter, 0)
	r.stages.Range(func(stage, value any) bool {
		for index, outcome := range processingArtifactOutcomes {
			if count := value.(*processingArtifactStageCounters).values[index].Load(); count > 0 {
				counters = append(counters, types.ProcessingArtifactCounter{
					Stage: stage.(string), Outcome: outcome, Count: count,
				})
			}
		}
		return true
	})
	sort.Slice(counters, func(i, j int) bool {
		if counters[i].Stage != counters[j].Stage {
			return counters[i].Stage < counters[j].Stage
		}
		return processingArtifactOutcomeIndex(counters[i].Outcome) < processingArtifactOutcomeIndex(counters[j].Outcome)
	})
	return counters
}

func processingArtifactOutcomeIndex(outcome string) int {
	for index, candidate := range processingArtifactOutcomes {
		if outcome == candidate {
			return index
		}
	}
	return -1
}

func processingArtifactTraceCacheStatus(cacheAttempted, hit bool) string {
	if !cacheAttempted {
		return "bypass"
	}
	if hit {
		return "hit"
	}
	return "miss"
}
