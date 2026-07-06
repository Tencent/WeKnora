package mysql

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultTablePrefix = "weknora_embeddings"
	envTablePrefix     = "MYSQL_TABLE_PREFIX"
	fieldEmbedding     = "embedding"
)

type whereCond struct {
	clause string
	args   []any
}

type whereBuilder struct {
	alias string
	conds []whereCond
}

func newWhereBuilder(alias string) *whereBuilder {
	return &whereBuilder{alias: alias}
}

func (w *whereBuilder) field(name string) string {
	if w.alias == "" {
		return "`" + name + "`"
	}
	return w.alias + ".`" + name + "`"
}

func (w *whereBuilder) addEqual(field string, value any) {
	w.conds = append(w.conds, whereCond{
		clause: w.field(field) + " = ?",
		args:   []any{value},
	})
}

func (w *whereBuilder) addIn(field string, values []string) {
	if len(values) == 0 {
		return
	}
	w.conds = append(w.conds, whereCond{
		clause: w.field(field) + " IN (" + placeholders(len(values)) + ")",
		args:   toAnySlice(values),
	})
}

func (w *whereBuilder) addNotIn(field string, values []string) {
	if len(values) == 0 {
		return
	}
	w.conds = append(w.conds, whereCond{
		clause: w.field(field) + " NOT IN (" + placeholders(len(values)) + ")",
		args:   toAnySlice(values),
	})
}

func (w *whereBuilder) build() (string, []any) {
	if len(w.conds) == 0 {
		return "1 = 1", nil
	}
	parts := make([]string, len(w.conds))
	args := make([]any, 0)
	for i, cond := range w.conds {
		parts[i] = cond.clause
		args = append(args, cond.args...)
	}
	return strings.Join(parts, " AND "), args
}

func buildBaseFilter(params types.RetrieveParams, alias string) *whereBuilder {
	w := newWhereBuilder(alias)
	w.addEqual("is_enabled", true)
	if len(params.KnowledgeBaseIDs) > 0 {
		w.addIn("knowledge_base_id", params.KnowledgeBaseIDs)
	}
	if len(params.KnowledgeIDs) > 0 {
		w.addIn("knowledge_id", params.KnowledgeIDs)
	}
	if len(params.TagIDs) > 0 {
		w.addIn("tag_id", params.TagIDs)
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		w.addNotIn("knowledge_id", params.ExcludeKnowledgeIDs)
	}
	if len(params.ExcludeChunkIDs) > 0 {
		w.addNotIn("chunk_id", params.ExcludeChunkIDs)
	}
	return w
}

func placeholders(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = "?"
	}
	return strings.Join(items, ", ")
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func tableName(prefix string, dim int) string {
	if prefix == "" {
		prefix = defaultTablePrefix
	}
	return fmt.Sprintf("%s_%d", prefix, dim)
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func mysqlEmbeddingSelectList(alias string, usesNativeVector bool) string {
	columns := []string{
		"id",
		"created_at",
		"updated_at",
		"source_id",
		"source_type",
		"chunk_id",
		"knowledge_id",
		"knowledge_base_id",
		"tag_id",
		"content",
		"dimension",
		"is_enabled",
	}
	out := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		out = append(out, qualifiedColumn(alias, column))
	}
	if usesNativeVector {
		out = append(out, fmt.Sprintf("VECTOR_TO_STRING(%s) AS %s", qualifiedColumn(alias, fieldEmbedding), quoteIdent(fieldEmbedding)))
	} else {
		out = append(out, qualifiedColumn(alias, fieldEmbedding))
	}
	return strings.Join(out, ", ")
}

func qualifiedColumn(alias, column string) string {
	if alias == "" {
		return quoteIdent(column)
	}
	return alias + "." + quoteIdent(column)
}

func vectorToJSON(vec []float32) (string, error) {
	if err := validateEmbedding(vec); err != nil {
		return "", err
	}
	raw, err := json.Marshal(vec)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func vectorFromJSON(raw string) ([]float32, error) {
	var values []float32
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func extractEmbedding(params map[string]any, sourceID string) []float32 {
	if params == nil {
		return nil
	}
	embMap, ok := params[fieldEmbedding].(map[string][]float32)
	if !ok {
		return nil
	}
	return embMap[sourceID]
}

func validateEmbedding(vec []float32) error {
	for i, value := range vec {
		f := float64(value)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("mysql: embedding[%d] is not finite: %s", i, strconv.FormatFloat(f, 'g', -1, 32))
		}
	}
	return nil
}

func cosineScore(query, doc []float32) float64 {
	if len(query) == 0 || len(query) != len(doc) {
		return 0
	}
	var dot, qNorm, dNorm float64
	for i := range query {
		q := float64(query[i])
		d := float64(doc[i])
		dot += q * d
		qNorm += q * q
		dNorm += d * d
	}
	if qNorm == 0 || dNorm == 0 {
		return 0
	}
	score := dot / (math.Sqrt(qNorm) * math.Sqrt(dNorm))
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score
}

func applyVectorRanking(rows []*mysqlEmbedding, query []float32, topK int, threshold float64) []*types.IndexWithScore {
	if topK <= 0 {
		topK = 10
	}
	items := make([]*types.IndexWithScore, 0, len(rows))
	for _, row := range rows {
		vec, err := vectorFromJSON(row.Embedding)
		if err != nil {
			continue
		}
		score := cosineScore(query, vec)
		if score < threshold {
			continue
		}
		items = append(items, row.toIndexWithScore(score, types.MatchTypeEmbedding))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > topK {
		items = items[:topK]
	}
	return items
}

func mergeAndLimit(results []*types.IndexWithScore, topK int) []*types.IndexWithScore {
	if topK <= 0 {
		topK = 10
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		return results[:topK]
	}
	return results
}
