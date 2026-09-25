package docparser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

// minerUBBoxScale is the coordinate range of content_list bounding boxes:
// MinerU normalizes them to 0-1000 of the page, origin at the top-left.
const minerUBBoxScale = 1000.0

// minerUContentItem is one layout block of MinerU's content_list output.
type minerUContentItem struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	PageIdx      int             `json:"page_idx"`
	BBox         []float64       `json:"bbox"`
	TableBody    string          `json:"table_body"`
	TableCaption json.RawMessage `json:"table_caption"`
	ImageCaption json.RawMessage `json:"image_caption"`
	ListItems    json.RawMessage `json:"list_items"`
	CodeBody     string          `json:"code_body"`
}

var minerUHTMLTagRe = regexp.MustCompile(`<[^>]+>`)

// text is the block's searchable text, in the order MinerU renders it to
// markdown: captions above tables and figures, then the body.
func (it minerUContentItem) text() string {
	parts := []string{it.Text}
	parts = append(parts, rawStrings(it.TableCaption)...)
	parts = append(parts, rawStrings(it.ImageCaption)...)
	if it.TableBody != "" {
		parts = append(parts, minerUHTMLTagRe.ReplaceAllString(it.TableBody, " "))
	}
	parts = append(parts, it.CodeBody)
	parts = append(parts, rawStrings(it.ListItems)...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

// rawStrings reads a JSON string or array of strings.
func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	return nil
}

// decodeMinerUContentList accepts the list itself or the list serialized as
// a JSON string, which is how some MinerU API versions return it.
func decodeMinerUContentList(raw []byte) []minerUContentItem {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var inner string
		if json.Unmarshal(raw, &inner) != nil {
			return nil
		}
		raw = []byte(inner)
	}
	var items []minerUContentItem
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// minerUContentListFromZip returns the shallowest *content_list.json of a
// MinerU result package, or nil.
func minerUContentListFromZip(zipData []byte) []byte {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil
	}
	var candidates []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, "content_list.json") && !strings.Contains(name, "content_list_v2") {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return strings.Count(candidates[i].Name, "/") < strings.Count(candidates[j].Name, "/")
	})
	data, err := readZipEntryBytes(candidates[0])
	if err != nil {
		return nil
	}
	return data
}

// minerUSourceBlocks aligns MinerU's layout blocks against its markdown so
// each paragraph, table and figure points back at its page and region. Only
// PDFs and images have pages a viewer can show; for office files MinerU's
// pages belong to an intermediate PDF, so those are left to the structure
// aligner.
func minerUSourceBlocks(markdown string, contentList []byte, fileType string) []types.SourceBlock {
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	if ft != "pdf" && !IsImageFormat(ft) {
		return nil
	}
	items := decodeMinerUContentList(contentList)
	if len(items) == 0 {
		return nil
	}
	units := make([]sourceloc.Unit, 0, len(items))
	for _, it := range items {
		loc := types.SourceLocator{Type: types.SourceLocatorPDF, Page: it.PageIdx + 1}
		if bbox, ok := minerUBBox(it.BBox); ok {
			loc.BBox = bbox
		}
		units = append(units, sourceloc.Unit{Text: it.text(), Locator: loc})
	}
	return sourceloc.Align(markdown, units)
}

// minerUBBox converts a 0-1000 content_list box to page fractions. Boxes
// outside that range come from MinerU versions that report other units and
// are dropped rather than drawn in the wrong place.
func minerUBBox(b []float64) ([]float64, bool) {
	if len(b) != 4 || b[2] <= b[0] || b[3] <= b[1] {
		return nil, false
	}
	for _, v := range b {
		if v < 0 || v > minerUBBoxScale {
			return nil, false
		}
	}
	out := make([]float64, 4)
	for i, v := range b {
		out[i] = v / minerUBBoxScale
	}
	return out, true
}
