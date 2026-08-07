package postgres

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm/clause"
)

type metadataPlaceholder func(int) string

func questionMarkMetadataPlaceholder(int) string { return "?" }

func postgresMetadataPlaceholder(position int) string {
	return "$" + strconv.Itoa(position)
}

type metadataFilterBinder struct {
	placeholder metadataPlaceholder
	next        int
	args        []interface{}
}

func (b *metadataFilterBinder) bind(value interface{}) string {
	b.next++
	b.args = append(b.args, value)
	return b.placeholder(b.next)
}

// compileMetadataFilter converts a validated MetadataFilter to a parameterized
// JSONB predicate. The offset is the count of parameters already present in a
// raw PostgreSQL query; it is ignored by question-mark binders.
func compileMetadataFilter(
	filter *types.MetadataFilter,
	placeholder metadataPlaceholder,
	offset int,
) (string, []interface{}, error) {
	if filter == nil {
		return "", nil, nil
	}
	if placeholder == nil {
		return "", nil, fmt.Errorf("metadata filter placeholder is nil")
	}
	if err := filter.Validate(); err != nil {
		return "", nil, err
	}

	binder := &metadataFilterBinder{placeholder: placeholder, next: offset}
	predicate, err := compileMetadataFilterNode(filter, binder)
	if err != nil {
		return "", nil, err
	}
	return predicate, binder.args, nil
}

func compileMetadataFilterNode(filter *types.MetadataFilter, binder *metadataFilterBinder) (string, error) {
	if filter.And != nil {
		return compileMetadataFilterGroup(filter.And, "AND", binder)
	}
	if filter.Or != nil {
		return compileMetadataFilterGroup(filter.Or, "OR", binder)
	}

	switch filter.Op {
	case types.MetadataFilterOpEqual:
		return compileMetadataFilterEqual(filter.Field, filter.Value, binder)
	case types.MetadataFilterOpIn:
		predicates := make([]string, 0, len(filter.Values))
		for _, value := range filter.Values {
			predicate, err := compileMetadataFilterEqual(filter.Field, value, binder)
			if err != nil {
				return "", err
			}
			predicates = append(predicates, predicate)
		}
		return "(" + strings.Join(predicates, " OR ") + ")", nil
	default:
		// Keep this guard even though Validate normally catches it: callers must
		// never receive an unfiltered query for an unsupported operator.
		return "", fmt.Errorf("unsupported metadata filter operator %q", filter.Op)
	}
}

func compileMetadataFilterGroup(
	children []types.MetadataFilter,
	operator string,
	binder *metadataFilterBinder,
) (string, error) {
	predicates := make([]string, 0, len(children))
	for i := range children {
		predicate, err := compileMetadataFilterNode(&children[i], binder)
		if err != nil {
			return "", err
		}
		predicates = append(predicates, predicate)
	}
	return "(" + strings.Join(predicates, " "+operator+" ") + ")", nil
}

func compileMetadataFilterEqual(field string, value interface{}, binder *metadataFilterBinder) (string, error) {
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode metadata filter value: %w", err)
	}

	// JSONB containment covers both persisted forms: the first alternative
	// matches a scalar value, and the second matches a scalar member of a
	// persisted JSON array.
	return fmt.Sprintf(
		"(access_metadata @> jsonb_build_object(%s, %s::jsonb) OR access_metadata @> jsonb_build_object(%s, jsonb_build_array(%s::jsonb)))",
		binder.bind(field), binder.bind(string(encodedValue)),
		binder.bind(field), binder.bind(string(encodedValue)),
	), nil
}

func buildKeywordRetrieveConditions(params types.RetrieveParams) ([]clause.Expression, error) {
	conds := make([]clause.Expression, 0)
	if len(params.KnowledgeBaseIDs) > 0 {
		conds = append(conds, clause.IN{
			Column: "knowledge_base_id",
			Values: common.ToInterfaceSlice(params.KnowledgeBaseIDs),
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		conds = append(conds, clause.IN{
			Column: "knowledge_id",
			Values: common.ToInterfaceSlice(params.KnowledgeIDs),
		})
	}
	if len(params.TagIDs) > 0 {
		conds = append(conds, clause.IN{
			Column: "tag_id",
			Values: common.ToInterfaceSlice(params.TagIDs),
		})
	}
	if params.MetadataFilter != nil {
		predicate, args, err := compileMetadataFilter(params.MetadataFilter, questionMarkMetadataPlaceholder, 0)
		if err != nil {
			return nil, fmt.Errorf("compile metadata filter: %w", err)
		}
		conds = append(conds, clause.Expr{SQL: predicate, Vars: args})
	}
	conds = append(conds,
		clause.Expr{SQL: "content ||| ?", Vars: []interface{}{params.Query}},
		clause.Expr{SQL: "(is_enabled IS NULL OR is_enabled = ?)", Vars: []interface{}{true}},
	)
	return conds, nil
}

func buildVectorRetrieveQuery(params types.RetrieveParams) (string, []interface{}, error) {
	dimension := len(params.Embedding)
	allVars := []interface{}{pgvector.NewHalfVector(params.Embedding)}
	whereParts := []string{fmt.Sprintf("dimension = $%d", len(allVars)+1)}
	allVars = append(allVars, dimension)

	appendIDs := func(column string, ids []string) {
		if len(ids) == 0 {
			return
		}
		placeholders := make([]string, len(ids))
		paramStart := len(allVars) + 1
		for i := range ids {
			placeholders[i] = fmt.Sprintf("$%d", paramStart+i)
			allVars = append(allVars, ids[i])
		}
		whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ", ")))
	}
	appendIDs("knowledge_base_id", params.KnowledgeBaseIDs)
	appendIDs("knowledge_id", params.KnowledgeIDs)
	appendIDs("tag_id", params.TagIDs)

	whereParts = append(whereParts, fmt.Sprintf("(is_enabled IS NULL OR is_enabled = $%d)", len(allVars)+1))
	allVars = append(allVars, true)
	if params.MetadataFilter != nil {
		predicate, args, err := compileMetadataFilter(params.MetadataFilter, postgresMetadataPlaceholder, len(allVars))
		if err != nil {
			return "", nil, fmt.Errorf("compile metadata filter: %w", err)
		}
		whereParts = append(whereParts, predicate)
		allVars = append(allVars, args...)
	}

	expandedTopK := vectorCandidateLimit(params.TopK)
	subqueryLimitParam := len(allVars) + 1
	thresholdParam := len(allVars) + 2
	finalLimitParam := len(allVars) + 3
	whereClause := "WHERE " + strings.Join(whereParts, " AND ")
	querySQL := fmt.Sprintf(`
		SELECT
			id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
			(1 - distance) as score
		FROM (
			SELECT
				id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
				embedding::halfvec(%[1]d) <=> $1::halfvec(%[1]d) as distance
			FROM embeddings
			%[2]s
			ORDER BY embedding::halfvec(%[1]d) <=> $1::halfvec(%[1]d)
			LIMIT $%[3]d
		) AS candidates
		WHERE distance <= $%[4]d
		ORDER BY distance ASC
		LIMIT $%[5]d
	`, dimension, whereClause, subqueryLimitParam, thresholdParam, finalLimitParam)

	allVars = append(allVars, expandedTopK, 1-params.Threshold, params.TopK)
	return querySQL, allVars, nil
}

func vectorCandidateLimit(topK int) int {
	expandedTopK := topK * 2
	if expandedTopK < 100 {
		expandedTopK = 100
	}
	if expandedTopK > 200 {
		expandedTopK = 200
	}
	if expandedTopK < topK {
		expandedTopK = topK
	}
	return expandedTopK
}
