package types

// The system attribute registry doubles as a gallery attribute source: every
// attribute declared for the observe-and-describe pipeline is offered to the
// image gallery, defaulting to filterable (the observation values are small
// ordered sets, which is what the gallery's extent / presence filters render
// best). The gallery's config tiers can override these flags at runtime —
// this registration is only the lowest-priority fallback.
//
// This file depends on the gallery's neutral source hook
// (types/gallery_attr_source.go). Merge the gallery PR first, or land the
// hook file together with this commit — both PRs carry byte-identical copies
// of that file so either merge order resolves cleanly.
func init() {
	RegisterGalleryAttrSource("system", func(string) []GalleryAttrDef {
		defs := make([]GalleryAttrDef, 0, len(ImageAttrRegistry))
		for _, spec := range ImageAttrRegistry {
			values := make([]GalleryAttrValue, 0, len(spec.Values))
			for _, value := range spec.Values {
				values = append(values, GalleryAttrValue{
					Value:       value.Value,
					Label:       value.Label,
					Description: value.Description,
				})
			}
			defs = append(defs, GalleryAttrDef{
				Name:        spec.Name,
				Type:        string(spec.Type),
				Values:      values,
				Label:       spec.Label,
				Description: spec.Description,
				// Extent / presence observations are filter material by
				// default; searching and sorting on them is an operator
				// decision made through the gallery policy tiers.
				Usage: GalleryUsage{InFilter: true},
			})
		}
		return defs
	})
}
