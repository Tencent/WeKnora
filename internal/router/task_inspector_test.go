package router

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestTaskInspectorMatchesWikiByKnowledgeBaseID(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tenant_id":         7,
		"knowledge_base_id": "kb-1",
	})

	assert.True(t, matchesWikiKnowledgeBase(types.TypeWikiIngest, payload, "kb-1"))
	assert.False(t, matchesWikiKnowledgeBase(types.TypeWikiIngest, payload, "kb-2"))
	assert.False(t, matchesWikiKnowledgeBase(types.TypeDocumentProcess, payload, "kb-1"))
	assert.False(t, matchesKnowledge(types.TypeWikiIngest, payload, "kid-1"),
		"wiki:ingest is KB-scoped and must not be matched by knowledge_id")
}

func TestQueuesScannedIncludesLowForWikiIngest(t *testing.T) {
	assert.Contains(t, queuesScanned, types.QueueLow)
}
