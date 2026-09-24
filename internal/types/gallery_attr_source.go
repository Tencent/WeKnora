package types

import (
	"regexp"
	"sync"
)

// GalleryUsage declares how the image gallery may use one attribute. It is
// the "eligibility" layer: an attribute only appears in the gallery UI
// section (filter panel / search-field checkboxes / sort dropdown) that its
// usage flags allow. Whether a search field is actually active is decided
// separately by the user-level status layer, never here.
type GalleryUsage struct {
	// InFilter marks the attribute as renderable in the filter panel.
	InFilter bool `json:"in_filter"`
	// InSearchField marks the attribute as selectable in the search-field
	// checkboxes (the user still chooses per field whether to search it).
	InSearchField bool `json:"in_searchfield"`
	// InSortField marks the attribute as an option in the sort dropdown.
	InSortField bool `json:"in_sortfield"`
}

// GalleryAttrDef is one gallery-consumable attribute as a source declares
// it. Name is source-local (unprefixed); the gallery namespaces it under the
// declaring source's id when it builds the contract (see GalleryAttrID).
// Type drives frontend rendering: "text" (substring search), "date",
// "extent" (ordered value set -> multi-select), "presence" (boolean ->
// true/false checkboxes) and "keywords" (array of tags -> tag search).
type GalleryAttrDef struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Values      []string          `json:"values,omitempty"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	ValueLabels map[string]string `json:"value_labels,omitempty"`
	Usage       GalleryUsage      `json:"usage"`
}

// GalleryAttrSource is the aggregated view of one registered attribute
// source: the source id plus the attributes it provides for a KB.
type GalleryAttrSource struct {
	ID    string          `json:"id"`
	Attrs []GalleryAttrDef `json:"attrs"`
}

// gallerySourceIDPattern constrains source ids so the namespaced attribute
// id "<sourceID>:<name>" stays unambiguous (the separator is ":").
var gallerySourceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var (
	galleryAttrSourcesMu sync.RWMutex
	galleryAttrSources   []galleryAttrSourceEntry
)

type galleryAttrSourceEntry struct {
	id string
	// fn resolves the attributes the source provides for a KB. kbID lets a
	// future per-KB source (e.g. KB-defined attributes) contribute; global
	// sources ignore it.
	fn func(kbID string) []GalleryAttrDef
}

// RegisterGalleryAttrSource adds an attribute source to the gallery's open
// registry. Registration order is priority order: when two sources declare
// the same source-local attribute name, the namespaced ids keep both alive
// ("system:keyword" vs "kb:keyword"), and earlier-registered sources win
// raw-key lookups on tie. Call from init(); a nil fn or a malformed /
// duplicate id is a programming error and panics loudly.
func RegisterGalleryAttrSource(id string, fn func(kbID string) []GalleryAttrDef) {
	if fn == nil || !gallerySourceIDPattern.MatchString(id) {
		panic("gallery: invalid attribute source registration (id=" + id + ")")
	}
	galleryAttrSourcesMu.Lock()
	defer galleryAttrSourcesMu.Unlock()
	for _, e := range galleryAttrSources {
		if e.id == id {
			panic("gallery: duplicate attribute source id: " + id)
		}
	}
	galleryAttrSources = append(galleryAttrSources, galleryAttrSourceEntry{id: id, fn: fn})
}

// GalleryAttrSources resolves every registered source for a KB, in
// registration (priority) order. A source returning nothing yields an empty
// attribute list, not an error — an absent source is a normal state (e.g.
// the system attribute pipeline not deployed).
func GalleryAttrSources(kbID string) []GalleryAttrSource {
	galleryAttrSourcesMu.RLock()
	defer galleryAttrSourcesMu.RUnlock()
	out := make([]GalleryAttrSource, 0, len(galleryAttrSources))
	for _, e := range galleryAttrSources {
		defs := e.fn(kbID)
		if defs == nil {
			defs = []GalleryAttrDef{}
		}
		out = append(out, GalleryAttrSource{ID: e.id, Attrs: defs})
	}
	return out
}

// GalleryAttrSourceIDs lists the registered source ids in priority order.
func GalleryAttrSourceIDs() []string {
	galleryAttrSourcesMu.RLock()
	defer galleryAttrSourcesMu.RUnlock()
	out := make([]string, 0, len(galleryAttrSources))
	for _, e := range galleryAttrSources {
		out = append(out, e.id)
	}
	return out
}

// GalleryAttrID joins a source id and a source-local attribute name into the
// namespaced id used across the gallery contract and every config tier.
func GalleryAttrID(sourceID, name string) string {
	return sourceID + ":" + name
}

// SplitGalleryAttrID splits a namespaced attribute id back into its source
// id and source-local name. ok is false for ids without a separator.
func SplitGalleryAttrID(id string) (sourceID, name string, ok bool) {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[:i], id[i+1:], i > 0 && i+1 < len(id)
		}
	}
	return "", "", false
}
