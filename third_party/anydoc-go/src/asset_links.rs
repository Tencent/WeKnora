//! Rewrite embedded images to markdown-linkable sources so anydoc's official
//! GFM serializer will emit `![alt](images/image-N.ext)` in place.
//!
//! `document_to_markdown` drops `ImageSource::Asset` to alt text because
//! Markdown cannot carry bytes. `ImageSource::External` is already rendered
//! as a normal image link. We keep the official serializer and only change
//! how those assets are addressed.

use anydoc::model::{Block, CellSlot, Document, ImageSource, Inline};

pub fn rewrite_asset_images(document: &mut Document) {
    let urls: Vec<Option<String>> = document
        .assets
        .iter()
        .enumerate()
        .map(|(i, asset)| {
            if asset.bytes.is_empty() {
                None
            } else {
                Some(format!("images/image-{}{}", i + 1, extension_for(&asset.media_type)))
            }
        })
        .collect();
    rewrite_blocks(&mut document.blocks, &urls);
    for note in &mut document.notes {
        rewrite_blocks(&mut note.blocks, &urls);
    }
}

fn rewrite_blocks(blocks: &mut [Block], urls: &[Option<String>]) {
    for block in blocks {
        match block {
            Block::Heading { content, .. } | Block::Paragraph(content) => {
                rewrite_inlines(content, urls);
            }
            Block::List(list) => {
                for item in &mut list.items {
                    rewrite_blocks(&mut item.blocks, urls);
                }
            }
            Block::Table(table) => {
                for row in &mut table.grid {
                    for slot in row {
                        if let CellSlot::Origin(cell) = slot {
                            rewrite_blocks(&mut cell.blocks, urls);
                        }
                    }
                }
            }
            Block::BlockQuote(inner) => rewrite_blocks(inner, urls),
            Block::CodeBlock { .. } | Block::Rule | Block::Math(_) => {}
        }
    }
}

fn rewrite_inlines(inlines: &mut [Inline], urls: &[Option<String>]) {
    for inline in inlines {
        match inline {
            Inline::Image { source, .. } => {
                if let ImageSource::Asset(id) = source {
                    if let Some(Some(url)) = urls.get(id.0) {
                        *source = ImageSource::External(url.clone());
                    }
                }
            }
            Inline::Link { content, .. } => rewrite_inlines(content, urls),
            _ => {}
        }
    }
}

/// Markdown file extension for one asset media type.
///
/// This table is one half of a contract with the Go side. The Markdown link
/// written here (`images/image-N<ext>`) is looked up by
/// `internal/infrastructure/docparser/image_resolver.go` in a map keyed by the
/// `ImageRef.OriginalRef` that `internal/infrastructure/docparser/anydoc/
/// backend_cgo.go` builds from the same media type. The two must agree
/// character for character or the image is dropped, so `extensionFor` there
/// lists exactly the same media types with exactly the same extensions.
fn extension_for(media_type: &str) -> &'static str {
    match media_type.to_ascii_lowercase().as_str() {
        "image/jpeg" | "image/jpg" => ".jpg",
        "image/png" => ".png",
        "image/gif" => ".gif",
        "image/webp" => ".webp",
        "image/bmp" => ".bmp",
        "image/tiff" => ".tiff",
        "image/svg+xml" => ".svg",
        // Vector metafiles. `shared::assets::media_type_for` and the OfficeArt
        // blip decoder both emit these for `.emf`/`.wmf` parts. They used to
        // fall through to ".bin" here while the Go side asked the platform MIME
        // registry and got ".emf"/".wmf", so the link never matched the ref and
        // the image never reached storage.
        "image/emf" => ".emf",
        "image/wmf" => ".wmf",
        _ => ".bin",
    }
}
