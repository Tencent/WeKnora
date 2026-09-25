package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// Source locator types. A locator points from a chunk back into the original
// uploaded file so a citation can open the file at the cited place. Every
// ordinal (page, slide, block, section, row) is 1-based.
const (
	// SourceLocatorPDF is a PDF page, optionally narrowed to a region.
	SourceLocatorPDF = "pdf"
	// SourceLocatorDocx is the Nth body-level paragraph or table of a Word file.
	SourceLocatorDocx = "docx"
	// SourceLocatorSlide is a presentation slide.
	SourceLocatorSlide = "slide"
	// SourceLocatorSheet is a row range of a spreadsheet sheet.
	SourceLocatorSheet = "sheet"
	// SourceLocatorText is a rune range of a plain-text original (md, txt, json).
	SourceLocatorText = "text"
	// SourceLocatorTime is a time range of an audio original.
	SourceLocatorTime = "time"
	// SourceLocatorSection is an EPUB spine item.
	SourceLocatorSection = "section"
)

// SourceLocatorQuoteMax caps the quote stored per locator, in runes.
const SourceLocatorQuoteMax = 300

// SourceLocator is one position in an original file. Only the fields of its
// Type are set; zero values are omitted from JSON and read back as zero.
type SourceLocator struct {
	Type string `json:"type"`
	// Page is the 1-based PDF page.
	Page int `json:"page,omitempty"`
	// BBox is [x0, y0, x1, y1] as fractions of the page width and height,
	// origin at the top-left corner of the rendered page.
	BBox []float64 `json:"bbox,omitempty"`
	// Block is the 1-based index among a Word file's body paragraphs and tables.
	Block int `json:"block,omitempty"`
	// Slide is the 1-based slide number in presentation order.
	Slide int `json:"slide,omitempty"`
	// Sheet is the spreadsheet sheet name; RowStart/RowEnd are its 1-based
	// row numbers as shown by spreadsheet applications.
	Sheet    string `json:"sheet,omitempty"`
	RowStart int    `json:"row_start,omitempty"`
	RowEnd   int    `json:"row_end,omitempty"`
	// Start/End are rune offsets into the original text file.
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
	// StartMs/EndMs bound an audio segment.
	StartMs int64 `json:"start_ms,omitempty"`
	EndMs   int64 `json:"end_ms,omitempty"`
	// Section is the 1-based EPUB spine index.
	Section int `json:"section,omitempty"`
	// Title names the section or slide when the source has one.
	Title string `json:"title,omitempty"`
	// Quote is the cited text as parsed, capped at SourceLocatorQuoteMax
	// runes. Viewers match it inside the rendered original and against the
	// answer sentence that cites the chunk.
	Quote string `json:"quote,omitempty"`
}

// SourceBlock ties a rune range of a parser's Markdown output to the place in
// the original file it came from.
type SourceBlock struct {
	Start   int
	End     int
	Locator SourceLocator
}

// SourceLocators is the per-chunk locator list, stored as a JSON column.
type SourceLocators []SourceLocator

// Scan implements sql.Scanner.
func (s *SourceLocators) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("SourceLocators: type assertion to []byte or string failed")
	}
	if len(raw) == 0 || string(raw) == "null" {
		*s = nil
		return nil
	}
	return json.Unmarshal(raw, s)
}

// Value implements driver.Valuer. An empty list is stored as NULL.
func (s SourceLocators) Value() (driver.Value, error) {
	if len(s) == 0 {
		return nil, nil
	}
	b, err := json.Marshal([]SourceLocator(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
