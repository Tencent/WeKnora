package types

// The builtin attribute source models the image fields every gallery has —
// caption, OCR text, timestamps and the chunk enabled flag — as attributes,
// so the gallery's filter / search / sort UI is rendered from one uniform
// contract with no hardcoded special cases. Values of builtin attributes do
// not live in image_info.attrs; the service layer resolves them from the
// asset's own fields (see the service package's galleryAttrs helpers).
//
// Defaults are deliberately useful out of the box: caption and ocr_text are
// searchable, the timestamps sortable, and the enabled flag filterable.
// Every flag can be overridden per tier (system / KB / user) at runtime
// without touching this file.
func init() {
	RegisterGalleryAttrSource("builtin", func(string) []GalleryAttrDef {
		return []GalleryAttrDef{
			{
				Name:        "caption",
				Type:        "text",
				Label:       "Caption",
				Description: "The model-generated description of the image.",
				Usage:       GalleryUsage{InSearchField: true, InSortField: true},
			},
			{
				Name:        "ocr_text",
				Type:        "text",
				Label:       "OCR text",
				Description: "Text extracted from the image by OCR.",
				Usage:       GalleryUsage{InSearchField: true},
			},
			{
				Name:  "created_at",
				Type:  "date",
				Label: "Created at",
				Usage: GalleryUsage{InSortField: true},
			},
			{
				Name:  "updated_at",
				Type:  "date",
				Label: "Updated at",
				Usage: GalleryUsage{InSortField: true},
			},
			{
				Name:  "is_enabled",
				Type:  "presence",
				Values: []string{"true", "false"},
				Label: "Enabled",
				ValueLabels: map[string]string{
					"true":  "Enabled",
					"false": "Disabled",
				},
				Usage: GalleryUsage{InFilter: true},
			},
		}
	})
}
