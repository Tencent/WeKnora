package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGraphSchema() *GraphSchema {
	return &GraphSchema{
		Enabled:       true,
		EntityTypes:   []GraphEntityType{{Name: "Person"}, {Name: "Company"}},
		RelationTypes: []GraphRelationType{{Name: "WORKS_AT", SourceType: "Person", TargetType: "Company"}},
	}
}

func TestGraphSchemaValidate(t *testing.T) {
	require.NoError(t, (*GraphSchema)(nil).Validate())
	require.NoError(t, (&GraphSchema{}).Validate())
	require.NoError(t, (*ExtractConfig)(nil).ValidateSchema())
	require.Error(t, (&GraphSchema{Enabled: true}).Validate())
	require.NoError(t, testGraphSchema().Validate())
	for _, tc := range []struct {
		name string
		edit func(*GraphSchema)
	}{
		{"duplicate entity", func(s *GraphSchema) { s.EntityTypes = append(s.EntityTypes, s.EntityTypes[0]) }},
		{"empty name", func(s *GraphSchema) { s.EntityTypes[0].Name = "" }},
		{"untrimmed name", func(s *GraphSchema) { s.EntityTypes[0].Name = " Person " }},
		{"control character", func(s *GraphSchema) { s.EntityTypes[0].Name = "Per\nson" }},
		{"long name", func(s *GraphSchema) { s.EntityTypes[0].Name = strings.Repeat("a", 129) }},
		{"long description", func(s *GraphSchema) { s.EntityTypes[0].Description = strings.Repeat("a", 1001) }},
		{"missing endpoint", func(s *GraphSchema) { s.RelationTypes[0].TargetType = "Unknown" }},
		{"duplicate relation", func(s *GraphSchema) { s.RelationTypes = append(s.RelationTypes, s.RelationTypes[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := testGraphSchema()
			tc.edit(schema)
			require.Error(t, schema.Validate())
		})
	}
	schema := testGraphSchema()
	schema.RelationTypes = append(schema.RelationTypes,
		GraphRelationType{Name: "WORKS_AT", SourceType: "Company", TargetType: "Company"})
	require.NoError(t, schema.Validate(), "same label with distinct endpoint definitions is allowed")
	assert.Equal(t, []string{"WORKS_AT"}, schema.RelationNames())
}

func TestGraphSchemaParseExtraction(t *testing.T) {
	schema := testGraphSchema()
	validator, err := CompileGraphJSONSchema(schema.ExtractionJSONSchema())
	require.NoError(t, err)
	const valid = `{"entities":[{"id":"e1","name":"Alex","type":"Person"},{"id":"e2","name":"Alex","type":"Company"}],"relations":[{"source":"e1","target":"e2","type":"WORKS_AT"}]}`
	graph, err := schema.ParseExtraction(valid, validator)
	require.NoError(t, err)
	require.Len(t, graph.Node, 2)
	assert.Equal(t, "Person", graph.Node[0].EntityType)
	assert.Empty(t, graph.Node[0].Attributes)
	assert.Equal(t, "Person", graph.Relation[0].SourceType)
	assert.Equal(t, "Company", graph.Relation[0].TargetType)
	for _, tc := range []struct{ name, value string }{
		{"unknown properties", strings.Replace(valid, `"type":"Person"`, `"type":"Person","attributes":[]`, 1)},
		{"wrong category", strings.Replace(valid, `"type":"Person"`, `"type":"Location"`, 1)},
		{"wrong primitive", strings.Replace(valid, `"name":"Alex"`, `"name":42`, 1)},
		{"duplicate id", strings.Replace(valid, `"id":"e2"`, `"id":"e1"`, 1)},
		{"duplicate identity", strings.Replace(valid, `"type":"Company"`, `"type":"Person"`, 1)},
		{"missing reference", strings.Replace(valid, `"target":"e2"`, `"target":"e9"`, 1)},
		{"wrong relation endpoints", strings.Replace(valid, `"source":"e1","target":"e2"`, `"source":"e2","target":"e1"`, 1)},
		{"missing required field", `{"entities":[]}`},
		{"extra root", `{"entities":[],"relations":[],"attributes":[]}`},
		{"null", `null`},
		{"truncated", valid[:len(valid)-1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := schema.ParseExtraction(tc.value, validator)
			require.Error(t, err)
		})
	}
	graph, err = schema.ParseExtraction(`{"entities":[],"relations":[]}`, validator)
	require.NoError(t, err)
	assert.Empty(t, graph.Node)
	t.Run("entity-only schema", func(t *testing.T) {
		schema.RelationTypes = nil
		validator, err := CompileGraphJSONSchema(schema.ExtractionJSONSchema())
		require.NoError(t, err)
		_, err = schema.ParseExtraction(`{"entities":[{"id":"e1","name":"Alex","type":"Person"}],"relations":[]}`, validator)
		require.NoError(t, err)
		_, err = schema.ParseExtraction(valid, validator)
		require.Error(t, err)
	})
}

func TestGraphSchemaQuery(t *testing.T) {
	schema := testGraphSchema()
	for _, query := range []GraphQuery{
		{Name: "Alex"},
		{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", Direction: "out", TargetType: "Company"},
		{Name: "Acme", EntityType: "Company", RelationType: "WORKS_AT", Direction: "in", TargetType: "Person"},
	} {
		assert.NoError(t, schema.ValidateQuery(query))
	}
	for _, query := range []GraphQuery{
		{Name: ""},
		{Name: "Alex", Direction: "sideways"},
		{Name: "Alex", EntityType: "Undeclared"},
		{Name: "Alex", RelationType: "Undeclared"},
		{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", Direction: "in", TargetType: "Company"},
	} {
		assert.Error(t, schema.ValidateQuery(query))
	}
}

func TestGraphSchemaConfigRoundTrip(t *testing.T) {
	config := ExtractConfig{Enabled: true, Text: "legacy example", Tags: []string{"Author"}, Schema: testGraphSchema()}
	config.Schema.Enabled = false
	data, err := json.Marshal(config)
	require.NoError(t, err)
	var roundTrip ExtractConfig
	require.NoError(t, json.Unmarshal(data, &roundTrip))
	assert.Equal(t, config, roundTrip)
	assert.False(t, roundTrip.HasSchema())
	roundTrip.Schema.Enabled = true
	assert.True(t, roundTrip.HasSchema())
	assert.Equal(t, "legacy example", roundTrip.Text)
}

func TestGraphSchemaQueryMatchesRequestedRelation(t *testing.T) {
	schema := testGraphSchema()
	schema.RelationTypes = append(schema.RelationTypes,
		GraphRelationType{Name: "OWNS", SourceType: "Company", TargetType: "Person"})
	for _, tc := range []struct {
		name    string
		query   GraphQuery
		wantErr bool
	}{
		{"works at outgoing", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", Direction: "out", TargetType: "Company"}, false},
		{"works at incoming", GraphQuery{Name: "Acme", EntityType: "Company", RelationType: "WORKS_AT", Direction: "in", TargetType: "Person"}, false},
		{"owns outgoing", GraphQuery{Name: "Acme", EntityType: "Company", RelationType: "OWNS", Direction: "out", TargetType: "Person"}, false},
		{"owns incoming", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "OWNS", Direction: "in", TargetType: "Company"}, false},
		{"cannot borrow owns endpoints", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", Direction: "in", TargetType: "Company"}, true},
		{"cannot borrow works at endpoints", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "OWNS", Direction: "out", TargetType: "Company"}, true},
		{"unknown source still checks target", GraphQuery{Name: "Alex", RelationType: "WORKS_AT", Direction: "in", TargetType: "Company"}, true},
		{"unknown target still checks source", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", Direction: "in"}, true},
		{"uncertain direction accepts valid endpoints", GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT", TargetType: "Company"}, false},
		{"name only", GraphQuery{Name: "Alex"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantErr {
				require.Error(t, schema.ValidateQuery(tc.query))
			} else {
				require.NoError(t, schema.ValidateQuery(tc.query))
			}
		})
	}
	// Multiple endpoint signatures for the same relation name remain supported.
	schema.RelationTypes = append(schema.RelationTypes,
		GraphRelationType{Name: "WORKS_AT", SourceType: "Company", TargetType: "Company"})
	require.NoError(t, schema.ValidateQuery(GraphQuery{
		Name: "Acme", EntityType: "Company", RelationType: "WORKS_AT", Direction: "out", TargetType: "Company",
	}))
}
