package types

import "testing"

func TestQueueDefinitionsAreUniqueAndConsumable(t *testing.T) {
	definitions := QueueDefinitions()
	if len(definitions) == 0 {
		t.Fatal("queue registry must not be empty")
	}

	validPools := map[string]bool{
		WorkerPoolCore:        true,
		WorkerPoolEnrichment:  true,
		WorkerPoolMaintenance: true,
		WorkerPoolWiki:        true,
	}
	seen := make(map[string]bool, len(definitions))
	seenTaskTypes := make(map[string]string)
	for _, definition := range definitions {
		if seen[definition.Name] {
			t.Fatalf("duplicate queue definition %q", definition.Name)
		}
		seen[definition.Name] = true
		if !validPools[definition.Pool] {
			t.Fatalf("queue %q references unknown pool %q", definition.Name, definition.Pool)
		}
		if definition.Weight <= 0 {
			t.Fatalf("queue %q has non-positive weight %d", definition.Name, definition.Weight)
		}
		if len(definition.TaskTypes) == 0 {
			t.Fatalf("queue %q declares no task types", definition.Name)
		}
		for _, taskType := range definition.TaskTypes {
			if previousQueue, exists := seenTaskTypes[taskType]; exists {
				t.Fatalf("task type %q is declared by both %q and %q", taskType, previousQueue, definition.Name)
			}
			seenTaskTypes[taskType] = definition.Name
			queue, ok := QueueForTaskType(taskType)
			if !ok || queue != definition.Name {
				t.Fatalf("task type %q resolves to queue %q, want %q", taskType, queue, definition.Name)
			}
		}
	}

	for pool := range validPools {
		if len(QueueWeightsForPool(pool)) == 0 {
			t.Fatalf("worker pool %q has no queues", pool)
		}
	}
}

func TestQueueMaintenanceKeepsLegacyPhysicalName(t *testing.T) {
	if QueueMaintenance != "low" {
		t.Fatalf("maintenance queue must keep legacy Redis name during rolling upgrades, got %q", QueueMaintenance)
	}
}

func TestEveryAsynqTaskTypeHasADeclaredQueue(t *testing.T) {
	taskTypes := []string{
		TypeChunkExtract, TypeDocumentProcess, TypeFAQImport,
		TypeQuestionGeneration, TypeSummaryGeneration, TypeKBClone,
		TypeIndexDelete, TypeKBDelete, TypeKnowledgeListDelete,
		TypeKnowledgeListReparse, TypeKnowledgeMove, TypeDataTableSummary,
		TypeImageMultimodal, TypeKnowledgePostProcess, TypeManualProcess,
		TypeDataSourceSync, TypeWikiIngest, TypeWikiFinalize,
	}
	for _, taskType := range taskTypes {
		if _, ok := QueueForTaskType(taskType); !ok {
			t.Fatalf("task type %q has no declared queue", taskType)
		}
	}
}

func TestAllocateWorkerPoolConcurrencyPreservesBudget(t *testing.T) {
	for _, total := range []int{3, 4, 8, 32, 64} {
		allocation := AllocateWorkerPoolConcurrency(total)
		if allocation.Total != total {
			t.Fatalf("total=%d: allocation reported total %d", total, allocation.Total)
		}
		if allocation.Core < 1 || allocation.Enrichment < 1 || allocation.Maintenance < 1 {
			t.Fatalf("total=%d: every pool must receive capacity: %+v", total, allocation)
		}
		if allocation.Core+allocation.Enrichment+allocation.Maintenance != total {
			t.Fatalf("total=%d: split does not preserve budget: %+v", total, allocation)
		}
	}

	minimum := AllocateWorkerPoolConcurrency(1)
	if minimum.Total != 3 || minimum.Core+minimum.Enrichment+minimum.Maintenance != 3 {
		t.Fatalf("undersized budget should clamp to three workers: %+v", minimum)
	}
}
