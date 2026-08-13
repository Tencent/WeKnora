//go:build anydoc && cgo

package anydoc

import (
	"fmt"
	"mime"
	"strings"

	upstream "github.com/firecrawl/anydoc/go"
)

// This build links the anydoc Rust archive through the upstream cgo bindings
// (vendored under third_party/anydoc-go until they are published as a module).
// Conversion runs in-process: no subprocess, no HTTP hop, no Python.

func backendAvailable() bool { return true }

func backendUnavailableReason() string { return "" }

func backendVersion() string { return upstream.Version }

func backendConvert(data []byte, opts Options) (*Result, error) {
	format, err := upstreamFormat(opts.Format)
	if err != nil {
		return nil, err
	}

	if !opts.WithAssets {
		markdown, err := upstream.ToMarkdownBytes(data, format)
		if err != nil {
			return nil, fmt.Errorf("anydoc: markdown conversion failed: %w", err)
		}
		return &Result{Markdown: markdown}, nil
	}

	// Official GFM serializer after rewriting Asset images to External URLs
	// (`images/image-N.ext`). ToDocument still supplies the image bytes.
	document, err := upstream.ToDocument(data, format)
	if err != nil {
		markdown, mdErr := upstream.ToMarkdownBytes(data, format)
		if mdErr != nil {
			return nil, fmt.Errorf("anydoc: markdown conversion failed: %w", mdErr)
		}
		return &Result{
			Markdown:    markdown,
			AssetsError: fmt.Errorf("anydoc: image extraction failed: %w", err),
		}, nil
	}
	markdown, err := upstream.ToMarkdownWithAssetLinks(data, format)
	if err != nil {
		return nil, fmt.Errorf("anydoc: markdown conversion failed: %w", err)
	}
	return &Result{Markdown: markdown, Assets: collectAssets(document)}, nil
}

// upstreamFormat maps our format name onto the binding's constant. An empty
// name means "detect from content".
func upstreamFormat(name string) (*upstream.Format, error) {
	if name == "" {
		return nil, nil
	}
	format, ok := upstream.FormatFromExtension(name)
	if !ok {
		return nil, fmt.Errorf("anydoc: unsupported format %q", name)
	}
	return &format, nil
}

// collectAssets names each embedded image after its position in the document,
// because containers store assets without a usable file name of their own, and
// attaches the alt text and section the image appeared under.
func collectAssets(document *upstream.Document) []Asset {
	if document == nil || len(document.Assets) == 0 {
		return nil
	}
	placements := placeAssets(document.Blocks)
	assets := make([]Asset, 0, len(document.Assets))
	for i, asset := range document.Assets {
		if len(asset.Data) == 0 {
			continue
		}
		placement := placements[uint64(i)]
		assets = append(assets, Asset{
			ID:        uint64(i),
			Name:      fmt.Sprintf("image-%d%s", i+1, extensionFor(asset.MediaType)),
			MediaType: asset.MediaType,
			Data:      asset.Data,
			Alt:       placement.alt,
			Section:   placement.section,
		})
	}
	return assets
}

// placement is where in the document an asset was referenced from.
type placement struct {
	alt     string
	section string
}

// placeAssets walks the document body and records, per asset, the alt text of
// the image that referenced it and the heading it sat under. The walk
// recurses into lists, tables, and quotes, since an image can be nested
// anywhere a paragraph can.
func placeAssets(blocks []upstream.Block) map[uint64]placement {
	placements := make(map[uint64]placement)
	section := ""
	walkBlocks(blocks, &section, placements)
	return placements
}

func walkBlocks(blocks []upstream.Block, section *string, placements map[uint64]placement) {
	for _, block := range blocks {
		switch block.Kind {
		case "heading":
			*section = inlineText(block.Content)
			walkInlines(block.Content, *section, placements)
		case "paragraph":
			walkInlines(block.Content, *section, placements)
		case "block_quote":
			walkBlocks(block.Blocks, section, placements)
		case "list":
			if block.List != nil {
				for _, item := range block.List.Items {
					walkBlocks(item.Blocks, section, placements)
				}
			}
		case "table":
			if block.Table != nil {
				for _, row := range block.Table.Grid {
					for _, slot := range row {
						if slot.Cell != nil {
							walkBlocks(slot.Cell.Blocks, section, placements)
						}
					}
				}
			}
		}
	}
}

func walkInlines(inlines []upstream.Inline, section string, placements map[uint64]placement) {
	for _, inline := range inlines {
		switch inline.Kind {
		case "image":
			if inline.Source == nil || inline.Source.Kind != "asset" || inline.Source.AssetID == nil {
				continue
			}
			alt := ""
			if inline.Alt != nil {
				alt = strings.TrimSpace(*inline.Alt)
			}
			// First reference wins: the same asset can be placed more than
			// once, and the earliest placement is the one a reader would
			// associate it with.
			if _, seen := placements[*inline.Source.AssetID]; !seen {
				placements[*inline.Source.AssetID] = placement{alt: alt, section: section}
			}
		case "link":
			walkInlines(inline.Content, section, placements)
		}
	}
}

// inlineText flattens inline content to plain text, which is all a heading is
// needed for here.
func inlineText(inlines []upstream.Inline) string {
	var text strings.Builder
	for _, inline := range inlines {
		switch inline.Kind {
		case "text":
			if inline.Text != nil {
				text.WriteString(*inline.Text)
			}
		case "link":
			text.WriteString(inlineText(inline.Content))
		}
	}
	return strings.TrimSpace(text.String())
}

func extensionFor(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/svg+xml":
		return ".svg"
	}
	// Anything else (EMF/WMF drawings, unknown object payloads) keeps
	// whatever extension the media type registry knows, and .bin otherwise,
	// so downstream storage never writes an extension-less blob.
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}
