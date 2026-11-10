package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGraphRetrievalIdentitiesDoNotChangeExtractionSerialization(t *testing.T) {
	graph := GraphData{
		Node: []*GraphNode{{Name: "Acme", Chunks: []string{"c1"}, ID: "private-node", KnowledgeID: "private-doc"}},
		Relation: []*GraphRelation{{
			Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN",
			ID: "private-relation", SourceID: "private-node", TargetID: "private-target",
		}},
	}
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		data, err := marshal(graph)
		require.NoError(t, err)
		assert.Contains(t, string(data), "Acme")
		assert.NotContains(t, string(data), "private-", "retrieval IDs must not leak into prompts or saved examples")
	}
	var parsed GraphData
	err := json.Unmarshal([]byte(`{"node":[{"name":"Acme","ID":"model-id"}],
		"relation":[{"node1":"Acme","node2":"Shanghai","SourceID":"model-id"}]}`), &parsed)
	require.NoError(t, err)
	assert.Empty(t, parsed.Node[0].ID)
	assert.Empty(t, parsed.Relation[0].SourceID, "extraction JSON cannot supply database identities")
}
