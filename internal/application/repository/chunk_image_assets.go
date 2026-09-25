package repository

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ListImageAssets returns one page of a knowledge base's images from
// chunk_images, the projection of chunks.image_info that database triggers
// maintain (migrations versioned/000113, sqlite/000032). An image copied onto
// several chunks is listed once, as the copy no other copy beats (see
// winnerClause), which an index probe per candidate row decides. A plain
// listing therefore reads the page off the listing index and stops; filters
// narrow the candidates before the probe.
func (r *chunkRepository) ListImageAssets(
	ctx context.Context, tenantID uint64, kbID string, q *types.ImageAssetQuery,
) ([]types.ImageAssetRow, int64, error) {
	if q == nil {
		q = &types.ImageAssetQuery{}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := max(q.Offset, 0)

	d := imageAssetDialect{postgres: r.db.Name() == "postgres"}
	where, args, filtered := d.where(tenantID, kbID, q)
	// Unfiltered, the total is the number of distinct images, which the
	// copies index answers without probing every row.
	count, countArgs := "SELECT COUNT(*) FROM chunk_images i WHERE "+where, args
	if !filtered {
		countArgs = args[:2]
		// DISTINCT in a subquery, not COUNT(DISTINCT): postgres always sorts
		// for the latter, while the former walks the copies index in order.
		count = "SELECT COUNT(*) FROM (SELECT DISTINCT i.image_key FROM chunk_images i " +
			"WHERE i.knowledge_base_id = ? AND i.tenant_id = ?) d"
	}
	dir := " ASC"
	if q.SortDesc {
		dir = " DESC"
	}
	// Ties (every image of one chunk shares its timestamps) follow the sort
	// direction, so a created_at listing is one backward walk of the index.
	order := d.sortKey(q.SortField) + dir + ", i.chunk_id" + dir + ", i.image_index" + dir
	query := "SELECT i.chunk_id, i.knowledge_id, i.chunk_type, i.is_enabled, i.status, i.created_at, " +
		"i.updated_at, i.image_index, i.url, i.original_url, i.caption, i.ocr_text, " + d.attrsText() +
		" AS attrs_json, (" + count + ") AS total_count " +
		"FROM chunk_images i WHERE " + where + " ORDER BY " + order + " LIMIT ? OFFSET ?"

	var rows []types.ImageAssetRow
	pageArgs := make([]any, 0, len(countArgs)+len(args)+2)
	pageArgs = append(append(append(pageArgs, countArgs...), args...), limit, offset)
	if err := r.db.WithContext(ctx).Raw(query, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	if len(rows) > 0 {
		return rows, rows[0].TotalCount, nil
	}
	if offset == 0 {
		return rows, 0, nil
	}
	// A page past the end carries no row to read the total from.
	var total int64
	err := r.db.WithContext(ctx).Raw(count, countArgs...).Scan(&total).Error
	return rows, total, err
}

// winnerClause keeps one copy per image: the one on the most recently updated
// chunk, ties broken by chunk id and then array position. The copies index
// serves the probe.
const winnerClause = "NOT EXISTS (SELECT 1 FROM chunk_images o " +
	"WHERE o.knowledge_base_id = i.knowledge_base_id AND o.tenant_id = i.tenant_id " +
	"AND o.image_key = i.image_key AND (o.updated_at > i.updated_at " +
	"OR (o.updated_at = i.updated_at AND (o.chunk_id < i.chunk_id " +
	"OR (o.chunk_id = i.chunk_id AND o.image_index < i.image_index)))))"

// imageAssetDialect holds the expressions postgres and sqlite spell
// differently: reading the attrs JSON and matching the keyword.
type imageAssetDialect struct {
	postgres bool
}

// where is the predicate selecting the query's images: the listed copy of
// each image of the knowledge base that passes the filters. Filters judge the
// listed copy, as if the other copies did not exist. Every clause is written
// to be non-NULL, so the NOT in the verdict clause cannot turn an unobserved
// image into a hidden one. filtered reports whether anything beyond the
// knowledge base scope constrains the result.
func (d imageAssetDialect) where(
	tenantID uint64, kbID string, q *types.ImageAssetQuery,
) (sql string, args []any, filtered bool) {
	clauses := []string{"i.knowledge_base_id = ?", "i.tenant_id = ?"}
	args = []any{kbID, tenantID}
	if q.IsEnabled != nil {
		clauses = append(clauses, "i.is_enabled = ?")
		args = append(args, *q.IsEnabled)
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		pattern := "%" + escapeLikeKeyword(kw) + "%"
		var matches []string
		for _, f := range q.SearchFields {
			expr, ok := d.value(f)
			if !ok {
				continue
			}
			if d.postgres {
				// ILIKE with the default backslash escape, the form the
				// trigram indexes on caption / ocr_text can serve.
				matches = append(matches, expr+" ILIKE ?")
			} else {
				// sqlite's LIKE folds ASCII case only, so non-ASCII keywords
				// match case-sensitively there.
				matches = append(matches, expr+" LIKE ? ESCAPE '"+likeEscapeChar+"'")
			}
			args = append(args, pattern)
		}
		if len(matches) == 0 {
			matches = []string{"1 = 0"}
		}
		clauses = append(clauses, "("+strings.Join(matches, " OR ")+")")
	}
	for _, set := range q.AttrFilters {
		expr, ok := d.value(set.Field)
		if !ok || len(set.Values) == 0 {
			continue
		}
		// An image never observed for the attribute is not constrained by
		// it: absence is silence, not a mismatch.
		clauses = append(clauses, "("+expr+" IS NULL OR "+expr+" IN ?)")
		args = append(args, set.Values)
	}
	if off, offArgs := d.anyCarries(q.OffRules); off != "" {
		// An "off" hides the images carrying its value unless an "on" claims
		// the same image: a forced display outranks a forced hide.
		on, onArgs := d.anyCarries(q.OnRules)
		if on == "" {
			on = "1 = 0"
		}
		clauses = append(clauses, "(("+on+") OR NOT ("+off+"))")
		args = append(append(args, onArgs...), offArgs...)
	}
	filtered = len(clauses) > 2
	return strings.Join(append(clauses, winnerClause), " AND "), args, filtered
}

// anyCarries is true for an image carrying any of the sets' values. An image
// without a value for a field never matches.
func (d imageAssetDialect) anyCarries(sets []types.ImageAssetValueSet) (string, []any) {
	var clauses []string
	var args []any
	for _, set := range sets {
		expr, ok := d.value(set.Field)
		if !ok || len(set.Values) == 0 {
			continue
		}
		clauses = append(clauses, "("+expr+" IS NOT NULL AND "+expr+" IN ?)")
		args = append(args, set.Values)
	}
	return strings.Join(clauses, " OR "), args
}

// sortKey is the value images are ordered by. An image without a value for
// the field sorts as the empty string.
func (d imageAssetDialect) sortKey(f types.ImageAssetField) string {
	switch f.Builtin {
	case "created_at", "updated_at", "is_enabled":
		return "i." + f.Builtin
	}
	if expr, ok := d.value(f); ok {
		return "LOWER(COALESCE(" + expr + ", ''))"
	}
	return "i.created_at"
}

// value is the normalized text of a field for one image, NULL when the image
// carries none. Booleans read as "true"/"false" on both backends, the same
// form the gallery contract lists values in. Timestamps are sort-only: they
// have no text form to search or filter on.
func (d imageAssetDialect) value(f types.ImageAssetField) (string, bool) {
	switch f.Builtin {
	case "":
	case "caption", "ocr_text":
		return "i." + f.Builtin, true
	case "is_enabled":
		return "(CASE WHEN i.is_enabled THEN 'true' ELSE 'false' END)", true
	default:
		return "", false
	}
	name := f.Attr
	// The name is embedded as a literal (validated against the gallery
	// contract upstream); refuse anything that could leave the JSON path.
	if name == "" || strings.ContainsAny(name, `'"\`) {
		return "", false
	}
	if d.postgres {
		return "(i.attrs->>'" + name + "')", true
	}
	path := `'$."` + name + `"'`
	return "(CASE json_type(i.attrs, " + path + ") WHEN 'true' THEN 'true' WHEN 'false' THEN 'false' " +
		"ELSE CAST(json_extract(i.attrs, " + path + ") AS TEXT) END)", true
}

// attrsText reads the attrs column back as JSON text.
func (d imageAssetDialect) attrsText() string {
	if d.postgres {
		return "i.attrs::text"
	}
	return "i.attrs"
}
