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
	// state).
	AttrFilters map[string][]string
	// IsEnabled, when non-nil, restricts to chunks with that enabled state.
	IsEnabled *bool
}
