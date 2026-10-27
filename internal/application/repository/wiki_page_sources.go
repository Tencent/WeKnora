package repository

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// filterWikiSourceRefs shares the same source identity predicate between the
// document browser and ingest's existing-page and summary reconciliation.
func (r *wikiPageRepository) filterWikiSourceRefs(query *gorm.DB, knowledgeIDs []string) (*gorm.DB, error) {
	ids := make([]string, 0, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return query.Where("1 = 0"), nil
	}
	if r.wikiDialect() == "sqlite" {
		return query.Where(`EXISTS (SELECT 1 FROM json_each(wiki_pages.source_refs) AS ref
			WHERE ref.value IN ? OR (instr(ref.value, '|') > 0
			AND substr(ref.value, 1, instr(ref.value, '|') - 1) IN ?))`, ids, ids), nil
	}
	clauses := make([]string, 0, len(ids)*2)
	args := make([]interface{}, 0, len(ids)*2)
	for _, id := range ids {
		needle, err := json.Marshal([]string{id})
		if err != nil {
			return nil, fmt.Errorf("marshal source ref needle: %w", err)
		}
		prefix, err := json.Marshal(id + "|")
		if err != nil {
			return nil, fmt.Errorf("marshal source ref prefix: %w", err)
		}
		clauses = append(clauses, "source_refs @> ?::jsonb", "source_refs::text LIKE ?")
		args = append(args, string(needle), "%"+escapeLikePattern(string(prefix[:len(prefix)-1]))+"%")
	}
	return query.Where("("+strings.Join(clauses, " OR ")+")", args...), nil
}
