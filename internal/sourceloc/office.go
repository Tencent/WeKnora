package sourceloc

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"

	"github.com/Tencent/WeKnora/internal/types"
)

// maxUnits bounds the structure extracted from one file.
const maxUnits = 200000

// maxZipEntryBytes bounds a single archive member read during extraction.
const maxZipEntryBytes = 64 << 20

// SupportsStructure reports whether StructureUnits can read fileType.
func SupportsStructure(fileType string) bool {
	switch normalizeType(fileType) {
	case "docx", "docm", "pptx", "pptm", "xlsx", "xlsm", "csv", "epub":
		return true
	}
	return false
}

// AlignStructure reads the structure of an office file and aligns the
// parser's markdown against it. It returns nil when the file type is not
// supported or nothing could be placed.
func AlignStructure(fileType string, data []byte, markdown string) ([]types.SourceBlock, error) {
	units, err := StructureUnits(fileType, data)
	if err != nil || len(units) == 0 {
		return nil, err
	}
	return Align(markdown, units), nil
}

// StructureUnits lists the structural pieces of an office file in document
// order, each with the locator it resolves to.
func StructureUnits(fileType string, data []byte) ([]Unit, error) {
	switch normalizeType(fileType) {
	case "docx", "docm":
		return docxUnits(data)
	case "pptx", "pptm":
		return pptxUnits(data)
	case "xlsx", "xlsm":
		return xlsxUnits(data)
	case "csv":
		return csvUnits(data)
	case "epub":
		return epubUnits(data)
	}
	return nil, nil
}

func normalizeType(fileType string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), "."))
}

// ---- docx -----------------------------------------------------------------

// docxUnits walks word/document.xml. Every body-level paragraph and table
// counts as one block, in the order Word lays them out; content controls
// (w:sdt) are transparent, as they are for the docx-preview renderer.
// Tables contribute one unit per row so long tables still align row by row.
func docxUnits(data []byte) ([]Unit, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	doc, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var (
		units    []Unit
		depth    int // element depth below w:body; sdt wrappers do not count
		inBody   bool
		block    int
		tblDepth int
		text     strings.Builder
		inText   bool
		skipText int
	)
	flush := func() {
		t := strings.TrimSpace(text.String())
		text.Reset()
		if t != "" && len(units) < maxUnits {
			loc := types.SourceLocator{Type: types.SourceLocatorDocx, Block: block}
			units = append(units, Unit{Text: t, Locator: loc})
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return units, nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if !inBody {
				if name == "body" {
					inBody = true
				}
				continue
			}
			if name == "sdt" || name == "sdtContent" || name == "sdtPr" || name == "sdtEndPr" {
				if name == "sdtPr" || name == "sdtEndPr" {
					skipText++
				}
				continue
			}
			depth++
			if depth == 1 && (name == "p" || name == "tbl") {
				block++
				text.Reset()
			}
			switch name {
			case "tbl":
				tblDepth++
			case "t":
				inText = true
			case "tab":
				text.WriteByte(' ')
			case "br", "cr":
				text.WriteByte(' ')
			case "delText", "instrText":
				skipText++
			}
		case xml.EndElement:
			name := t.Name.Local
			if !inBody {
				continue
			}
			if name == "body" {
				inBody = false
				continue
			}
			if name == "sdt" || name == "sdtContent" || name == "sdtPr" || name == "sdtEndPr" {
				if name == "sdtPr" || name == "sdtEndPr" {
					skipText--
				}
				continue
			}
			switch name {
			case "t":
				inText = false
			case "delText", "instrText":
				skipText--
			case "tr":
				if tblDepth == 1 {
					flush()
				}
			case "tc":
				text.WriteByte(' ')
			case "tbl":
				tblDepth--
			}
			if depth == 1 && name == "p" && tblDepth == 0 {
				flush()
			}
			depth--
		case xml.CharData:
			if inText && skipText == 0 {
				text.Write(t)
			}
		}
	}
	return units, nil
}

// ---- pptx -----------------------------------------------------------------

type pptxPresentation struct {
	SlideIDs []struct {
		RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sldIdLst>sldId"`
}

type opcRelationships struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// pptxUnits lists every text paragraph of every slide, and of its speaker
// notes, in presentation order. Paragraphs rather than whole slides are the
// units so a parser that orders shapes differently still aligns.
func pptxUnits(data []byte) ([]Unit, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	presXML, err := readZipFile(zr, "ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	var pres pptxPresentation
	if err := xml.Unmarshal(presXML, &pres); err != nil {
		return nil, err
	}
	rels, err := readRels(zr, "ppt/_rels/presentation.xml.rels")
	if err != nil {
		return nil, err
	}
	var units []Unit
	for i, sld := range pres.SlideIDs {
		target, ok := rels[sld.RID]
		if !ok {
			continue
		}
		slidePath := resolvePart("ppt", target.Target)
		slideXML, err := readZipFile(zr, slidePath)
		if err != nil {
			continue
		}
		loc := types.SourceLocator{Type: types.SourceLocatorSlide, Slide: i + 1}
		for _, p := range drawingParagraphs(slideXML) {
			units = append(units, Unit{Text: p, Locator: loc})
		}
		// Speaker notes render after the slide in every parser we use.
		slideRels, err := readRels(zr, relsPathFor(slidePath))
		if err == nil {
			for _, rel := range slideRels {
				if strings.HasSuffix(rel.Type, "/notesSlide") {
					notesXML, err := readZipFile(zr, resolvePart(path.Dir(slidePath), rel.Target))
					if err == nil {
						for _, p := range drawingParagraphs(notesXML) {
							units = append(units, Unit{Text: p, Locator: loc})
						}
					}
				}
			}
		}
		if len(units) >= maxUnits {
			break
		}
	}
	return units, nil
}

// drawingParagraphs returns the text of each DrawingML paragraph (a:p).
func drawingParagraphs(doc []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var (
		out    []string
		text   strings.Builder
		inText bool
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "br":
				text.WriteByte(' ')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if s := strings.TrimSpace(text.String()); s != "" {
					out = append(out, s)
				}
				text.Reset()
			}
		case xml.CharData:
			if inText {
				text.Write(t)
			}
		}
	}
	return out
}

// ---- xlsx / csv -----------------------------------------------------------

// xlsxUnits lists every non-empty row of every sheet with its row number.
func xlsxUnits(data []byte) ([]Unit, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var units []Unit
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for r, cells := range rows {
			text := strings.TrimSpace(strings.Join(cells, " "))
			if text == "" {
				continue
			}
			units = append(units, Unit{Text: text, Locator: types.SourceLocator{
				Type: types.SourceLocatorSheet, Sheet: sheet, RowStart: r + 1, RowEnd: r + 1,
			}})
			if len(units) >= maxUnits {
				return units, nil
			}
		}
	}
	return units, nil
}

// csvUnits lists every record of a CSV file as a row of its only sheet.
func csvUnits(data []byte) ([]Unit, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	var units []Unit
	for row := 1; ; row++ {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return units, nil
		}
		text := strings.TrimSpace(strings.Join(rec, " "))
		if text == "" {
			continue
		}
		units = append(units, Unit{Text: text, Locator: types.SourceLocator{
			Type: types.SourceLocatorSheet, RowStart: row, RowEnd: row,
		}})
		if len(units) >= maxUnits {
			break
		}
	}
	return units, nil
}

// ---- epub -----------------------------------------------------------------

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Manifest []struct {
		ID   string `xml:"id,attr"`
		Href string `xml:"href,attr"`
	} `xml:"manifest>item"`
	Spine []struct {
		IDRef string `xml:"idref,attr"`
	} `xml:"spine>itemref"`
}

// epubUnits lists the block-level text of every spine item. The section
// number is the item's 1-based position in the spine; the title is its first
// heading.
func epubUnits(data []byte) ([]Unit, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	containerXML, err := readZipFile(zr, "META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container epubContainer
	if err := xml.Unmarshal(containerXML, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, errors.New("epub: no rootfile")
	}
	opfPath := container.Rootfiles[0].FullPath
	opfXML, err := readZipFile(zr, opfPath)
	if err != nil {
		return nil, err
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return nil, err
	}
	hrefs := make(map[string]string, len(pkg.Manifest))
	for _, item := range pkg.Manifest {
		hrefs[item.ID] = item.Href
	}
	var units []Unit
	for i, ref := range pkg.Spine {
		href, ok := hrefs[ref.IDRef]
		if !ok {
			continue
		}
		doc, err := readZipFile(zr, resolvePart(path.Dir(opfPath), stripFragment(href)))
		if err != nil {
			continue
		}
		title, blocks := htmlBlocks(doc)
		loc := types.SourceLocator{Type: types.SourceLocatorSection, Section: i + 1, Title: title}
		for _, b := range blocks {
			units = append(units, Unit{Text: b, Locator: loc})
		}
		if len(units) >= maxUnits {
			break
		}
	}
	return units, nil
}

// htmlBlocks returns the first heading and the text of each block-level
// element of an XHTML document.
func htmlBlocks(doc []byte) (string, []string) {
	z := html.NewTokenizer(bytes.NewReader(doc))
	var (
		title     string
		blocks    []string
		text      strings.Builder
		inHeading bool
		skip      int
	)
	flush := func() {
		if s := strings.Join(strings.Fields(text.String()), " "); s != "" {
			blocks = append(blocks, s)
			if inHeading && title == "" {
				title = s
			}
		}
		text.Reset()
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			flush()
			return title, blocks
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if isHTMLBlock(tag) {
				flush()
				if strings.HasPrefix(tag, "h") && len(tag) == 2 {
					inHeading = tt == html.StartTagToken
				}
			}
		case html.TextToken:
			if skip == 0 {
				text.Write(z.Text())
				text.WriteByte(' ')
			}
		}
	}
}

func isHTMLBlock(tag string) bool {
	switch tag {
	case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
		"blockquote", "pre", "section", "article", "table", "dt", "dd", "br", "figcaption":
		return true
	}
	return false
}

// ---- zip helpers ----------------------------------------------------------

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			if f.UncompressedSize64 > maxZipEntryBytes {
				return nil, fmt.Errorf("%s: entry too large", name)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(io.LimitReader(rc, maxZipEntryBytes))
		}
	}
	return nil, fmt.Errorf("%s: not found", name)
}

func readRels(zr *zip.Reader, name string) (map[string]struct{ Type, Target string }, error) {
	raw, err := readZipFile(zr, name)
	if err != nil {
		return nil, err
	}
	var rels opcRelationships
	if err := xml.Unmarshal(raw, &rels); err != nil {
		return nil, err
	}
	out := make(map[string]struct{ Type, Target string }, len(rels.Items))
	for _, r := range rels.Items {
		out[r.ID] = struct{ Type, Target string }{r.Type, r.Target}
	}
	return out, nil
}

// relsPathFor returns the relationship part of an OPC part, e.g.
// ppt/slides/slide1.xml -> ppt/slides/_rels/slide1.xml.rels.
func relsPathFor(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// resolvePart resolves a relationship target against the directory of its
// source part. Absolute targets are rooted at the package root.
func resolvePart(dir, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return path.Clean(path.Join(dir, target))
}

func stripFragment(href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		return href[:i]
	}
	return href
}
