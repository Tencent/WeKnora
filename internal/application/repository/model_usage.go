package repository

import (
	"strings"

	"gorm.io/gorm"
)

type jsonModelReference struct {
	column string
	path   []string
}

func scopeJSONModelReferences(
	db *gorm.DB,
	modelID string,
	directColumns []string,
	jsonReferences []jsonModelReference,
) *gorm.DB {
	dialect := NewDialect(db)
	conditions := make([]string, 0, len(directColumns)+len(jsonReferences))
	args := make([]interface{}, 0, len(directColumns)+len(jsonReferences))

	for _, column := range directColumns {
		conditions = append(conditions, dialect.QuoteIdentifier(column)+" = ?")
		args = append(args, modelID)
	}
	for _, reference := range jsonReferences {
		expr := dialect.JSONText(reference.column, reference.path...)
		conditions = append(conditions, expr.SQL+" = ?")
		args = append(args, expr.Args...)
		args = append(args, modelID)
	}
	return db.Where(strings.Join(conditions, " OR "), args...)
}

// scopeKnowledgeBasesByModelID filters knowledge_bases rows that reference
// modelID in any model-binding field.
func scopeKnowledgeBasesByModelID(db *gorm.DB, modelID string) *gorm.DB {
	return scopeJSONModelReferences(
		db,
		modelID,
		[]string{"embedding_model_id", "summary_model_id"},
		[]jsonModelReference{
			{column: "image_processing_config", path: []string{"model_id"}},
			{column: "vlm_config", path: []string{"model_id"}},
			{column: "asr_config", path: []string{"model_id"}},
			{column: "wiki_config", path: []string{"synthesis_model_id"}},
		},
	)
}

// scopeCustomAgentsByModelID filters custom_agents rows whose config JSON
// references modelID in any model-binding field.
func scopeCustomAgentsByModelID(db *gorm.DB, modelID string) *gorm.DB {
	return scopeJSONModelReferences(
		db,
		modelID,
		nil,
		[]jsonModelReference{
			{column: "config", path: []string{"model_id"}},
			{column: "config", path: []string{"rerank_model_id"}},
			{column: "config", path: []string{"vlm_model_id"}},
			{column: "config", path: []string{"asr_model_id"}},
			{column: "config", path: []string{"query_understand_model_id"}},
			{column: "config", path: []string{"question_suggestions", "follow_ups", "model_id"}},
		},
	)
}
