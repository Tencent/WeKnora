package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Gallery attribute plumbing
//
// The gallery reads every attribute through one resolver keyed by the
// namespaced attribute id ("<sourceID>:<name>"). Builtin attributes resolve
// from the asset's own fields; every other source resolves from the generic
// attribute map stored in image_info. This keeps the gallery decoupled from
// any particular attribute pipeline: it compiles and runs whether or not the
// observation feature is deployed, and new sources light up without changes
// here.
// ---------------------------------------------------------------------------

// galleryImageInfo is the gallery's generic read model of one entry in a
// chunk's image_info JSON array. It is deliberately local and free of the
// attribute pipeline's types; the observations are read as a plain map.
type galleryImageInfo struct {
	URL         string `json:"url"`
	OriginalURL string `json:"original_url"`
	Caption     string `json:"caption"`
	OCRText     string `json:"ocr_text"`
	// Attrs mirrors the persisted observation envelope; the usable
	// attribute map is nested one level down under "attrs".
	Attrs struct {
		Attrs map[string]any `json:"attrs"`
	} `json:"attrs"`
}

// galleryBuiltinSourceID is the id of the builtin attribute source the types
// package registers; its values come from asset fields, not image_info.
const galleryBuiltinSourceID = "builtin"

// galleryDefaultSearchFields is the search field set used when the request
// does not name one (older clients, API callers).
var galleryDefaultSearchFields = []string{
	types.GalleryAttrID(galleryBuiltinSourceID, "caption"),
	types.GalleryAttrID(galleryBuiltinSourceID, "ocr_text"),
}

// galleryBuiltinValue resolves one builtin attribute to its normalized
// string value for the asset.
func galleryBuiltinValue(asset types.ImageAsset, name string) (string, bool) {
	switch name {
	case "caption":
		return asset.Caption, true
	case "ocr_text":
		return asset.OCRText, true
	case "created_at":
		return asset.CreatedAt.UTC().Format(time.RFC3339), true
	case "updated_at":
		return asset.UpdatedAt.UTC().Format(time.RFC3339), true
	case "is_enabled":
		return strconv.FormatBool(asset.IsEnabled), true
	}
	return "", false
}

// galleryAttrValue resolves any namespaced gallery attribute id to its
// normalized string value. ok is false when the attribute has no value on
// this image — "unobserved" stays distinct from any observed value.
func galleryAttrValue(asset types.ImageAsset, id string) (string, bool) {
	source, name, ok := types.SplitGalleryAttrID(id)
	if !ok {
		return "", false
	}
	if source == galleryBuiltinSourceID {
		return galleryBuiltinValue(asset, name)
	}
	if v, present := asset.Attrs[name]; present {
		return galleryAttrValueString(v), true
	}
	return "", false
}

// galleryAttrValueString normalizes an arbitrary observed attribute value
// (string / bool / number / array) into the string space search, filter and
// sort all operate in. Arrays join their elements so "keyword list" values
// remain substring-searchable as a whole.
func galleryAttrValueString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, galleryAttrValueString(item))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// filterImageAssets applies keyword, attribute and enabled-state constraints.
// Attribute filters are AND-ed across attributes and OR-ed within one
// attribute's allowed values. An image that has never been observed for one
// of the filtered attributes is not disqualified by it — see matchAttrFilters
// for why absence is not a verdict. The keyword is a case-insensitive
// substring match against the union of the requested search fields.
func filterImageAssets(assets []types.ImageAsset, filter *types.ImageListFilter) []types.ImageAsset {
	if filter == nil {
		return assets
	}
	kw := strings.ToLower(strings.TrimSpace(filter.Keyword))
	fields := filter.SearchIn
	if len(fields) == 0 {
		fields = galleryDefaultSearchFields
	}
	out := assets[:0]
	for _, a := range assets {
		if filter.IsEnabled != nil && a.IsEnabled != *filter.IsEnabled {
			continue
		}
		if kw != "" {
			var b strings.Builder
			for _, f := range fields {
				if v, ok := galleryAttrValue(a, f); ok {
					b.WriteString(v)
					b.WriteByte(' ')
				}
			}
			if !strings.Contains(strings.ToLower(b.String()), kw) {
				continue
			}
		}
		if !matchAttrFilters(a, filter.AttrFilters) {
			continue
		}
		if !matchAttrRules(a, filter) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// The two verdicts an attribute rule can carry.
const (
	galleryRuleOff = "off"
	galleryRuleOn  = "on"
)

// matchAttrRules judges one image against the per-value verdicts the gallery
// panel set up. A verdict only speaks about images that actually carry the
// value it names, so what decides is the image's own observed value, not the
// rule's existence. An image no rule touches stays visible.
func matchAttrRules(a types.ImageAsset, filter *types.ImageListFilter) bool {
	if filter == nil || len(filter.AttrRules) == 0 {
		return true
	}
	var offHit, onHit bool
	for name, verdicts := range filter.AttrRules {
		observed, ok := galleryAttrValue(a, name)
		if !ok {
			// Never looked at for this attribute, so no rule here has an
			// opinion about this image. Silence is not a verdict.
			continue
		}
		switch verdicts[observed] {
		case galleryRuleOff:
			offHit = true
		case galleryRuleOn:
			onHit = true
		}
	}
	// A forced display outranks a forced hide: the panel's "on" marks an
	// image someone deliberately asked to see, so it wins over a rule that
	// deliberately wants it gone.
	if onHit {
		return true
	}
	return !offHit
}

func matchAttrFilters(a types.ImageAsset, attrFilters map[string][]string) bool {
	for name, allowed := range attrFilters {
		if len(allowed) == 0 {
			continue
		}
		observed, ok := galleryAttrValue(a, name)
		if !ok {
			// An image that was never looked at for this attribute carries no
			// key at all, and "no key" must not read as "wrong key". A filter
			// narrows a list; taking the whole population out because a
			// pipeline has not reached these chunks yet is not narrowing, it
			// is losing the list. So the attribute places no constraint on the
			// image and stays out of the decision; re-running the pipeline
			// fills the attribute in, and the filter binds again from then on.
			continue
		}
		hit := false
		for _, want := range allowed {
			if observed == strings.TrimSpace(want) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// sortImageAssets orders the assets by the requested namespaced attribute.
// Legacy bare field names ("created_at" / "updated_at" / "caption") map onto
// their builtin ids. Attribute sorts compare the normalized string values;
// an image without a value for the sort attribute compares as empty.
func sortImageAssets(assets []types.ImageAsset, filter *types.ImageListFilter) {
	sortBy := types.GalleryAttrID(galleryBuiltinSourceID, "created_at")
	sortOrder := "desc"
	if filter != nil {
		switch filter.SortBy {
		case "created_at", "updated_at", "caption":
			sortBy = types.GalleryAttrID(galleryBuiltinSourceID, filter.SortBy)
		case "":
			// keep default
		default:
			sortBy = filter.SortBy
		}
		if filter.SortOrder == "asc" {
			sortOrder = "asc"
		}
	}
	sort.SliceStable(assets, func(i, j int) bool {
		less := galleryAssetLess(assets[i], assets[j], sortBy)
		if sortOrder == "desc" {
			return !less
		}
		return less
	})
}

func galleryAssetLess(a, b types.ImageAsset, sortBy string) bool {
	av, _ := galleryAttrValue(a, sortBy)
	bv, _ := galleryAttrValue(b, sortBy)
	// Timestamp fields compare chronologically; everything else as text.
	if av != "" && bv != "" {
		if at, err1 := time.Parse(time.RFC3339, av); err1 == nil {
			if bt, err2 := time.Parse(time.RFC3339, bv); err2 == nil {
				return at.Before(bt)
			}
		}
	}
	return strings.ToLower(av) < strings.ToLower(bv)
}
