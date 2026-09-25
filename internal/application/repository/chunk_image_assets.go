package repository

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ListImageAssets compiles one gallery page request into a single query:
// expand every chunk's image_info array, keep one row per image (keyed by URL,
// the most recently updated chunk wins), then filter, sort and page in the
// database. Only the page's rows come back, and the total of the filtered set
// rides along on each of them.
//
// image_info is a text column holding a JSON array. An image the multimodal
// pipeline produced lives on its image_ocr and image_caption children with
// identical JSON; chunks written before 2026-02 may carry several images on
// one text chunk, which is why the array is expanded instead of reading [0].
//
// The expansion reduces each image to narrow scalars (dedup key, sort key and
// one boolean for every filter) so de-duplication and sorting never carry the
// image JSON, whose OCR text can run to kilobytes; the JSON is read back from
// chunks for the page's rows only.
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
	filtered, args := d.filteredImageAssets(tenantID, kbID, q)
	dir := " ASC"
	if q.SortDesc {
		dir = " DESC"
	}
	order := "sort_key" + dir + ", chunk_id ASC, image_index ASC"
	page := "SELECT chunk_id, image_index, ROW_NUMBER() OVER (ORDER BY " + order + ") AS ord, " +
		"COUNT(*) OVER () AS total_count FROM (" + filtered + ") f ORDER BY ord LIMIT ? OFFSET ?"
	query := "SELECT c.id AS chunk_id, c.knowledge_id, c.chunk_type, c.is_enabled, c.status, " +
		"c.created_at, c.updated_at, p.image_index, " + d.element("c.image_info", "p.image_index") +
		" AS image_json, p.total_count FROM (" + page + ") p JOIN chunks c ON c.id = p.chunk_id ORDER BY p.ord"

	var rows []types.ImageAssetRow
	var total int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if d.postgres {
			// The planner assumes every jsonb_array_elements call yields 100
			// rows, so it prices this query ~100x too high and JIT-compiles
			// it; on a KB of 20k images that compilation alone costs more
			// than the query. Most arrays hold a single image.
			if err := tx.Exec("SET LOCAL jit = off").Error; err != nil {
				return err
			}
		}
		pageArgs := append(append([]any{}, args...), limit, offset)
		if err := tx.Raw(query, pageArgs...).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			total = rows[0].TotalCount
			return nil
		}
		if offset == 0 {
			return nil
		}
		// A page past the end carries no row to read the total from.
		return tx.Raw("SELECT COUNT(*) FROM ("+filtered+") f", args...).Scan(&total).Error
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// imageAssetDialect holds the few expressions postgres and sqlite spell
// differently: array expansion and JSON field extraction. Everything else in
// the gallery query is shared SQL.
type imageAssetDialect struct {
	postgres bool
}

// filteredImageAssets returns the de-duplicated, filtered image set as a
// subquery of (chunk_id, image_index, sort_key), with its bind arguments in
// order.
//
// It runs in two passes. The first expands every array but only extracts the
// URL, so de-duplication sorts narrow rows. The second re-reads just the entry
// that won for each image and evaluates the sort key and the filters on it:
// filters judge the winning copy as if the duplicates had never existed, and
// the keyword scan touches each image's text once instead of once per copy.
func (d imageAssetDialect) filteredImageAssets(
	tenantID uint64, kbID string, q *types.ImageAssetQuery,
) (string, []any) {
	index := "CAST(e.key AS INTEGER)"
	expand := "FROM chunks c, json_each(CASE WHEN json_valid(c.image_info) THEN c.image_info ELSE '[]' END) AS e"
	// sqlite has no LATERAL; filtering json_each on the key picks the entry.
	reread := ", json_each(c.image_info) AS e"
	rereadWhere := " AND e.key = w.image_index"
	if d.postgres {
		index = "(e.idx - 1)"
		expand = "FROM chunks c CROSS JOIN LATERAL jsonb_array_elements(c.image_info::jsonb) " +
			"WITH ORDINALITY AS e(img, idx)"
		// OFFSET 0 keeps postgres from inlining the subquery, which would
		// re-parse the JSON once per expression that reads the entry.
		reread = " CROSS JOIN LATERAL (SELECT c.image_info::jsonb -> CAST(w.image_index AS INTEGER) AS img OFFSET 0) e"
		rereadWhere = ""
	}
	dedupKey := "COALESCE(NULLIF(" + d.text("url") + ", ''), NULLIF(" + d.text("original_url") +
		", ''), c.id || '#' || " + index + ")"
	// The LIKE guard keeps non-array values out of the expansion; sqlite also
	// swaps invalid JSON for an empty array above, because json_each aborts
	// the whole statement on malformed input.
	base := "SELECT c.id AS chunk_id, " + index + " AS image_index, c.created_at, c.updated_at, c.is_enabled, " +
		dedupKey + " AS dedup_key " +
		expand + " WHERE c.tenant_id = ? AND c.knowledge_base_id = ? AND c.deleted_at IS NULL " +
		"AND c.image_info LIKE '[%'"
	winners := "SELECT chunk_id, image_index, created_at, updated_at, is_enabled FROM (SELECT b.*, " +
		"ROW_NUMBER() OVER (PARTITION BY dedup_key ORDER BY updated_at DESC, chunk_id ASC, image_index ASC) " +
		"AS rn FROM (" + base + ") b) r WHERE rn = 1"
	args := []any{tenantID, kbID}
	if !readsImageEntry(q) {
		// Timestamps and the enabled flag rode along with the winners, so a
		// plain listing never goes back to the JSON.
		where := "1 = 1"
		if q.IsEnabled != nil {
			where = "is_enabled = ?"
			args = append(args, *q.IsEnabled)
		}
		sortKey := "created_at"
		if f := q.SortField.Builtin; f == "updated_at" || f == "is_enabled" {
			sortKey = f
		}
		return "SELECT chunk_id, image_index, " + sortKey + " AS sort_key FROM (" + winners + ") w WHERE " + where, args
	}
	keep, keepArgs := d.keep(q)
	filtered := "SELECT w.chunk_id, w.image_index, " + d.sortKey(q.SortField) + " AS sort_key FROM (" +
		winners + ") w JOIN chunks c ON c.id = w.chunk_id" + reread + " WHERE " + keep + rereadWhere
	return filtered, append(args, keepArgs...)
}

// readsImageEntry reports whether the query filters or sorts on anything
// stored inside the image entry, as opposed to the chunk's own columns.
func readsImageEntry(q *types.ImageAssetQuery) bool {
	if strings.TrimSpace(q.Keyword) != "" || len(q.AttrFilters) > 0 || len(q.OnRules) > 0 || len(q.OffRules) > 0 {
		return true
	}
	switch q.SortField.Builtin {
	case "created_at", "updated_at", "is_enabled":
		return false
	}
	_, ok := (imageAssetDialect{}).value(q.SortField)
	return ok
}

// keep is one boolean per image row that is true when the query's filters let
// the image through. Every clause is written to be non-NULL, so the NOT in the
// verdict clause cannot turn an unobserved image into a hidden one.
func (d imageAssetDialect) keep(q *types.ImageAssetQuery) (string, []any) {
	var clauses []string
	var args []any
	if q.IsEnabled != nil {
		clauses = append(clauses, "c.is_enabled = ?")
		args = append(args, *q.IsEnabled)
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		parts := make([]string, 0, len(q.SearchFields))
		for _, f := range q.SearchFields {
			if expr, ok := d.value(f); ok {
				parts = append(parts, "COALESCE("+expr+", '')")
			}
		}
		if len(parts) == 0 {
			clauses = append(clauses, "1 = 0")
		} else {
			// sqlite's LOWER only folds ASCII, so non-ASCII keywords match
			// case-sensitively there; postgres folds per the database locale.
			clauses = append(clauses,
				"LOWER("+strings.Join(parts, " || ' ' || ")+") LIKE ? ESCAPE '"+likeEscapeChar+"'")
			args = append(args, "%"+escapeLikeKeyword(strings.ToLower(kw))+"%")
		}
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
	if len(clauses) == 0 {
		return "(1 = 1)", nil
	}
	return "(" + strings.Join(clauses, " AND ") + ")", args
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

// sortKey is the value images are ordered by; ties fall back to chunk and
// position so pages are deterministic (every image of one chunk shares its
// timestamps). An image without a value for the field sorts as the empty
// string.
func (d imageAssetDialect) sortKey(f types.ImageAssetField) string {
	switch f.Builtin {
	case "created_at", "updated_at":
		return "c." + f.Builtin
	case "is_enabled":
		return "c.is_enabled"
	}
	if expr, ok := d.value(f); ok {
		return "LOWER(COALESCE(" + expr + ", ''))"
	}
	return "c.created_at"
}

// value is the normalized text of a field for one image row, NULL when the
// image carries none. Booleans read as "true"/"false" on both backends, the
// same form the gallery contract lists values in. Timestamps are sort-only:
// they have no text form to search or filter on.
func (d imageAssetDialect) value(f types.ImageAssetField) (string, bool) {
	switch f.Builtin {
	case "":
	case "caption", "ocr_text":
		return d.text(f.Builtin), true
	case "is_enabled":
		return "(CASE WHEN c.is_enabled THEN 'true' ELSE 'false' END)", true
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
		return "(e.img->'attrs'->'attrs'->>'" + name + "')", true
	}
	path := `'$.attrs.attrs."` + name + `"'`
	return "(CASE json_type(e.value, " + path + ") WHEN 'true' THEN 'true' WHEN 'false' THEN 'false' " +
		"ELSE CAST(json_extract(e.value, " + path + ") AS TEXT) END)", true
}

// text extracts a top-level string field of the expanded image entry.
func (d imageAssetDialect) text(key string) string {
	if d.postgres {
		return "(e.img->>'" + key + "')"
	}
	return "json_extract(e.value, '$." + key + "')"
}

// element reads one entry of an image_info array back as JSON text.
func (d imageAssetDialect) element(column, index string) string {
	if d.postgres {
		return "(" + column + "::jsonb -> CAST(" + index + " AS INTEGER))::text"
	}
	return "json_extract(" + column + ", '$[' || " + index + " || ']')"
}
