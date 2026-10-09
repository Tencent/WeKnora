package service

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Gallery attribute plumbing
//
// The gallery addresses every attribute by its namespaced id
// ("<sourceID>:<name>"). Builtin attributes map onto the asset's own fields;
// every other source reads the generic attribute map stored in image_info.
// The repository evaluates both in SQL. This keeps the gallery decoupled from
// any particular attribute pipeline: it compiles and runs whether or not the
// observation feature is deployed, and new sources light up without changes
// here.
// ---------------------------------------------------------------------------

// galleryBuiltinSourceID is the id of the builtin attribute source the types
// package registers; its values come from asset fields, not image_info.
const galleryBuiltinSourceID = "builtin"

// galleryDefaultSearchFields is the search field set used when the request
// does not name one (older clients, API callers).
var galleryDefaultSearchFields = []string{
	types.GalleryAttrID(galleryBuiltinSourceID, "caption"),
	types.GalleryAttrID(galleryBuiltinSourceID, "ocr_text"),
}

// galleryField maps a namespaced gallery attribute id onto the field the
// repository query reads: builtin ids address the asset's own columns, every
// other source reads the source-local name from the image_info attrs map.
func galleryField(id string) (types.ImageAssetField, bool) {
	source, name, ok := types.SplitGalleryAttrID(id)
	if !ok {
		return types.ImageAssetField{}, false
	}
	if source == galleryBuiltinSourceID {
		return types.ImageAssetField{Builtin: name}, true
	}
	return types.ImageAssetField{Attr: name}, true
}

// buildImageAssetQuery translates the gallery request into the storage-neutral
// query the repository compiles into SQL. Map iteration is sorted so the same
// request always produces the same statement.
func buildImageAssetQuery(filter *types.ImageListFilter, page *types.Pagination) *types.ImageAssetQuery {
	q := &types.ImageAssetQuery{
		SortField: types.ImageAssetField{Builtin: "created_at"},
		SortDesc:  true,
		Offset:    page.Offset(),
		Limit:     page.GetPageSize(),
	}
	if filter == nil {
		return q
	}
	q.Keyword = strings.TrimSpace(filter.Keyword)
	q.IsEnabled = filter.IsEnabled

	searchIn := filter.SearchIn
	if len(searchIn) == 0 {
		searchIn = galleryDefaultSearchFields
	}
	for _, id := range searchIn {
		if f, ok := galleryField(id); ok {
			q.SearchFields = append(q.SearchFields, f)
		}
	}

	for _, id := range sortedKeys(filter.AttrFilters) {
		f, ok := galleryField(id)
		if !ok {
			continue
		}
		var values []string
		for _, v := range filter.AttrFilters[id] {
			values = append(values, strings.TrimSpace(v))
		}
		if len(values) > 0 {
			q.AttrFilters = append(q.AttrFilters, types.ImageAssetValueSet{Field: f, Values: values})
		}
	}

	for _, id := range sortedKeys(filter.AttrRules) {
		f, ok := galleryField(id)
		if !ok {
			continue
		}
		var on, off []string
		verdicts := filter.AttrRules[id]
		for _, value := range sortedKeys(verdicts) {
			switch verdicts[value] {
			case galleryRuleOn:
				on = append(on, value)
			case galleryRuleOff:
				off = append(off, value)
			}
		}
		if len(on) > 0 {
			q.OnRules = append(q.OnRules, types.ImageAssetValueSet{Field: f, Values: on})
		}
		if len(off) > 0 {
			q.OffRules = append(q.OffRules, types.ImageAssetValueSet{Field: f, Values: off})
		}
	}

	switch filter.SortBy {
	case "":
	case "created_at", "updated_at", "caption":
		// Legacy bare field names map onto their builtin ids.
		q.SortField = types.ImageAssetField{Builtin: filter.SortBy}
	default:
		if f, ok := galleryField(filter.SortBy); ok {
			q.SortField = f
		}
	}
	q.SortDesc = filter.SortOrder != "asc"
	return q
}

// The two verdicts an attribute rule can carry.
const (
	galleryRuleOff = "off"
	galleryRuleOn  = "on"
)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
