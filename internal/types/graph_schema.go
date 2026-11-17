package types

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// GraphSchema defines domain types, not entity instances or user-defined properties.
type GraphSchema struct {
	Enabled       bool                `json:"enabled" yaml:"enabled"`
	EntityTypes   []GraphEntityType   `json:"entity_types" yaml:"entity_types"`
	RelationTypes []GraphRelationType `json:"relation_types" yaml:"relation_types"`
}

type GraphEntityType struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type GraphRelationType struct {
	Name        string `json:"name" yaml:"name"`
	SourceType  string `json:"source_type" yaml:"source_type"`
	TargetType  string `json:"target_type" yaml:"target_type"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

func (s *GraphSchema) Validate() error {
	if s == nil || !s.Enabled {
		return nil
	}
	if len(s.EntityTypes) == 0 || len(s.EntityTypes) > 50 || len(s.RelationTypes) > 100 {
		return fmt.Errorf("graph schema requires 1-50 entity types and at most 100 relation definitions")
	}
	names := map[string]bool{}
	for _, entity := range s.EntityTypes {
		if !validGraphTypeName(entity.Name) || utf8.RuneCountInString(entity.Description) > 1000 {
			return fmt.Errorf("invalid graph entity type %q", entity.Name)
		}
		if names[entity.Name] {
			return fmt.Errorf("duplicate graph entity type %q", entity.Name)
		}
		names[entity.Name] = true
	}
	relations := map[[3]string]bool{}
	for _, relation := range s.RelationTypes {
		if !validGraphTypeName(relation.Name) || utf8.RuneCountInString(relation.Description) > 1000 {
			return fmt.Errorf("invalid graph relation type %q", relation.Name)
		}
		if !names[relation.SourceType] || !names[relation.TargetType] {
			return fmt.Errorf("graph relation %q references an undefined entity type", relation.Name)
		}
		key := [3]string{relation.Name, relation.SourceType, relation.TargetType}
		if relations[key] {
			return fmt.Errorf("duplicate graph relation definition %q", relation.Name)
		}
		relations[key] = true
	}
	return nil
}

func validGraphTypeName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) &&
		utf8.ValidString(name) && utf8.RuneCountInString(name) <= 128 &&
		!strings.ContainsAny(name, "\x00\r\n")
}

func (s *GraphSchema) EntityNames() []string {
	names := make([]string, 0, len(s.EntityTypes))
	for _, entity := range s.EntityTypes {
		names = append(names, entity.Name)
	}
	return names
}

func (s *GraphSchema) RelationNames() []string {
	names := []string{}
	for _, relation := range s.RelationTypes {
		if !slices.Contains(names, relation.Name) {
			names = append(names, relation.Name)
		}
	}
	return names
}

func (s *GraphSchema) AllowsRelation(name, source, target string) bool {
	for _, relation := range s.RelationTypes {
		if relation.Name == name && relation.SourceType == source && relation.TargetType == target {
			return true
		}
	}
	return false
}

func (e *ExtractConfig) ValidateSchema() error {
	if e == nil || !e.Enabled {
		return nil
	}
	return e.Schema.Validate()
}

func (e *ExtractConfig) HasSchema() bool {
	return e != nil && e.Schema != nil && e.Schema.Enabled
}

func graphStringSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 512}
}

func graphObjectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
	}
}

// ExtractionJSONSchema is generated from the same domain model used by validation.
func (s *GraphSchema) ExtractionJSONSchema() map[string]any {
	entity := graphObjectSchema(map[string]any{
		"id": graphStringSchema(), "name": graphStringSchema(),
		"type": map[string]any{"type": "string", "enum": s.EntityNames()},
	}, []string{"id", "name", "type"})
	relation := graphObjectSchema(map[string]any{
		"source": graphStringSchema(), "target": graphStringSchema(),
		"type": map[string]any{"type": "string", "enum": s.RelationNames()},
	}, []string{"source", "target", "type"})
	relationArray := map[string]any{"type": "array", "items": relation, "maxItems": 400}
	if len(s.RelationTypes) == 0 {
		// JSON Schema enums cannot be empty, even for an array with no items.
		relationArray = map[string]any{"type": "array", "maxItems": 0}
	}
	return graphObjectSchema(map[string]any{
		"entities":  map[string]any{"type": "array", "items": entity, "maxItems": 200},
		"relations": relationArray,
	}, []string{"entities", "relations"})
}

func CompileGraphJSONSchema(document map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	const url = "https://weknora.invalid/graph-output.json"
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var resource any
	if err := json.Unmarshal(data, &resource); err != nil {
		return nil, err
	}
	if err := compiler.AddResource(url, resource); err != nil {
		return nil, err
	}
	return compiler.Compile(url)
}

type schemaExtraction struct {
	Entities []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entities"`
	Relations []struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Type   string `json:"type"`
	} `json:"relations"`
}

// ParseExtraction rejects undeclared fields before conversion; legacy rebuilding
// must not invent missing endpoints or merge different types by display name.
func (s *GraphSchema) ParseExtraction(content string, validator *jsonschema.Schema) (*GraphData, error) {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return nil, fmt.Errorf("invalid graph JSON: %w", err)
	}
	if err := validator.Validate(value); err != nil {
		return nil, fmt.Errorf("graph output violates JSON Schema: %w", err)
	}
	var output schemaExtraction
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return nil, err
	}
	graph := &GraphData{Node: []*GraphNode{}, Relation: []*GraphRelation{}}
	byID := map[string]*GraphNode{}
	identities := map[[2]string]bool{}
	for _, entity := range output.Entities {
		key := [2]string{entity.Name, entity.Type}
		if strings.TrimSpace(entity.ID) == "" || strings.TrimSpace(entity.Name) == "" ||
			byID[entity.ID] != nil || identities[key] {
			return nil, fmt.Errorf("empty or duplicate graph entity %q", entity.ID)
		}
		node := &GraphNode{Name: entity.Name, EntityType: entity.Type}
		byID[entity.ID] = node
		identities[key] = true
		graph.Node = append(graph.Node, node)
	}
	seen := map[[3]string]bool{}
	for _, relation := range output.Relations {
		source, target := byID[relation.Source], byID[relation.Target]
		if source == nil || target == nil {
			return nil, fmt.Errorf("graph relation references an undefined entity")
		}
		if !s.AllowsRelation(relation.Type, source.EntityType, target.EntityType) {
			return nil, fmt.Errorf("graph relation %q has invalid endpoint types", relation.Type)
		}
		key := [3]string{relation.Source, relation.Target, relation.Type}
		if seen[key] {
			continue
		}
		seen[key] = true
		graph.Relation = append(graph.Relation, &GraphRelation{
			Node1: source.Name, Node2: target.Name, Type: relation.Type,
			SourceType: source.EntityType, TargetType: target.EntityType,
		})
	}
	return graph, nil
}

// GraphQuery describes one bounded, one-hop semantic filter. Empty types mean
// uncertain, not permission to leave the authorized namespace.
type GraphQuery struct {
	Name         string `json:"name"`
	EntityType   string `json:"entity_type"`
	RelationType string `json:"relation_type"`
	Direction    string `json:"direction"`
	TargetType   string `json:"target_type"`
}

func (s *GraphSchema) ValidateQuery(q GraphQuery) error {
	if strings.TrimSpace(q.Name) == "" || utf8.RuneCountInString(q.Name) > 512 {
		return fmt.Errorf("invalid graph query name")
	}
	if q.Direction != "" && q.Direction != "out" && q.Direction != "in" {
		return fmt.Errorf("invalid graph query direction")
	}
	if (q.EntityType != "" && !slices.Contains(s.EntityNames(), q.EntityType)) ||
		(q.TargetType != "" && !slices.Contains(s.EntityNames(), q.TargetType)) ||
		(q.RelationType != "" && !slices.Contains(s.RelationNames(), q.RelationType)) {
		return fmt.Errorf("graph query references an undefined schema type")
	}
	if q.RelationType != "" {
		for _, r := range s.RelationTypes {
			if r.Name != q.RelationType {
				continue
			}
			out := (q.EntityType == "" || r.SourceType == q.EntityType) &&
				(q.TargetType == "" || r.TargetType == q.TargetType)
			in := (q.EntityType == "" || r.TargetType == q.EntityType) &&
				(q.TargetType == "" || r.SourceType == q.TargetType)
			if (q.Direction != "in" && out) || (q.Direction != "out" && in) {
				return nil
			}
		}
		return fmt.Errorf("graph query relation has incompatible endpoint types")
	}
	return nil
}
