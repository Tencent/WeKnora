package types

import "time"

// ImageAsset is a single image projected from a chunk's image_info array.
// It is the unit the gallery lists, filters, sorts and serves.
type ImageAsset struct {
	// ID is stable and unique within a KB: "<chunkID>#<index-in-array>".
	ID string `json:"id"`
	// ChunkID is the owning chunk (text chunk for embedded images, or the
	// image_ocr / image_caption chunk for dedicated image assets).
	ChunkID string `json:"chunk_id"`
	// KnowledgeID is the document/knowledge item the image belongs to.
	KnowledgeID string `json:"knowledge_id"`
	// SourceName is the human-readable name of the source knowledge item
	// (document), resolved from KnowledgeID. Empty when the item no longer
	// exists (e.g. deleted), in which case KnowledgeID is still shown.
	SourceName string `json:"source_name"`
	// ChunkType is the owning chunk's type; empty for images discovered on a
	// text (document) chunk. Helps the UI label the source.
	ChunkType string `json:"chunk_type"`
	// URL is the rendered image URL (COS / storage provider).
	URL string `json:"url"`
	// OriginalURL is the pre-transform source reference, when different.
	OriginalURL string `json:"original_url"`
	// Caption is the model-generated image description.
	Caption string `json:"caption"`
	// OCRText is the extracted text from the image, if any.
	OCRText string `json:"ocr_text"`
	// Attrs is the observed attribute map (e.g. contain.text, contain.data_visual)
	// produced by the attribute pipeline. Absent keys mean "not observed".
	Attrs map[string]any `json:"attrs"`
	// IsEnabled mirrors the owning chunk's enabled flag.
	IsEnabled bool `json:"is_enabled"`
	// Status mirrors the owning chunk's index status.
	Status int `json:"status"`
	// CreatedAt is the owning chunk's creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the owning chunk's last update time.
	UpdatedAt time.Time `json:"updated_at"`
}

// ImageListFilter holds the gallery query constraints coming from the UI.
// Attribute references use namespaced gallery attribute ids
// ("<sourceID>:<name>", e.g. "builtin:caption", "system:contain.text").
type ImageListFilter struct {
	// Keyword is matched as a case-insensitive substring against the union
	// of the SearchIn fields' values.
	Keyword string
	// SearchIn lists the namespaced attribute ids to search. Empty means
	// the gallery default (builtin caption + ocr_text). The handler only
	// forwards ids whose resolved usage has in_searchfield=true.
	SearchIn []string
	// SortBy is a namespaced attribute id with in_sortfield=true. The bare
	// legacy values ("created_at", "updated_at", "caption") are still
	// accepted and mapped onto their builtin ids.
	SortBy string
	// SortOrder is "asc" or "desc".
	SortOrder string
	// AttrFilters maps a namespaced attribute id to the set of allowed
	// values. Values within one attribute are OR-ed; attributes are
	// AND-ed. An attribute present in this map but with no value on an
	// image fails the match (so "unobserved" is a distinct, filterable
	// state). The older, positional form of attribute filtering; see
	// AttrRules for the per-value verdicts the gallery panel now edits.
	AttrFilters map[string][]string
	// AttrRules maps a namespaced attribute id to a verdict per allowed
	// value: "" (or absent) leaves the image alone, "off" hides it, "on"
	// shows it. A verdict only speaks about images that actually carry
	// that value, so an image is judged by the rules it matches.
	//
	// When more than one rule speaks about the same image, "on" outranks
	// "off": a forced display beats a forced hide. Everything unclaimed
	// stays visible, which is what makes the default state usable — a
	// filter narrows the list, it never empties it.
	AttrRules map[string]map[string]string
	// IsEnabled, when non-nil, restricts to chunks with that enabled state.
	IsEnabled *bool
}

// ImageAssetField names one value of an image the gallery query can search,
// filter or sort on. Exactly one of the two is set.
type ImageAssetField struct {
	// Builtin is one of the builtin source's fields: "caption", "ocr_text",
	// "created_at", "updated_at" or "is_enabled".
	Builtin string
	// Attr is a source-local attribute name read from image_info attrs
	// (e.g. "contain.text").
	Attr string
}

// ImageAssetValueSet pairs a field with a set of raw values.
type ImageAssetValueSet struct {
	Field  ImageAssetField
	Values []string
}

// ImageAssetQuery is the storage-neutral form of one gallery page request. The
// service translates namespaced attribute ids into fields; the repository
// compiles it into SQL, so de-duplication, filtering, sorting and paging all
// run in the database and only the requested page leaves it.
type ImageAssetQuery struct {
	// Keyword is matched, lowercased, as a substring of the SearchFields'
	// values joined by spaces. Empty disables the keyword match.
	Keyword      string
	SearchFields []ImageAssetField
	// AttrFilters are AND-ed across sets and OR-ed within one; an image
	// without a value for the field is not constrained by it.
	AttrFilters []ImageAssetValueSet
	// OnRules / OffRules are the per-value verdicts: an image carrying an
	// "off" value is hidden unless it also carries an "on" value.
	OnRules  []ImageAssetValueSet
	OffRules []ImageAssetValueSet
	// IsEnabled, when non-nil, restricts to chunks with that enabled state.
	IsEnabled *bool
	SortField ImageAssetField
	SortDesc  bool
	Offset    int
	Limit     int
}

// ImageAssetRow is one de-duplicated image as the repository returns it: the
// owning chunk's columns plus the raw JSON of the image's image_info entry.
type ImageAssetRow struct {
	ChunkID     string    `gorm:"column:chunk_id"`
	KnowledgeID string    `gorm:"column:knowledge_id"`
	ChunkType   string    `gorm:"column:chunk_type"`
	IsEnabled   bool      `gorm:"column:is_enabled"`
	Status      int       `gorm:"column:status"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
	ImageIndex  int       `gorm:"column:image_index"`
	ImageJSON   string    `gorm:"column:image_json"`
	TotalCount  int64     `gorm:"column:total_count"`
}
