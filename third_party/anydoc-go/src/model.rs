//! `#[repr(C)]` DTOs and a flat, self-describing serialization of the document
//! model.
//!
//! The document tree is recursive (blocks hold blocks, inlines hold inlines,
//! tables hold cells that hold blocks), so rather than surfacing it through a
//! forest of opaque handles and one `_free` per node type, the FFI serializes
//! the whole `Document` into a single length-prefixed byte buffer. That gives
//! exactly one allocation family for the tree (`anydoc_buffer_free`) and keeps
//! the C ABI tiny. Variant tags are written inline as `c_int`-compatible
//! values, mirroring the Node/Python `kind` string convention.
//!
//! Format: see `write_document` below. Every field is length-prefixed so the
//! Go side can decode without guessing. Strings carry their byte length as a
//! u32; slices carry a u32 count followed by that many elements. Booleans are
//! a single byte (0 or 1). Integers are little-endian fixed width.

use std::os::raw::c_int;

use anydoc::model;

/// Tag values for `Block` variants. Stable across versions; the Go side maps
/// these to the same lowercase string names the Node and Python bindings use.
pub const BLOCK_HEADING: c_int = 0;
pub const BLOCK_PARAGRAPH: c_int = 1;
pub const BLOCK_LIST: c_int = 2;
pub const BLOCK_TABLE: c_int = 3;
pub const BLOCK_QUOTE: c_int = 4;
pub const BLOCK_CODE: c_int = 5;
pub const BLOCK_RULE: c_int = 6;

pub const INLINE_TEXT: c_int = 0;
pub const INLINE_LINK: c_int = 1;
pub const INLINE_IMAGE: c_int = 2;
pub const INLINE_ANCHOR: c_int = 3;
pub const INLINE_NOTEREF: c_int = 4;
pub const INLINE_LINEBREAK: c_int = 5;

pub const LINK_EXTERNAL: c_int = 0;
pub const LINK_RELATIVE: c_int = 1;
pub const LINK_ANCHOR: c_int = 2;

pub const IMG_EXTERNAL: c_int = 0;
pub const IMG_ASSET: c_int = 1;
pub const IMG_UNAVAILABLE: c_int = 2;

pub const MARKER_BULLET: c_int = 0;
pub const MARKER_DECIMAL: c_int = 1;
pub const MARKER_LOWER_ALPHA: c_int = 2;
pub const MARKER_UPPER_ALPHA: c_int = 3;
pub const MARKER_LOWER_ROMAN: c_int = 4;
pub const MARKER_UPPER_ROMAN: c_int = 5;

pub const TABLE_DATA: c_int = 0;
pub const TABLE_LAYOUT: c_int = 1;

pub const SLOT_ORIGIN: c_int = 0;
pub const SLOT_COVERED: c_int = 1;

pub const NOTE_FOOTNOTE: c_int = 0;
pub const NOTE_ENDNOTE: c_int = 1;

/// Encoder for the flat document buffer. Little-endian, length-prefixed.
pub struct Encoder {
    buf: Vec<u8>,
}

impl Encoder {
    pub fn new() -> Self {
        Encoder { buf: Vec::with_capacity(8 * 1024) }
    }

    pub fn into_vec(self) -> Vec<u8> {
        self.buf
    }

    pub fn u8(&mut self, v: u8) {
        self.buf.push(v);
    }

    pub fn bool(&mut self, v: bool) {
        self.buf.push(if v { 1 } else { 0 });
    }

    pub fn u32(&mut self, v: u32) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn u64(&mut self, v: u64) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn i32(&mut self, v: i32) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    /// A length-prefixed UTF-8 string. `None` writes a -1 length sentinel.
    pub fn opt_str(&mut self, v: &Option<String>) {
        match v {
            Some(s) => self.str(s),
            None => self.i32(-1),
        }
    }

    pub fn str(&mut self, s: &str) {
        self.u32(s.len() as u32);
        self.buf.extend_from_slice(s.as_bytes());
    }

    /// A length-prefixed byte slice.
    pub fn bytes(&mut self, b: &[u8]) {
        self.u32(b.len() as u32);
        self.buf.extend_from_slice(b);
    }
}

pub fn write_document(e: &mut Encoder, doc: &model::Document) {
    write_blocks(e, &doc.blocks);
    e.u32(doc.notes.len() as u32);
    for note in &doc.notes {
        write_note(e, note);
    }
    e.u32(doc.assets.len() as u32);
    for asset in &doc.assets {
        write_asset(e, asset);
    }
}

fn write_blocks(e: &mut Encoder, blocks: &[model::Block]) {
    e.u32(blocks.len() as u32);
    for block in blocks {
        write_block(e, block);
    }
}

fn write_block(e: &mut Encoder, block: &model::Block) {
    match block {
        model::Block::Heading { level, anchor, content } => {
            e.i32(BLOCK_HEADING);
            e.u8(*level);
            e.opt_str(anchor);
            write_inlines(e, content);
        }
        model::Block::Paragraph(content) => {
            e.i32(BLOCK_PARAGRAPH);
            write_inlines(e, content);
        }
        model::Block::List(list) => {
            e.i32(BLOCK_LIST);
            write_list(e, list);
        }
        model::Block::Table(table) => {
            e.i32(BLOCK_TABLE);
            write_table(e, table);
        }
        model::Block::BlockQuote(inner) => {
            e.i32(BLOCK_QUOTE);
            write_blocks(e, inner);
        }
        model::Block::CodeBlock { lang, text } => {
            e.i32(BLOCK_CODE);
            e.opt_str(lang);
            e.str(text);
        }
        model::Block::Rule => {
            e.i32(BLOCK_RULE);
        }
    }
}

fn write_inlines(e: &mut Encoder, inlines: &[model::Inline]) {
    e.u32(inlines.len() as u32);
    for inline in inlines {
        write_inline(e, inline);
    }
}

fn write_inline(e: &mut Encoder, inline: &model::Inline) {
    match inline {
        model::Inline::Text { text, style } => {
            e.i32(INLINE_TEXT);
            e.str(text);
            write_style(e, *style);
        }
        model::Inline::Link { content, target } => {
            e.i32(INLINE_LINK);
            write_inlines(e, content);
            write_link_target(e, target);
        }
        model::Inline::Image { alt, source } => {
            e.i32(INLINE_IMAGE);
            e.str(alt);
            write_image_source(e, source);
        }
        model::Inline::Anchor(id) => {
            e.i32(INLINE_ANCHOR);
            e.str(id);
        }
        model::Inline::NoteRef(id) => {
            e.i32(INLINE_NOTEREF);
            e.str(id);
        }
        model::Inline::LineBreak => {
            e.i32(INLINE_LINEBREAK);
        }
    }
}

fn write_style(e: &mut Encoder, style: model::Style) {
    e.bool(style.bold);
    e.bool(style.italic);
    e.bool(style.strike);
    e.bool(style.code);
}

fn write_link_target(e: &mut Encoder, target: &model::LinkTarget) {
    match target {
        model::LinkTarget::External(v) => {
            e.i32(LINK_EXTERNAL);
            e.str(v);
        }
        model::LinkTarget::Relative(v) => {
            e.i32(LINK_RELATIVE);
            e.str(v);
        }
        model::LinkTarget::Anchor(v) => {
            e.i32(LINK_ANCHOR);
            e.str(v);
        }
    }
}

fn write_image_source(e: &mut Encoder, source: &model::ImageSource) {
    match source {
        model::ImageSource::External(url) => {
            e.i32(IMG_EXTERNAL);
            e.str(url);
        }
        model::ImageSource::Asset(id) => {
            e.i32(IMG_ASSET);
            e.u64(id.0 as u64);
        }
        model::ImageSource::Unavailable => {
            e.i32(IMG_UNAVAILABLE);
        }
    }
}

fn write_list(e: &mut Encoder, list: &model::List) {
    e.i32(marker_tag(list.marker));
    e.u64(list.start);
    e.u32(list.items.len() as u32);
    for item in &list.items {
        write_list_item(e, item);
    }
}

fn write_list_item(e: &mut Encoder, item: &model::ListItem) {
    write_blocks(e, &item.blocks);
    match item.checked {
        None => e.i32(-1),
        Some(false) => e.i32(0),
        Some(true) => e.i32(1),
    }
    e.opt_str(&item.marker_label);
}

fn write_table(e: &mut Encoder, table: &model::Table) {
    e.u32(table.header_rows as u32);
    e.i32(if table.kind == model::TableKind::Layout { TABLE_LAYOUT } else { TABLE_DATA });
    e.u32(table.grid.len() as u32);
    for row in &table.grid {
        e.u32(row.len() as u32);
        for slot in row {
            write_slot(e, slot);
        }
    }
}

fn write_slot(e: &mut Encoder, slot: &model::CellSlot) {
    match slot {
        model::CellSlot::Origin(cell) => {
            e.i32(SLOT_ORIGIN);
            write_cell(e, cell);
        }
        model::CellSlot::Covered { origin_row, origin_col } => {
            e.i32(SLOT_COVERED);
            e.u32(*origin_row as u32);
            e.u32(*origin_col as u32);
        }
    }
}

fn write_cell(e: &mut Encoder, cell: &model::Cell) {
    e.u32(cell.col_span);
    e.u32(cell.row_span);
    write_blocks(e, &cell.blocks);
}

fn write_note(e: &mut Encoder, note: &model::Note) {
    e.str(&note.id);
    e.i32(if note.kind == model::NoteKind::Endnote { NOTE_ENDNOTE } else { NOTE_FOOTNOTE });
    write_blocks(e, &note.blocks);
}

fn write_asset(e: &mut Encoder, asset: &model::Asset) {
    e.u64(asset.id.0 as u64);
    e.str(&asset.media_type);
    e.str(&asset.origin_part);
    e.bytes(&asset.bytes);
}

fn marker_tag(marker: model::MarkerKind) -> c_int {
    match marker {
        model::MarkerKind::Bullet => MARKER_BULLET,
        model::MarkerKind::Decimal => MARKER_DECIMAL,
        model::MarkerKind::LowerAlpha => MARKER_LOWER_ALPHA,
        model::MarkerKind::UpperAlpha => MARKER_UPPER_ALPHA,
        model::MarkerKind::LowerRoman => MARKER_LOWER_ROMAN,
        model::MarkerKind::UpperRoman => MARKER_UPPER_ROMAN,
    }
}
