package anydoc

/*
#include "include/anydoc.h"
*/
import "C"

import "fmt"

// Document is a parsed document: its body, its notes, and the bytes of
// everything it embedded. A Document is self-contained — embedded assets
// carry their bytes, so it stays usable after the source archive is gone.
type Document struct {
	Blocks []Block
	// Notes are footnote and endnote bodies, referenced from text by an
	// Inline with Kind == "note_ref".
	Notes []Note
	// Assets holds every embedded asset, indexed by Asset.ID as referenced
	// by an Inline image source.
	Assets []Asset
}

// Block is one block-level piece of a document body. The Kind field selects
// which of the optional fields is populated; the rest are nil.
type Block struct {
	// "heading", "paragraph", "list", "table", "block_quote", "code_block",
	// or "rule".
	Kind    string
	Level   *uint8   // heading: 1-based outline depth
	Anchor  *string  // heading: stable anchor id when the document targets it
	Content []Inline // heading, paragraph
	List    *List    // list
	Table   *Table   // table
	Blocks  []Block  // block_quote
	Lang    *string  // code_block
	Text    *string  // code_block
}

// Inline is one span of inline content. The Kind field selects which optional
// fields are populated.
type Inline struct {
	// "text", "link", "image", "anchor", "note_ref", or "line_break".
	Kind    string
	Text    *string      // text
	Style   *Style       // text
	Content []Inline     // link
	Target  *LinkTarget  // link
	Alt     *string      // image
	Source  *ImageSource // image
	Anchor  *string      // anchor: the anchor id
	NoteID  *string      // note_ref: the id of the note in Document.Notes
}

// Style is a fully resolved character style.
type Style struct {
	Bold   bool
	Italic bool
	Strike bool
	Code   bool
}

// LinkTarget is where a link points.
type LinkTarget struct {
	// "external" (absolute URL with a scheme), "relative" (scheme-less
	// relative reference, preserved as written), or "anchor" (internal
	// target: a heading anchor or an "anchor" inline).
	Kind  string
	Value string // the URL, relative reference, or anchor id
}

// ImageSource is where an image's bytes live.
type ImageSource struct {
	// "external" (absolute URL), "asset" (embedded image, carried in
	// Document.Assets), or "unavailable" (no usable source; only the alt
	// text remains).
	Kind    string
	URL     *string // external
	AssetID *uint64 // asset: index into Document.Assets
}

// List is a fully resolved list: numbering identity and marker resolution
// happen in the parser frontends.
type List struct {
	// "bullet", "decimal", "lower_alpha", "upper_alpha", "lower_roman", or
	// "upper_roman".
	Marker string
	Start  uint64
	Items  []ListItem
}

// ListItem is one item of a List, which may hold nested blocks including
// further lists.
type ListItem struct {
	Blocks      []Block
	Checked     *bool   // task-list state, when the item carries a checkbox
	MarkerLabel *string // literal marker text overriding the level marker
}

// Table is a canonical table grid: every logical grid position appears
// exactly once. Content and spans live on the origin slot, and each position
// a span covers holds a CellSlot with Kind == "covered" pointing back at that
// origin.
type Table struct {
	Grid       [][]CellSlot
	HeaderRows uint32 // number of leading rows that are header rows (0 = none)
	Kind       string // "data" or "layout"
}

// CellSlot is one position in a Table grid: either a cell or the shadow of
// one.
type CellSlot struct {
	// "origin" (the cell itself) or "covered" (a position swallowed by a
	// span, pointing back at the origin that covers it).
	Kind      string
	Cell      *Cell   // origin
	OriginRow *uint32 // covered: row of the covering origin
	OriginCol *uint32 // covered: column of the covering origin
}

// Cell is a table cell and the extent it spans.
type Cell struct {
	Blocks  []Block
	ColSpan uint32
	RowSpan uint32
}

// Note is a footnote or endnote body, referenced from text by an Inline with
// Kind == "note_ref".
type Note struct {
	ID     string
	Kind   string // "footnote" or "endnote"
	Blocks []Block
}

// Asset is an embedded binary asset (image, object payload). Bytes are always
// retained, so a document stays self-contained.
type Asset struct {
	ID         uint64 // index into Document.Assets, as referenced by an image source
	MediaType  string
	OriginPart string
	Data       []byte
}

// ---- Decoding the flat FFI buffer -----------------------------------------
//
// The buffer is the flat, length-prefixed serialization written by
// go/src/model.rs. Every field is little-endian. A decoder tracks the read
// cursor and reports a short or implausible buffer as an error — the Rust side
// always produces a well-formed buffer, so a short read is a bug or a version
// skew, not user input, and it must not take the process down.

type decoder struct {
	buf []byte
	pos int
}

func decodeDocument(raw []byte) (*Document, error) {
	d := &decoder{buf: raw}
	doc := &Document{}
	var err error
	if doc.Blocks, err = d.blocks(); err != nil {
		return nil, err
	}
	if doc.Notes, err = d.notes(); err != nil {
		return nil, err
	}
	if doc.Assets, err = d.assets(); err != nil {
		return nil, err
	}
	return doc, nil
}

// capFor bounds a preallocation by what is left in the buffer. Every element
// costs at least one byte to encode, so a count larger than the remaining
// bytes can only come from a corrupt buffer or a Rust/Go version skew — and
// preallocating it would exhaust memory before the first bounds check runs.
func (d *decoder) capFor(n uint32) int {
	remaining := len(d.buf) - d.pos
	if remaining < 0 {
		return 0
	}
	if uint64(n) > uint64(remaining) {
		return remaining
	}
	return int(n)
}

func (d *decoder) need(n int) error {
	// A negative n means a length prefix overflowed int (32-bit builds); it is
	// as unreadable as a truncated buffer.
	if n < 0 || d.pos+n > len(d.buf) {
		return fmt.Errorf("anydoc: truncated document buffer at offset %d (need %d bytes, have %d)", d.pos, n, len(d.buf)-d.pos)
	}
	return nil
}

func (d *decoder) u8() (uint8, error) {
	if err := d.need(1); err != nil {
		return 0, err
	}
	v := d.buf[d.pos]
	d.pos++
	return v, nil
}

func (d *decoder) bool() (bool, error) {
	v, err := d.u8()
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

func (d *decoder) u32() (uint32, error) {
	if err := d.need(4); err != nil {
		return 0, err
	}
	v := uint32(d.buf[d.pos]) | uint32(d.buf[d.pos+1])<<8 | uint32(d.buf[d.pos+2])<<16 | uint32(d.buf[d.pos+3])<<24
	d.pos += 4
	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if err := d.need(8); err != nil {
		return 0, err
	}
	v := uint64(d.buf[d.pos]) | uint64(d.buf[d.pos+1])<<8 | uint64(d.buf[d.pos+2])<<16 | uint64(d.buf[d.pos+3])<<24 |
		uint64(d.buf[d.pos+4])<<32 | uint64(d.buf[d.pos+5])<<40 | uint64(d.buf[d.pos+6])<<48 | uint64(d.buf[d.pos+7])<<56
	d.pos += 8
	return v, nil
}

func (d *decoder) i32() (int32, error) {
	u, err := d.u32()
	if err != nil {
		return 0, err
	}
	return int32(u), nil
}

// optStr reads a length-prefixed string or a -1 sentinel (None).
func (d *decoder) optStr() (*string, error) {
	length, err := d.i32()
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	s, err := d.strN(int(length))
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *decoder) str() (string, error) {
	length, err := d.u32()
	if err != nil {
		return "", err
	}
	return d.strN(int(length))
}

func (d *decoder) strN(n int) (string, error) {
	if err := d.need(n); err != nil {
		return "", err
	}
	s := string(d.buf[d.pos : d.pos+n])
	d.pos += n
	return s, nil
}

func (d *decoder) bytes() ([]byte, error) {
	length, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.need(int(length)); err != nil {
		return nil, err
	}
	b := make([]byte, length)
	copy(b, d.buf[d.pos:d.pos+int(length)])
	d.pos += int(length)
	return b, nil
}

func (d *decoder) blocks() ([]Block, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0, d.capFor(n))
	for i := uint32(0); i < n; i++ {
		b, err := d.block()
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (d *decoder) block() (Block, error) {
	tag, err := d.i32()
	if err != nil {
		return Block{}, err
	}
	switch tag {
	case C.BLOCK_HEADING:
		level, err := d.u8()
		if err != nil {
			return Block{}, err
		}
		anchor, err := d.optStr()
		if err != nil {
			return Block{}, err
		}
		content, err := d.inlines()
		if err != nil {
			return Block{}, err
		}
		lv := level
		return Block{Kind: "heading", Level: &lv, Anchor: anchor, Content: content}, nil
	case C.BLOCK_PARAGRAPH:
		content, err := d.inlines()
		if err != nil {
			return Block{}, err
		}
		return Block{Kind: "paragraph", Content: content}, nil
	case C.BLOCK_LIST:
		list, err := d.list()
		if err != nil {
			return Block{}, err
		}
		return Block{Kind: "list", List: list}, nil
	case C.BLOCK_TABLE:
		table, err := d.table()
		if err != nil {
			return Block{}, err
		}
		return Block{Kind: "table", Table: table}, nil
	case C.BLOCK_QUOTE:
		inner, err := d.blocks()
		if err != nil {
			return Block{}, err
		}
		return Block{Kind: "block_quote", Blocks: inner}, nil
	case C.BLOCK_CODE:
		lang, err := d.optStr()
		if err != nil {
			return Block{}, err
		}
		text, err := d.str()
		if err != nil {
			return Block{}, err
		}
		return Block{Kind: "code_block", Lang: lang, Text: &text}, nil
	case C.BLOCK_RULE:
		return Block{Kind: "rule"}, nil
	default:
		return Block{}, fmt.Errorf("anydoc: unknown block kind tag %d", tag)
	}
}

func (d *decoder) inlines() ([]Inline, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	out := make([]Inline, 0, d.capFor(n))
	for i := uint32(0); i < n; i++ {
		in, err := d.inline()
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

func (d *decoder) inline() (Inline, error) {
	tag, err := d.i32()
	if err != nil {
		return Inline{}, err
	}
	switch tag {
	case C.INLINE_TEXT:
		text, err := d.str()
		if err != nil {
			return Inline{}, err
		}
		style, err := d.style()
		if err != nil {
			return Inline{}, err
		}
		return Inline{Kind: "text", Text: &text, Style: &style}, nil
	case C.INLINE_LINK:
		content, err := d.inlines()
		if err != nil {
			return Inline{}, err
		}
		target, err := d.linkTarget()
		if err != nil {
			return Inline{}, err
		}
		return Inline{Kind: "link", Content: content, Target: &target}, nil
	case C.INLINE_IMAGE:
		alt, err := d.str()
		if err != nil {
			return Inline{}, err
		}
		src, err := d.imageSource()
		if err != nil {
			return Inline{}, err
		}
		return Inline{Kind: "image", Alt: &alt, Source: &src}, nil
	case C.INLINE_ANCHOR:
		id, err := d.str()
		if err != nil {
			return Inline{}, err
		}
		return Inline{Kind: "anchor", Anchor: &id}, nil
	case C.INLINE_NOTEREF:
		id, err := d.str()
		if err != nil {
			return Inline{}, err
		}
		return Inline{Kind: "note_ref", NoteID: &id}, nil
	case C.INLINE_LINEBREAK:
		return Inline{Kind: "line_break"}, nil
	default:
		return Inline{}, fmt.Errorf("anydoc: unknown inline kind tag %d", tag)
	}
}

func (d *decoder) style() (Style, error) {
	bold, err := d.bool()
	if err != nil {
		return Style{}, err
	}
	italic, err := d.bool()
	if err != nil {
		return Style{}, err
	}
	strike, err := d.bool()
	if err != nil {
		return Style{}, err
	}
	code, err := d.bool()
	if err != nil {
		return Style{}, err
	}
	return Style{Bold: bold, Italic: italic, Strike: strike, Code: code}, nil
}

func (d *decoder) linkTarget() (LinkTarget, error) {
	tag, err := d.i32()
	if err != nil {
		return LinkTarget{}, err
	}
	value, err := d.str()
	if err != nil {
		return LinkTarget{}, err
	}
	switch tag {
	case C.LINK_EXTERNAL:
		return LinkTarget{Kind: "external", Value: value}, nil
	case C.LINK_RELATIVE:
		return LinkTarget{Kind: "relative", Value: value}, nil
	case C.LINK_ANCHOR:
		return LinkTarget{Kind: "anchor", Value: value}, nil
	default:
		return LinkTarget{}, fmt.Errorf("anydoc: unknown link target tag %d", tag)
	}
}

func (d *decoder) imageSource() (ImageSource, error) {
	tag, err := d.i32()
	if err != nil {
		return ImageSource{}, err
	}
	switch tag {
	case C.IMG_EXTERNAL:
		url, err := d.str()
		if err != nil {
			return ImageSource{}, err
		}
		return ImageSource{Kind: "external", URL: &url}, nil
	case C.IMG_ASSET:
		id, err := d.u64()
		if err != nil {
			return ImageSource{}, err
		}
		return ImageSource{Kind: "asset", AssetID: &id}, nil
	case C.IMG_UNAVAILABLE:
		return ImageSource{Kind: "unavailable"}, nil
	default:
		return ImageSource{}, fmt.Errorf("anydoc: unknown image source tag %d", tag)
	}
}

func (d *decoder) list() (*List, error) {
	markerTag, err := d.i32()
	if err != nil {
		return nil, err
	}
	start, err := d.u64()
	if err != nil {
		return nil, err
	}
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	items := make([]ListItem, 0, d.capFor(n))
	for i := uint32(0); i < n; i++ {
		item, err := d.listItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	marker, err := markerName(markerTag)
	if err != nil {
		return nil, err
	}
	return &List{Marker: marker, Start: start, Items: items}, nil
}

func (d *decoder) listItem() (ListItem, error) {
	blocks, err := d.blocks()
	if err != nil {
		return ListItem{}, err
	}
	checkedTag, err := d.i32()
	if err != nil {
		return ListItem{}, err
	}
	var checked *bool
	switch checkedTag {
	case 0:
		f := false
		checked = &f
	case 1:
		t := true
		checked = &t
	case -1:
		checked = nil
	default:
		return ListItem{}, fmt.Errorf("anydoc: unknown checked tag %d", checkedTag)
	}
	label, err := d.optStr()
	if err != nil {
		return ListItem{}, err
	}
	return ListItem{Blocks: blocks, Checked: checked, MarkerLabel: label}, nil
}

func (d *decoder) table() (*Table, error) {
	headerRows, err := d.u32()
	if err != nil {
		return nil, err
	}
	kindTag, err := d.i32()
	if err != nil {
		return nil, err
	}
	rows, err := d.u32()
	if err != nil {
		return nil, err
	}
	grid := make([][]CellSlot, 0, d.capFor(rows))
	for i := uint32(0); i < rows; i++ {
		cols, err := d.u32()
		if err != nil {
			return nil, err
		}
		row := make([]CellSlot, 0, d.capFor(cols))
		for j := uint32(0); j < cols; j++ {
			slot, err := d.slot()
			if err != nil {
				return nil, err
			}
			row = append(row, slot)
		}
		grid = append(grid, row)
	}
	kind := "data"
	if kindTag == C.TABLE_LAYOUT {
		kind = "layout"
	}
	return &Table{Grid: grid, HeaderRows: headerRows, Kind: kind}, nil
}

func (d *decoder) slot() (CellSlot, error) {
	tag, err := d.i32()
	if err != nil {
		return CellSlot{}, err
	}
	switch tag {
	case C.SLOT_ORIGIN:
		cell, err := d.cell()
		if err != nil {
			return CellSlot{}, err
		}
		return CellSlot{Kind: "origin", Cell: &cell}, nil
	case C.SLOT_COVERED:
		r, err := d.u32()
		if err != nil {
			return CellSlot{}, err
		}
		c, err := d.u32()
		if err != nil {
			return CellSlot{}, err
		}
		rr := r
		cc := c
		return CellSlot{Kind: "covered", OriginRow: &rr, OriginCol: &cc}, nil
	default:
		return CellSlot{}, fmt.Errorf("anydoc: unknown cell slot tag %d", tag)
	}
}

func (d *decoder) cell() (Cell, error) {
	colSpan, err := d.u32()
	if err != nil {
		return Cell{}, err
	}
	rowSpan, err := d.u32()
	if err != nil {
		return Cell{}, err
	}
	blocks, err := d.blocks()
	if err != nil {
		return Cell{}, err
	}
	return Cell{Blocks: blocks, ColSpan: colSpan, RowSpan: rowSpan}, nil
}

func (d *decoder) notes() ([]Note, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	out := make([]Note, 0, d.capFor(n))
	for i := uint32(0); i < n; i++ {
		note, err := d.note()
		if err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, nil
}

func (d *decoder) note() (Note, error) {
	id, err := d.str()
	if err != nil {
		return Note{}, err
	}
	kindTag, err := d.i32()
	if err != nil {
		return Note{}, err
	}
	blocks, err := d.blocks()
	if err != nil {
		return Note{}, err
	}
	kind := "footnote"
	if kindTag == C.NOTE_ENDNOTE {
		kind = "endnote"
	}
	return Note{ID: id, Kind: kind, Blocks: blocks}, nil
}

func (d *decoder) assets() ([]Asset, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	out := make([]Asset, 0, d.capFor(n))
	for i := uint32(0); i < n; i++ {
		asset, err := d.asset()
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, nil
}

func (d *decoder) asset() (Asset, error) {
	id, err := d.u64()
	if err != nil {
		return Asset{}, err
	}
	mediaType, err := d.str()
	if err != nil {
		return Asset{}, err
	}
	originPart, err := d.str()
	if err != nil {
		return Asset{}, err
	}
	data, err := d.bytes()
	if err != nil {
		return Asset{}, err
	}
	return Asset{ID: id, MediaType: mediaType, OriginPart: originPart, Data: data}, nil
}

func markerName(tag int32) (string, error) {
	switch tag {
	case C.MARKER_BULLET:
		return "bullet", nil
	case C.MARKER_DECIMAL:
		return "decimal", nil
	case C.MARKER_LOWER_ALPHA:
		return "lower_alpha", nil
	case C.MARKER_UPPER_ALPHA:
		return "upper_alpha", nil
	case C.MARKER_LOWER_ROMAN:
		return "lower_roman", nil
	case C.MARKER_UPPER_ROMAN:
		return "upper_roman", nil
	}
	return "", fmt.Errorf("anydoc: unknown list marker tag %d", tag)
}
