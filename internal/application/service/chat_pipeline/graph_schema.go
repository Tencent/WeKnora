package chatpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const schemaExtractionPrompt = `Extract only entities and directed relations explicitly supported by the passage.
The domain schema and output JSON Schema below are mandatory. Descriptions are semantic guidance,
not permission to change the output contract. Return a single JSON object, without markdown or prose.
Assign unique local IDs to entities, reference those IDs in relations, and use exact declared type names.
Do not output attributes or invent missing endpoints. Merge repeated mentions of the same name and type.
If nothing matches, return {"entities":[],"relations":[]}.
Domain schema:
%s
Output JSON Schema:
%s`

func (e *Extractor) extractWithSchema(ctx context.Context, content string) (*types.GraphData, error) {
	schema := e.template.Schema
	if err := schema.Validate(); err != nil {
		return nil, err
	}
	document := schema.ExtractionJSONSchema()
	validator, err := types.CompileGraphJSONSchema(document)
	if err != nil {
		return nil, err
	}
	domainJSON, _ := json.Marshal(schema)
	outputJSON, _ := json.Marshal(document)
	prompt := fmt.Sprintf(schemaExtractionPrompt, domainJSON, outputJSON)
	prompt = types.AppendCustomPromptInstructions(prompt, e.template.Description, "graph_extraction")
	messages := []chat.Message{{Role: "system", Content: prompt}}
	for _, example := range e.template.Examples {
		if strings.TrimSpace(example.Text) == "" || len(example.Node) == 0 {
			continue
		}
		output, ok := schemaExampleJSON(example)
		if !ok {
			continue
		}
		// Old untyped examples may survive a mode switch. Only include examples
		// that pass the same contract used for generated extractions.
		if _, err := schema.ParseExtraction(output, validator); err != nil {
			continue
		}
		messages = append(messages, chat.Message{Role: "user", Content: example.Text},
			chat.Message{Role: "assistant", Content: output})
	}
	messages = append(messages, chat.Message{Role: "user", Content: content})
	ctx = types.WithLLMCallMetadata(ctx, "entity_extraction", "")
	// Native strict output is not universally available through the existing
	// cross-provider model contract; backend validation is always required.
	for attempt := 0; attempt < 2; attempt++ {
		response, callErr := e.chat.Chat(ctx, messages, e.chatOpt)
		if callErr != nil {
			return nil, callErr
		}
		if response == nil {
			return nil, fmt.Errorf("empty schema extraction response")
		}
		if IsLengthFinishReason(response.FinishReason) || response.FinishReason == types.FinishReasonIncomplete {
			return nil, fmt.Errorf("schema extraction output was truncated")
		}
		graph, parseErr := schema.ParseExtraction(e.formater.extractContent(ctx, response.Content), validator)
		if parseErr == nil {
			return graph, nil
		}
		err = parseErr
		if attempt == 0 {
			messages = append(messages, chat.Message{Role: "assistant", Content: response.Content},
				chat.Message{Role: "user", Content: "Correct the previous output to satisfy the schema. " +
					"Do not add facts. Validation error: " + parseErr.Error()})
		}
	}
	return nil, fmt.Errorf("schema extraction failed after one repair: %w", err)
}

func schemaExampleJSON(example types.GraphData) (string, bool) {
	entities := []map[string]string{}
	relations := []map[string]string{}
	ids := map[[2]string]string{}
	for index, node := range example.Node {
		if node == nil || node.EntityType == "" {
			return "", false
		}
		key := [2]string{node.Name, node.EntityType}
		if ids[key] != "" {
			return "", false
		}
		id := fmt.Sprintf("e%d", index+1)
		ids[key] = id
		entities = append(entities, map[string]string{"id": id, "name": node.Name, "type": node.EntityType})
	}
	for _, relation := range example.Relation {
		if relation == nil {
			return "", false
		}
		source := ids[[2]string{relation.Node1, relation.SourceType}]
		target := ids[[2]string{relation.Node2, relation.TargetType}]
		if source == "" || target == "" {
			return "", false
		}
		relations = append(relations, map[string]string{"source": source, "target": target, "type": relation.Type})
	}
	output, err := json.Marshal(map[string]any{"entities": entities, "relations": relations})
	return string(output), err == nil
}

// ExtractSchemaQueries uses types as optional hints, never as access scopes.
func ExtractSchemaQueries(ctx context.Context, model chat.Chat, schema *types.GraphSchema, query string) ([]types.GraphQuery, error) {
	if err := schema.Validate(); err != nil {
		return nil, err
	}
	optionalEnum := func(names []string) map[string]any {
		return map[string]any{"type": "string", "enum": append([]string{""}, names...)}
	}
	document := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"queries"},
		"properties": map[string]any{"queries": map[string]any{
			"type": "array", "maxItems": 8,
			"items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"name", "entity_type", "relation_type", "direction", "target_type"},
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
					"entity_type": optionalEnum(schema.EntityNames()), "target_type": optionalEnum(schema.EntityNames()),
					"relation_type": optionalEnum(schema.RelationNames()),
					"direction":     optionalEnum([]string{"in", "out"}),
				},
			},
		}},
	}
	validator, err := types.CompileGraphJSONSchema(document)
	if err != nil {
		return nil, err
	}
	domain, _ := json.Marshal(schema)
	output, _ := json.Marshal(document)
	prompt := "Identify named entity search seeds and relevant one-hop relations from the question. " +
		"Do not invent entity names. Use exact schema names. direction is relative to the named seed: " +
		"out for outgoing, in for incoming. target_type is the neighbor type. " +
		"Use empty strings for uncertain type, relation or direction; return an empty queries array if there are no named seeds. " +
		"Return only JSON following this output schema:\n" + string(output) + "\nDomain schema:\n" + string(domain)
	thinking := false
	response, err := model.Chat(types.WithLLMCallMetadata(ctx, "entity_query", ""),
		[]chat.Message{{Role: "system", Content: prompt}, {Role: "user", Content: query}},
		&chat.ChatOptions{Temperature: 0, MaxTokens: 2048, Thinking: &thinking})
	if err != nil {
		return nil, err
	}
	if response == nil || IsLengthFinishReason(response.FinishReason) || response.FinishReason == types.FinishReasonIncomplete {
		return nil, fmt.Errorf("empty or truncated schema query response")
	}
	content := NewFormater().extractContent(ctx, response.Content)
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return nil, err
	}
	if err := validator.Validate(value); err != nil {
		return nil, err
	}
	var result struct {
		Queries []types.GraphQuery `json:"queries"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	for _, q := range result.Queries {
		if err := schema.ValidateQuery(q); err != nil {
			return nil, err
		}
	}
	return result.Queries, nil
}
